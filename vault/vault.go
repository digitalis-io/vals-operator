package vault

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	dmetrics "digitalis.io/vals-operator/metrics"
	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	kubernetesMountPath   = "kubernetes"
	approleMountPath      = "approle"
	userpassRoleMountPath = "userpass"
)

var log logr.Logger
var client SecretsClient
var backendType BackendType

// VaultDbSecret represents database credentials from Vault/OpenBao
type VaultDbSecret struct {
	LeaseId       string `json:"lease_id"`
	LeaseDuration int    `json:"lease_duration"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	Hosts         string `json:"hosts"`
	ConnectionURL string `json:"connection_url"`
}

func setTokenEnvVars(c SecretsClient, token string) error {
	tokenEnvVar := fmt.Sprintf("%s_TOKEN", strings.ToUpper(c.Backend().String()))
	if err := os.Setenv(tokenEnvVar, token); err != nil {
		return err
	}
	if c.Backend() == BackendOpenBao {
		if err := os.Setenv("VAULT_TOKEN", token); err != nil {
			return err
		}
	}
	return nil
}

// tokenRenewer manages the token lifecycle. Pass the initial login response from
// Start() to avoid a redundant second login on startup.
func tokenRenewer(c SecretsClient, initial *SecretResponse) {
	loginResp := initial
	for {
		if loginResp == nil {
			var err error
			loginResp, err = c.Login(context.TODO())
			if err != nil {
				dmetrics.VaultTokenError.WithLabelValues(c.Address()).SetToCurrentTime()
				log.Error(err, "unable to authenticate", "backend", c.Backend())
				return
			}
		}

		if err := setTokenEnvVars(c, loginResp.Auth.ClientToken); err != nil {
			dmetrics.VaultTokenError.WithLabelValues(c.Address()).SetToCurrentTime()
			log.Error(err, "Cannot set token env variable", "backend", c.Backend())
			return
		}

		c.SetToken(loginResp.Auth.ClientToken)

		tokenErr := manageTokenLifecycle(c, loginResp)
		if tokenErr != nil {
			dmetrics.VaultTokenError.WithLabelValues(c.Address()).SetToCurrentTime()
			log.Error(tokenErr, "unable to start managing token lifecycle")
			return
		}

		dmetrics.VaultTokenError.WithLabelValues(c.Address()).Set(0)
		time.Sleep(60 * time.Second)
		loginResp = nil
	}
}

// Starts token lifecycle management. Returns only fatal errors as errors,
// otherwise returns nil so we can attempt login again.
func manageTokenLifecycle(c SecretsClient, token *SecretResponse) error {
	renew := token.Auth.Renewable
	if !renew {
		log.Info("Token is not configured to be renewable. Re-attempting login.")
		return nil
	}

	watcher, err := c.NewLifetimeWatcher(&LifetimeWatcherInput{
		Secret: token,
	})
	if err != nil {
		return fmt.Errorf("unable to initialize new lifetime watcher for renewing auth token: %w", err)
	}

	watcher.Start()
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
			log.Info("Successfully renewed token", "backend", c.Backend())
			if err = setTokenEnvVars(c, renewal.Secret.Auth.ClientToken); err != nil {
				return err
			}
		}
	}
}

func RenewDbCredentials(leaseId string, increment int) error {
	if client == nil {
		var err error
		client, err = NewSecretsClient()
		if err != nil {
			return err
		}
	}

	if leaseId == "" {
		return fmt.Errorf("missing lease id")
	}

	_, err := client.Renew(leaseId, increment)
	return err
}

func IsLeaseValid(leaseId string) bool {
	if client == nil {
		var err error
		client, err = NewSecretsClient()
		if err != nil {
			return false
		}
	}

	if leaseId == "" {
		return false
	}

	_, err := client.Lookup(leaseId)
	return err == nil
}

func RevokeDbCredentials(leaseId string) error {
	if client == nil {
		var err error
		client, err = NewSecretsClient()
		if err != nil {
			return err
		}
	}

	if leaseId == "" {
		return fmt.Errorf("missing lease id")
	}

	return client.Revoke(leaseId)
}

func GetDbCredentials(role string, mount string) (VaultDbSecret, error) {
	var dbSecret VaultDbSecret
	var err error

	if client == nil {
		client, err = NewSecretsClient()
		if err != nil {
			return dbSecret, err
		}
	}

	path := fmt.Sprintf("%s/creds/%s", mount, role)
	s, err := client.Read(path)
	if err != nil {
		return dbSecret, err
	}

	if s == nil ||
		s.Data["username"] == "" || s.Data["password"] == "" ||
		s.Data["username"] == nil || s.Data["password"] == nil {
		return dbSecret, fmt.Errorf("backend did not return credentials: %v", err)
	}

	// Get connection URL or hosts list
	var connectionURL string
	var hosts string
	var port string

	path = fmt.Sprintf("%s/config/%s", mount, mount)
	cfg, err2 := client.Read(path)
	if err2 != nil {
		log.Info("Could not get access details for the database", "error", err2)
	} else if cfg != nil && cfg.Data != nil {
		conn, ok := cfg.Data["connection_details"].(map[string]interface{})
		if !ok {
			return dbSecret, fmt.Errorf("backend did not return the connection details for the database")
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
	log = ctrl.Log.WithName("secrets-backend")

	client, err = NewSecretsClient()
	if err != nil {
		dmetrics.VaultError.WithLabelValues("unknown").SetToCurrentTime()
		log.Error(err, "Error setting up secrets client")
		return err
	}

	backendType = client.Backend()
	log.Info("Using secrets backend", "backend", backendType, "address", client.Address())

	// Workaround: Set VAULT_ variables for vals library when using OpenBao
	// The vals library doesn't have native OpenBao support, so it requires VAULT_ variables
	if backendType == BackendOpenBao {
		// If BAO_ADDR is set but VAULT_ADDR is not, copy BAO_ADDR to VAULT_ADDR for vals compatibility
		if os.Getenv("BAO_ADDR") != "" && os.Getenv("VAULT_ADDR") == "" {
			log.Info("Setting VAULT_ADDR for vals library compatibility", "address", os.Getenv("BAO_ADDR"))
			os.Setenv("VAULT_ADDR", os.Getenv("BAO_ADDR"))
		}
		// Copy BAO_TOKEN to VAULT_TOKEN if needed (will be set later after login if not already set)
		if os.Getenv("BAO_TOKEN") != "" && os.Getenv("VAULT_TOKEN") == "" {
			log.Info("Setting VAULT_TOKEN for vals library compatibility")
			os.Setenv("VAULT_TOKEN", os.Getenv("BAO_TOKEN"))
		}
	}

	// Check if using token-only auth
	tokenEnvVar := fmt.Sprintf("%s_TOKEN", strings.ToUpper(backendType.String()))
	if os.Getenv(tokenEnvVar) != "" && detectAuthMode(strings.ToUpper(backendType.String())) == AuthModeToken {
		log.Info("Using token-only authentication, skipping token renewal")
		client.SetToken(os.Getenv(tokenEnvVar))
		return nil
	}

	dmetrics.VaultError.WithLabelValues(client.Address()).Set(0)

	// Perform initial login synchronously so VAULT_TOKEN is set before the
	// reconciler starts processing ValsSecret resources.
	loginResp, err := client.Login(context.TODO())
	if err != nil {
		dmetrics.VaultError.WithLabelValues(client.Address()).SetToCurrentTime()
		log.Error(err, "Initial login to secrets backend failed")
		return err
	}
	if err = setTokenEnvVars(client, loginResp.Auth.ClientToken); err != nil {
		return err
	}
	client.SetToken(loginResp.Auth.ClientToken)

	go tokenRenewer(client, loginResp)

	return nil
}
