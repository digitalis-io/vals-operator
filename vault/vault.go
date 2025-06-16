package vault

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	dmetrics "digitalis.io/vals-operator/metrics"
	"digitalis.io/vals-operator/utils"
	"github.com/hashicorp/vault/api"
	vault "github.com/hashicorp/vault/api"
	vaultApprole "github.com/hashicorp/vault/api/auth/approle"
	vaultKube "github.com/hashicorp/vault/api/auth/kubernetes"
	vaultUserpass "github.com/hashicorp/vault/api/auth/userpass"

	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	kubernetesMountPath   = "kubernetes"
	approleMountPath      = "approle"
	userpassRoleMountPath = "userpass"
	maxRetries            = 3
	retryDelay            = 2 * time.Second
)

var (
	log          logr.Logger
	vaultURL     string = getEnv("VAULT_ADDR", "http://vault:8200")
	client       *vault.Client
	clientMutex  sync.RWMutex
	currentToken string
	tokenMutex   sync.RWMutex
)

func getEnv(key string, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

// setCurrentToken safely updates the current token
func setCurrentToken(token string) {
	tokenMutex.Lock()
	defer tokenMutex.Unlock()

	currentToken = token

	// Update the client with the new token
	clientMutex.Lock()
	defer clientMutex.Unlock()

	if client != nil {
		client.SetToken(token)
	}
}

// getCurrentToken safely retrieves the current token
func getCurrentToken() string {
	tokenMutex.RLock()
	defer tokenMutex.RUnlock()
	return currentToken
}

// getOrCreateClient returns the vault client, creating it if necessary
func getOrCreateClient() (*vault.Client, error) {
	clientMutex.RLock()
	if client != nil {
		clientMutex.RUnlock()
		return client, nil
	}
	clientMutex.RUnlock()

	// Need to create client
	clientMutex.Lock()
	defer clientMutex.Unlock()

	// Double-check after acquiring write lock
	if client != nil {
		return client, nil
	}

	newClient, err := createVaultClient()
	if err != nil {
		return nil, err
	}

	client = newClient
	return client, nil
}

// createVaultClient creates a new vault client instance
func createVaultClient() (*api.Client, error) {
	var vaultSkipVerify bool = false

	if os.Getenv("VAULT_SKIP_VERIFY") != "" && os.Getenv("VAULT_SKIP_VERIFY") == "true" {
		vaultSkipVerify = true
	}
	if vaultURL == "" {
		return nil, fmt.Errorf("VAULT_ADDR is not set")
	}

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: vaultSkipVerify},
	}

	httpClient := &http.Client{Transport: tr}

	// create a vault client
	newClient, err := api.NewClient(&api.Config{Address: vaultURL, HttpClient: httpClient})
	if err != nil {
		return nil, err
	}

	// Set the current token if available
	token := getCurrentToken()
	if token == "" {
		token = os.Getenv("VAULT_TOKEN")
	}
	if token != "" {
		newClient.SetToken(token)
	}

	return newClient, nil
}

// refreshClient forces creation of a new client
func refreshClient() error {
	clientMutex.Lock()
	defer clientMutex.Unlock()

	newClient, err := createVaultClient()
	if err != nil {
		return err
	}

	client = newClient
	return nil
}

func tokenRenewer(client *vault.Client) {
	// Default
	login := loginKube

	if getEnv("VAULT_LOGIN_USER", "") != "" && getEnv("VAULT_LOGIN_PASSWORD", "") != "" {
		login = loginUserPass
	} else if getEnv("VAULT_APP_ROLE", "") != "" && getEnv("VAULT_SECRET_ID", "") != "" {
		login = loginAppRole
	}

	// Initialize exponential backoff
	backoff := utils.NewExponentialBackoff(
		5*time.Second,  // initial backoff
		60*time.Second, // max backoff
		2.0,            // multiplier
		0,              // no max attempts (infinite)
	)

	for {
		vaultLoginResp, err := login(client)
		if err != nil {
			dmetrics.VaultTokenError.WithLabelValues(vaultURL).SetToCurrentTime()
			backoffDuration := backoff.NextBackoff()
			log.Error(err, "unable to authenticate to Vault", "backoff", backoffDuration, "attemptCount", backoff.AttemptCount())
			time.Sleep(backoffDuration)
			continue
		}

		// Update the token and client
		setCurrentToken(vaultLoginResp.Auth.ClientToken)

		err = manageTokenLifecycle(client, vaultLoginResp)
		if err != nil {
			dmetrics.VaultTokenError.WithLabelValues(vaultURL).SetToCurrentTime()
			backoffDuration := backoff.NextBackoff()
			log.Error(err, "unable to start managing token lifecycle", "backoff", backoffDuration, "attemptCount", backoff.AttemptCount())
			// On error, force client refresh on next use
			if err := refreshClient(); err != nil {
				log.Error(err, "Failed to refresh client after token lifecycle error")
			}
			time.Sleep(backoffDuration)
			continue
		}

		// Success - reset backoff
		dmetrics.VaultTokenError.WithLabelValues(vaultURL).Set(0)
		backoff.Reset()

		// Force client refresh after token lifecycle ends
		if err := refreshClient(); err != nil {
			log.Error(err, "Failed to refresh client after token lifecycle completion")
		}

		// Wait a bit before attempting to re-authentication
		log.Info("Token lifecycle ended, waiting before re-authentication", "delay", "5s")
		time.Sleep(5 * time.Second)
	}
}

