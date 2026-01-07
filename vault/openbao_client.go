package vault

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"

	openbaoApprole "github.com/openbao/openbao/api/auth/approle/v2"
	openbaoKube "github.com/openbao/openbao/api/auth/kubernetes/v2"
	openbaoUserpass "github.com/openbao/openbao/api/auth/userpass/v2"
	openbao "github.com/openbao/openbao/api/v2"
)

// OpenBaoClient wraps OpenBao client to implement SecretsClient interface
type OpenBaoClient struct {
	client   *openbao.Client
	backend  BackendType
	address  string
	authMode AuthMode
}

// NewOpenBaoClient creates a new OpenBao client wrapper
func NewOpenBaoClient() (SecretsClient, error) {
	baoAddr := getEnvWithPrefix("BAO", "ADDR", "")
	if baoAddr == "" {
		return nil, fmt.Errorf("BAO_ADDR is not set")
	}

	skipVerify := getEnvWithPrefix("BAO", "SKIP_VERIFY", "false") == "true"

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: skipVerify},
	}

	httpClient := &http.Client{Transport: tr}
	client, err := openbao.NewClient(&openbao.Config{
		Address:    baoAddr,
		HttpClient: httpClient,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create openbao client: %w", err)
	}

	// Set token if available
	token := getEnvWithPrefix("BAO", "TOKEN", "")
	if token != "" {
		client.SetToken(token)
	}

	return &OpenBaoClient{
		client:   client,
		backend:  BackendOpenBao,
		address:  baoAddr,
		authMode: detectAuthMode("BAO"),
	}, nil
}

func (o *OpenBaoClient) Login(ctx context.Context) (*SecretResponse, error) {
	var secret *openbao.Secret
	var err error

	switch o.authMode {
	case AuthModeKubernetes:
		secret, err = o.loginKubernetes(ctx)
	case AuthModeAppRole:
		secret, err = o.loginAppRole(ctx)
	case AuthModeUserPass:
		secret, err = o.loginUserPass(ctx)
	case AuthModeToken:
		// Token auth doesn't require login
		return &SecretResponse{
			Auth: &AuthInfo{
				ClientToken: o.client.Token(),
				Renewable:   false,
			},
		}, nil
	default:
		return nil, fmt.Errorf("no authentication method configured")
	}

	if err != nil {
		return nil, err
	}

	return convertOpenBaoSecret(secret), nil
}

func (o *OpenBaoClient) loginKubernetes(ctx context.Context) (*openbao.Secret, error) {
	roleID := getEnvWithPrefix("BAO", "ROLE_ID", "")
	if roleID == "" {
		return nil, fmt.Errorf("BAO_ROLE_ID is not defined")
	}

	kubeAuth, err := openbaoKube.NewKubernetesAuth(roleID,
		openbaoKube.WithMountPath(getEnvWithPrefix("BAO", "KUBERNETES_MOUNT_POINT", kubernetesMountPath)))
	if err != nil {
		return nil, err
	}

	authInfo, err := o.client.Auth().Login(ctx, kubeAuth)
	if err != nil {
		return nil, fmt.Errorf("unable to login to kubernetes auth method: %w", err)
	}
	if authInfo == nil {
		return nil, fmt.Errorf("no auth info was returned after login")
	}

	return authInfo, nil
}

func (o *OpenBaoClient) loginAppRole(ctx context.Context) (*openbao.Secret, error) {
	roleID := getEnvWithPrefix("BAO", "APP_ROLE", "")

	appRoleAuth, err := openbaoApprole.NewAppRoleAuth(roleID,
		&openbaoApprole.SecretID{FromEnv: "BAO_SECRET_ID"},
		openbaoApprole.WithMountPath(getEnvWithPrefix("BAO", "APPROLE_MOUNT_PATH", approleMountPath)))
	if err != nil {
		return nil, fmt.Errorf("unable to initialize approle auth: %w", err)
	}

	authInfo, err := o.client.Auth().Login(ctx, appRoleAuth)
	if err != nil {
		return nil, fmt.Errorf("unable to login to approle auth method: %w", err)
	}
	if authInfo == nil {
		return nil, fmt.Errorf("no auth info was returned after login")
	}

	return authInfo, nil
}

