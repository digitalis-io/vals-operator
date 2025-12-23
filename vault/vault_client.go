package vault

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"

	"github.com/hashicorp/vault/api"
	vaultApprole "github.com/hashicorp/vault/api/auth/approle"
	vaultKube "github.com/hashicorp/vault/api/auth/kubernetes"
	vaultUserpass "github.com/hashicorp/vault/api/auth/userpass"
)

// VaultClient wraps HashiCorp Vault client to implement SecretsClient interface
type VaultClient struct {
	client   *api.Client
	backend  BackendType
	address  string
	authMode AuthMode
}

// NewVaultClient creates a new Vault client wrapper
func NewVaultClient() (SecretsClient, error) {
	vaultAddr := getEnvWithPrefix("VAULT", "ADDR", "")
	if vaultAddr == "" {
		return nil, fmt.Errorf("VAULT_ADDR is not set")
	}

	skipVerify := getEnvWithPrefix("VAULT", "SKIP_VERIFY", "false") == "true"

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: skipVerify},
	}

	httpClient := &http.Client{Transport: tr}
	client, err := api.NewClient(&api.Config{
		Address:    vaultAddr,
		HttpClient: httpClient,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create vault client: %w", err)
	}

	// Set token if available
	token := getEnvWithPrefix("VAULT", "TOKEN", "")
	if token != "" {
		client.SetToken(token)
	}

	return &VaultClient{
		client:   client,
		backend:  BackendVault,
		address:  vaultAddr,
		authMode: detectAuthMode("VAULT"),
	}, nil
}

func (v *VaultClient) Login(ctx context.Context) (*SecretResponse, error) {
	var secret *api.Secret
	var err error

	switch v.authMode {
	case AuthModeKubernetes:
		secret, err = v.loginKubernetes(ctx)
	case AuthModeAppRole:
		secret, err = v.loginAppRole(ctx)
	case AuthModeUserPass:
		secret, err = v.loginUserPass(ctx)
	case AuthModeToken:
		// Token auth doesn't require login
		return &SecretResponse{
			Auth: &AuthInfo{
				ClientToken: v.client.Token(),
				Renewable:   false,
			},
		}, nil
	default:
		return nil, fmt.Errorf("no authentication method configured")
	}

	if err != nil {
		return nil, err
	}

	return convertVaultSecret(secret), nil
}

func (v *VaultClient) loginKubernetes(ctx context.Context) (*api.Secret, error) {
	roleID := getEnvWithPrefix("VAULT", "ROLE_ID", "")
	if roleID == "" {
		return nil, fmt.Errorf("VAULT_ROLE_ID is not defined")
	}

	kubeAuth, err := vaultKube.NewKubernetesAuth(roleID,
		vaultKube.WithMountPath(getEnvWithPrefix("VAULT", "KUBERNETES_MOUNT_POINT", kubernetesMountPath)))
	if err != nil {
		return nil, err
	}

	authInfo, err := v.client.Auth().Login(ctx, kubeAuth)
	if err != nil {
		return nil, fmt.Errorf("unable to login to kubernetes auth method: %w", err)
	}
	if authInfo == nil {
		return nil, fmt.Errorf("no auth info was returned after login")
	}

	return authInfo, nil
}

func (v *VaultClient) loginAppRole(ctx context.Context) (*api.Secret, error) {
	roleID := getEnvWithPrefix("VAULT", "APP_ROLE", "")

	appRoleAuth, err := vaultApprole.NewAppRoleAuth(roleID,
		&vaultApprole.SecretID{FromEnv: "VAULT_SECRET_ID"},
		vaultApprole.WithMountPath(getEnvWithPrefix("VAULT", "APPROLE_MOUNT_PATH", approleMountPath)))
	if err != nil {
		return nil, fmt.Errorf("unable to initialize approle auth: %w", err)
	}

	authInfo, err := v.client.Auth().Login(ctx, appRoleAuth)
	if err != nil {
		return nil, fmt.Errorf("unable to login to approle auth method: %w", err)
	}
	if authInfo == nil {
		return nil, fmt.Errorf("no auth info was returned after login")
	}

	return authInfo, nil
}