// Starts token lifecycle management. Returns only fatal errors as errors,
// otherwise returns nil so we can attempt login again.
func manageTokenLifecycle(client *vault.Client, token *vault.Secret) error {
	renew := token.Auth.Renewable
	if !renew {
		log.Info("Token is not configured to be renewable. Re-attempting login.")
		return nil
	}

	watcher, err := client.NewLifetimeWatcher(&vault.LifetimeWatcherInput{
		Secret: token,
	})
	if err != nil {
		return fmt.Errorf("unable to initialize new lifetime watcher for renewing auth token: %w", err)
	}

	go watcher.Start()
	defer watcher.Stop()

	for {
		select {
		case err := <-watcher.DoneCh():
			if err != nil {
				log.Error(err, "Failed to renew token")
				return nil
			}
			// This occurs once the token has reached max TTL.
			log.Info("Token can no longer be renewed. Re-attempting login.")
			return nil

		// Successfully completed renewal
		case renewal := <-watcher.RenewCh():
			log.Info("Successfully renewed vault token")
			setCurrentToken(renewal.Secret.Auth.ClientToken)
		}
	}
}

// executeWithRetry executes a vault operation with retry logic for 403 errors
func executeWithRetry[T any](operation func(*vault.Client) (T, error)) (T, error) {
	var result T
	var err error

	for attempt := 0; attempt < maxRetries; attempt++ {
		c, err := getOrCreateClient()
		if err != nil {
			return result, err
		}

		result, err = operation(c)
		if err == nil {
			return result, nil
		}

		// Check if this is an authentication error
		if strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "permission denied") {
			log.Info("Got 403 error, refreshing client", "attempt", attempt+1)

			// Force client refresh
			if err := refreshClient(); err != nil {
				log.Error(err, "Failed to refresh client")
			}

			// Wait before retry
			if attempt < maxRetries-1 {
				time.Sleep(retryDelay)
			}
			continue
		}

		// Non-auth error, return immediately
		return result, err
	}

	return result, fmt.Errorf("operation failed after %d retries: %w", maxRetries, err)
}

func loginKube(client *vault.Client) (*vault.Secret, error) {
	roleId := getEnv("VAULT_ROLE_ID", "")
	vaultToken := getEnv("VAULT_TOKEN", "")

	if roleId == "" && vaultToken == "" {
		return nil, fmt.Errorf("VAULT_ROLE_ID is not defined")
	}

	kubeAuth, err := vaultKube.NewKubernetesAuth(roleId,
		vaultKube.WithMountPath(getEnv("VAULT_KUBERNETES_MOUNT_POINT", kubernetesMountPath)))
	if err != nil {
		return nil, err
	}
	authInfo, err := client.Auth().Login(context.TODO(), kubeAuth)
	if err != nil {
		return nil, fmt.Errorf("unable to login to kubernetes auth method: %w", err)
	}
	if authInfo == nil {
		return nil, fmt.Errorf("no auth info was returned after login")
	}

	return authInfo, nil
}

func loginUserPass(client *vault.Client) (*vault.Secret, error) {
	loginUser := getEnv("VAULT_LOGIN_USER", "")

	userpassAuth, err := vaultUserpass.NewUserpassAuth(loginUser,
		&vaultUserpass.Password{FromEnv: "VAULT_LOGIN_PASSWORD"},
		vaultUserpass.WithMountPath(getEnv("VAULT_USERPASS_MOUNT_PATH", userpassRoleMountPath)))

	if err != nil {
		return nil, fmt.Errorf("unable to initialize userpass auth method: %w", err)
	}

	authInfo, err := client.Auth().Login(context.TODO(), userpassAuth)
	if err != nil {
		return nil, fmt.Errorf("unable to login to userpass auth method: %w", err)
	}
	if authInfo == nil {
		return nil, fmt.Errorf("no auth info was returned after login")
	}

	return authInfo, nil
}

func loginAppRole(client *vault.Client) (*vault.Secret, error) {
	roleId := getEnv("VAULT_APP_ROLE", "")

	appRoleAuth, err := vaultApprole.NewAppRoleAuth(roleId,
		&vaultApprole.SecretID{FromEnv: "VAULT_SECRET_ID"},
		vaultApprole.WithMountPath(getEnv("VAULT_APPROLE_MOUNT_PATH", approleMountPath)))

	if err != nil {
		return nil, fmt.Errorf("unable to initialize approle auth method: %w", err)
	}

	authInfo, err := client.Auth().Login(context.TODO(), appRoleAuth)
	if err != nil {
		return nil, fmt.Errorf("unable to login to approle auth method: %w", err)
	}
	if authInfo == nil {
		return nil, fmt.Errorf("no auth info was returned after login")
	}

	return authInfo, nil
}