func (o *OpenBaoClient) loginUserPass(ctx context.Context) (*openbao.Secret, error) {
	loginUser := getEnvWithPrefix("BAO", "LOGIN_USER", "")

	userpassAuth, err := openbaoUserpass.NewUserpassAuth(loginUser,
		&openbaoUserpass.Password{FromEnv: "BAO_LOGIN_PASSWORD"},
		openbaoUserpass.WithMountPath(getEnvWithPrefix("BAO", "USERPASS_MOUNT_PATH", userpassRoleMountPath)))
	if err != nil {
		return nil, fmt.Errorf("unable to initialize userpass auth: %w", err)
	}

	authInfo, err := o.client.Auth().Login(ctx, userpassAuth)
	if err != nil {
		return nil, fmt.Errorf("unable to login to userpass auth method: %w", err)
	}
	if authInfo == nil {
		return nil, fmt.Errorf("no auth info was returned after login")
	}

	return authInfo, nil
}

func (o *OpenBaoClient) SetToken(token string) {
	o.client.SetToken(token)
}

func (o *OpenBaoClient) NewLifetimeWatcher(input *LifetimeWatcherInput) (LifetimeWatcher, error) {
	openbaoSecret := convertToOpenBaoSecret(input.Secret)

	watcher, err := o.client.NewLifetimeWatcher(&openbao.LifetimeWatcherInput{
		Secret: openbaoSecret,
	})
	if err != nil {
		return nil, err
	}

	return &OpenBaoLifetimeWatcher{watcher: watcher}, nil
}

func (o *OpenBaoClient) Read(path string) (*SecretResponse, error) {
	secret, err := o.client.Logical().Read(path)
	if err != nil {
		return nil, err
	}
	return convertOpenBaoSecret(secret), nil
}

func (o *OpenBaoClient) Write(path string, data map[string]interface{}) (*SecretResponse, error) {
	secret, err := o.client.Logical().Write(path, data)
	if err != nil {
		return nil, err
	}
	return convertOpenBaoSecret(secret), nil
}

func (o *OpenBaoClient) Renew(leaseID string, increment int) (*SecretResponse, error) {
	secret, err := o.client.Sys().Renew(leaseID, increment)
	if err != nil {
		return nil, err
	}
	return convertOpenBaoSecret(secret), nil
}

func (o *OpenBaoClient) Revoke(leaseID string) error {
	return o.client.Sys().Revoke(leaseID)
}

func (o *OpenBaoClient) Lookup(leaseID string) (*SecretResponse, error) {
	secret, err := o.client.Sys().Lookup(leaseID)
	if err != nil {
		return nil, err
	}
	return convertOpenBaoSecret(secret), nil
}

func (o *OpenBaoClient) Backend() BackendType {
	return o.backend
}

func (o *OpenBaoClient) Address() string {
	return o.address
}

// OpenBaoLifetimeWatcher wraps openbao.LifetimeWatcher
type OpenBaoLifetimeWatcher struct {
	watcher *openbao.LifetimeWatcher
}

func (w *OpenBaoLifetimeWatcher) Start() {
	go w.watcher.Start()
}

func (w *OpenBaoLifetimeWatcher) Stop() {
	w.watcher.Stop()
}

func (w *OpenBaoLifetimeWatcher) DoneCh() <-chan error {
	return w.watcher.DoneCh()
}

func (w *OpenBaoLifetimeWatcher) RenewCh() <-chan *RenewalInfo {
	ch := make(chan *RenewalInfo)
	go func() {
		for renewal := range w.watcher.RenewCh() {
			ch <- &RenewalInfo{
				Secret: convertOpenBaoSecret(renewal.Secret),
			}
		}
		close(ch)
	}()
	return ch
}

// Helper function to convert OpenBao secret to unified SecretResponse
func convertOpenBaoSecret(s *openbao.Secret) *SecretResponse {
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

// Helper function to convert unified SecretResponse back to OpenBao secret
func convertToOpenBaoSecret(s *SecretResponse) *openbao.Secret {
	if s == nil {
		return nil
	}

	secret := &openbao.Secret{
		LeaseID:       s.LeaseID,
		LeaseDuration: s.LeaseDuration,
		Renewable:     s.Renewable,
		Data:          s.Data,
	}

	if s.Auth != nil {
		secret.Auth = &openbao.SecretAuth{
			ClientToken: s.Auth.ClientToken,
			Renewable:   s.Auth.Renewable,
		}
	}

	return secret
}
