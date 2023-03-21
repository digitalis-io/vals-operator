package vault

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

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
)

var log logr.Logger
var vaultURL string = getEnv("VAULT_ADDR", "http://vault:8200")
var client *vault.Client

func getEnv(key string, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func vaultClient() (*api.Client, error) {
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
	return api.NewClient(&api.Config{Address: vaultURL, HttpClient: httpClient})
}

func tokenRenewer(client *vault.Client) {
	// Default
	login := loginKube

	if getEnv("VAULT_LOGIN_USER", "") != "" && getEnv("VAULT_LOGIN_PASSWORD", "") != "" {
		login = loginUserPass
	} else if getEnv("VAULT_APP_ROLE", "") != "" && getEnv("VAULT_SECRET_ID", "") != "" {
		login = loginAppRole
	}

	for {
		vaultLoginResp, err := login(client)
		if err != nil {
			log.Error(err, "unable to authenticate to Vault")
			return
		}
		err = os.Setenv("VAULT_TOKEN", vaultLoginResp.Auth.ClientToken)
		if err != nil {
			log.Error(err, "Cannot set VAULT_TOKEN env variable")
			return
		}

		tokenErr := manageTokenLifecycle(client, vaultLoginResp)
		if tokenErr != nil {
			log.Error(err, "unable to start managing token lifecycle")
			return
		}
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
			err = os.Setenv("VAULT_TOKEN", renewal.Secret.Auth.ClientToken)
			if err != nil {
				return err
			}
		}
	}
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
	ConnectionURL string `json:"hosts"`
}

func RenewDbCredentials(leaseId string, increment int) (err error) {
	if client == nil {
		client, err = vaultClient()
		if err != nil {
			return err
		}
	}

	if leaseId == "" {
		return fmt.Errorf("missing lease id")
	}

	_, err = client.Sys().Renew(leaseId, increment)

	return err
}

func IsLeaseValid(leaseId string) bool {
	var err error
	if client == nil {
		client, err = vaultClient()
		if err != nil {
			return false
		}
	}
	if leaseId == "" {
		return false
	}
	_, err = client.Sys().Lookup(leaseId)
	if err != nil {
		return false
	}
	return true
}

func RevokeDbCredentials(leaseId string) (err error) {
	if client == nil {
		client, err = vaultClient()
		if err != nil {
			return err
		}
	}
	if leaseId == "" {
		return fmt.Errorf("missing lease id")
	}
	err = client.Sys().Revoke(leaseId)
	return err
}

func GetDbCredentials(role string, mount string) (VaultDbSecret, error) {
	var dbSecret VaultDbSecret
	var err error
	if client == nil {
		client, err = vaultClient()
		if err != nil {
			return dbSecret, err
		}
	}
	path := fmt.Sprintf("%s/creds/%s", mount, role)
	s, err := client.Logical().Read(path)
	if err != nil {
		return dbSecret, err
	}
	if s == nil ||
		s.Data["username"] == "" || s.Data["password"] == "" ||
		s.Data["username"] == nil || s.Data["password"] == nil {
		return dbSecret, fmt.Errorf("vault did not return credentials: %v", err)
	}
	/* Get connection URL or hosts list */
	var connectionURL string
	var hosts string
	var port string
	path = fmt.Sprintf("%s/config/%s", mount, mount)
	cfg, err2 := client.Logical().Read(path)
	if err2 != nil {
		return dbSecret, fmt.Errorf("vault did not return the connection details for the database: %v", err2)
	}
	conn, ok := cfg.Data["connection_details"].(map[string]interface{})
	if !ok {
		return dbSecret, fmt.Errorf("vault did not return the connection details for the database")
	}

	hosts, _ = conn["hosts"].(string)
	connectionURL, _ = conn["connection_url"].(string)
	port = conn["port"].(json.Number).String()

	if connectionURL != "" {
		connectionURL = strings.Replace(connectionURL, "{{username}}", s.Data["username"].(string), 1)
		connectionURL = strings.Replace(connectionURL, "{{password}}", s.Data["password"].(string), 1)
	}

	if hosts != "" && port != "" {
		rep := fmt.Sprintf(":%s,", port)
		hosts = fmt.Sprintf("%s:%s", strings.Replace(hosts, ",", rep, -1), port)
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

	client, err = vaultClient()
	if err != nil {
		log.Error(err, "Error setting up vault client")
		return err
	}

	// FIXME: we don't currently handle renewals when auth is token only
	vaultToken := getEnv("VAULT_TOKEN", "")
	if vaultToken != "" {
		return nil
	}

	go tokenRenewer(client)

	return nil
}