type VaultDbSecret struct {
	LeaseId       string `json:"lease_id"`
	LeaseDuration int    `json:"lease_duration"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	Hosts         string `json:"hosts"`
	ConnectionURL string `json:"connection_url"`
}

func RenewDbCredentials(leaseId string, increment int) error {
	if leaseId == "" {
		return fmt.Errorf("missing lease id")
	}

	_, err := executeWithRetry(func(c *vault.Client) (interface{}, error) {
		return c.Sys().Renew(leaseId, increment)
	})

	return err
}

func IsLeaseValid(leaseId string) bool {
	if leaseId == "" {
		return false
	}

	_, err := executeWithRetry(func(c *vault.Client) (interface{}, error) {
		return c.Sys().Lookup(leaseId)
	})

	return err == nil
}

func RevokeDbCredentials(leaseId string) error {
	if leaseId == "" {
		return fmt.Errorf("missing lease id")
	}

	_, err := executeWithRetry(func(c *vault.Client) (interface{}, error) {
		return nil, c.Sys().Revoke(leaseId)
	})

	return err
}

func GetDbCredentials(role string, mount string) (VaultDbSecret, error) {
	var dbSecret VaultDbSecret

	path := fmt.Sprintf("%s/creds/%s", mount, role)

	s, err := executeWithRetry(func(c *vault.Client) (*vault.Secret, error) {
		return c.Logical().Read(path)
	})

	if err != nil {
		return dbSecret, err
	}

	if s == nil ||
		s.Data["username"] == "" || s.Data["password"] == "" ||
		s.Data["username"] == nil || s.Data["password"] == nil {
		return dbSecret, fmt.Errorf("vault did not return credentials")
	}

	/* Get connection URL or hosts list */
	var connectionURL string
	var hosts string
	var port string
	path = fmt.Sprintf("%s/config/%s", mount, mount)

	cfg, err2 := executeWithRetry(func(c *vault.Client) (*vault.Secret, error) {
		return c.Logical().Read(path)
	})

	if err2 != nil {
		log.Info("Could not get access details for the database", err2)
	} else {
		conn, ok := cfg.Data["connection_details"].(map[string]interface{})
		if !ok {
			return dbSecret, fmt.Errorf("vault did not return the connection details for the database")
		}

		h, ok := conn["hosts"].(string)
		if ok {
			hosts = h
		}
		c, ok := conn["connection_url"].(string)
		if ok {
			connectionURL = c
		}

		n, ok := conn["port"].(json.Number)
		if ok {
			port = n.String()
		}

		if connectionURL != "" {
			connectionURL = strings.Replace(connectionURL, "{{username}}", s.Data["username"].(string), 1)
			connectionURL = strings.Replace(connectionURL, "{{password}}", s.Data["password"].(string), 1)
		}

		if hosts != "" && port != "" {
			rep := fmt.Sprintf(":%s,", port)
			hosts = fmt.Sprintf("%s:%s", strings.Replace(hosts, ",", rep, -1), port)
		}
	}

	dbSecret = VaultDbSecret{
		LeaseId:       s.LeaseID,
		LeaseDuration: s.LeaseDuration,
		Username:      s.Data["username"].(string),
		Password:      s.Data["password"].(string),
		Hosts:         hosts,
		ConnectionURL: connectionURL,
	}

	return dbSecret, nil
}

// Start background process to check vault tokens
func Start() error {
	var err error
	log = ctrl.Log.WithName("vault")

	c, err := getOrCreateClient()
	if err != nil {
		dmetrics.VaultError.WithLabelValues(vaultURL).SetToCurrentTime()
		log.Error(err, "Error setting up vault client")
		return err
	}

	// Handle token-only authentication
	vaultToken := getEnv("VAULT_TOKEN", "")
	if vaultToken != "" {
		// For token-only auth, we should still try to renew if possible
		log.Info("Using token-only authentication, attempting to set up renewal")

		// Set the current token
		setCurrentToken(vaultToken)

		// Try to look up the token to see if it's renewable
		tokenInfo, err := c.Auth().Token().LookupSelf()
		if err != nil {
			log.Error(err, "Failed to lookup token info, renewal will not be available")
			return nil
		}

		// Check if token is renewable
		renewable, ok := tokenInfo.Data["renewable"].(bool)
		if ok && renewable {
			log.Info("Token is renewable, starting renewal process")
			go tokenRenewer(c)
		} else {
			log.Info("Token is not renewable, no automatic renewal will occur")
		}

		return nil
	}

	dmetrics.VaultError.WithLabelValues(vaultURL).Set(0)

	go tokenRenewer(c)

	return nil
}
