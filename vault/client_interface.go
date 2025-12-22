package vault

import (
	"context"
)

// BackendType represents the secrets management backend in use
type BackendType int

const (
	BackendUnknown BackendType = iota
	BackendVault
	BackendOpenBao
)

func (b BackendType) String() string {
	switch b {
	case BackendVault:
		return "vault"
	case BackendOpenBao:
		return "openbao"
	default:
		return "unknown"
	}
}

// SecretsClient is the unified interface for both Vault and OpenBao clients
type SecretsClient interface {
	// Authentication
	Login(ctx context.Context) (*SecretResponse, error)
	SetToken(token string)

	// Token Lifecycle
	NewLifetimeWatcher(input *LifetimeWatcherInput) (LifetimeWatcher, error)

	// Logical API
	Read(path string) (*SecretResponse, error)
	Write(path string, data map[string]interface{}) (*SecretResponse, error)

	// System API
	Renew(leaseID string, increment int) (*SecretResponse, error)
	Revoke(leaseID string) error
	Lookup(leaseID string) (*SecretResponse, error)

	// Metadata
	Backend() BackendType
	Address() string
}

// SecretResponse is a unified response structure
type SecretResponse struct {
	LeaseID       string
	LeaseDuration int
	Renewable     bool
	Data          map[string]interface{}
	Auth          *AuthInfo
}

// AuthInfo contains authentication information
type AuthInfo struct {
	ClientToken string
	Renewable   bool
}

// LifetimeWatcherInput contains parameters for token renewal
type LifetimeWatcherInput struct {
	Secret *SecretResponse
}

// LifetimeWatcher manages token lifecycle
type LifetimeWatcher interface {
	Start()
	Stop()
	DoneCh() <-chan error
	RenewCh() <-chan *RenewalInfo
}

// RenewalInfo contains information about a successful renewal
type RenewalInfo struct {
	Secret *SecretResponse
}