func (v *VaultClient) loginUserPass(ctx context.Context) (*api.Secret, error) {
	loginUser := getEnvWithPrefix("VAULT", "LOGIN_USER", "")

	userpassAuth, err := vaultUserpass.NewUserpassAuth(loginUser,
		&vaultUserpass.Password{FromEnv: "VAULT_LOGIN_PASSWORD"},
		vaultUserpass.WithMountPath(getEnvWithPrefix("VAULT", "USERPASS_MOUNT_PATH", userpassRoleMountPath)))
	if err != nil {
		return nil, fmt.Errorf("unable to initialize userpass auth: %w", err)
	}

	authInfo, err := v.client.Auth().Login(ctx, userpassAuth)
	if err != nil {
		return nil, fmt.Errorf("unable to login to userpass auth method: %w", err)
	}
	if authInfo == nil {
		return nil, fmt.Errorf("no auth info was returned after login")
	}

	return authInfo, nil
}

func (v *VaultClient) SetToken(token string) {
	v.client.SetToken(token)
}

func (v *VaultClient) NewLifetimeWatcher(input *LifetimeWatcherInput) (LifetimeWatcher, error) {
	vaultSecret := convertToVaultSecret(input.Secret)

	watcher, err := v.client.NewLifetimeWatcher(&api.LifetimeWatcherInput{
		Secret: vaultSecret,
	})
	if err != nil {
		return nil, err
	}

	return &VaultLifetimeWatcher{watcher: watcher}, nil
}

func (v *VaultClient) Read(path string) (*SecretResponse, error) {
	secret, err := v.client.Logical().Read(path)
	if err != nil {
		return nil, err
	}
	return convertVaultSecret(secret), nil
}

func (v *VaultClient) Write(path string, data map[string]interface{}) (*SecretResponse, error) {
	secret, err := v.client.Logical().Write(path, data)
	if err != nil {
		return nil, err
	}
	return convertVaultSecret(secret), nil
}

func (v *VaultClient) Renew(leaseID string, increment int) (*SecretResponse, error) {
	secret, err := v.client.Sys().Renew(leaseID, increment)
	if err != nil {
		return nil, err
	}
	return convertVaultSecret(secret), nil
}

func (v *VaultClient) Revoke(leaseID string) error {
	return v.client.Sys().Revoke(leaseID)
}

func (v *VaultClient) Lookup(leaseID string) (*SecretResponse, error) {
	secret, err := v.client.Sys().Lookup(leaseID)
	if err != nil {
		return nil, err
	}
	return convertVaultSecret(secret), nil
}

func (v *VaultClient) Backend() BackendType {
	return v.backend
}

func (v *VaultClient) Address() string {
	return v.address
}

// VaultLifetimeWatcher wraps api.LifetimeWatcher
type VaultLifetimeWatcher struct {
	watcher *api.LifetimeWatcher
}

func (w *VaultLifetimeWatcher) Start() {
	go w.watcher.Start()
}

func (w *VaultLifetimeWatcher) Stop() {
	w.watcher.Stop()
}

func (w *VaultLifetimeWatcher) DoneCh() <-chan error {
	return w.watcher.DoneCh()
}

func (w *VaultLifetimeWatcher) RenewCh() <-chan *RenewalInfo {
	ch := make(chan *RenewalInfo)
	go func() {
		for renewal := range w.watcher.RenewCh() {
			ch <- &RenewalInfo{
				Secret: convertVaultSecret(renewal.Secret),
			}
		}
		close(ch)
	}()
	return ch
}

// Helper function to convert Vault secret to unified SecretResponse
func convertVaultSecret(s *api.Secret) *SecretResponse {
	if s == nil {
		return nil
	}

	resp := &SecretResponse{
		LeaseID:       s.LeaseID,
		LeaseDuration: s.LeaseDuration,
		Renewable:     s.Renewable,
		Data:          s.Data,
	}

	if s.Auth != nil {
		resp.Auth = &AuthInfo{
			ClientToken: s.Auth.ClientToken,
			Renewable:   s.Auth.Renewable,
		}
	}

	return resp
}

// Helper function to convert unified SecretResponse back to Vault secret
func convertToVaultSecret(s *SecretResponse) *api.Secret {
	if s == nil {
		return nil
	}

	secret := &api.Secret{
		LeaseID:       s.LeaseID,
		LeaseDuration: s.LeaseDuration,
		Renewable:     s.Renewable,
		Data:          s.Data,
	}

	if s.Auth != nil {
		secret.Auth = &api.SecretAuth{
			ClientToken: s.Auth.ClientToken,
			Renewable:   s.Auth.Renewable,
		}
	}

	return secret
}
