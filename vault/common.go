package vault

import (
	"fmt"
	"os"

	ctrl "sigs.k8s.io/controller-runtime"
)

// AuthMode represents the authentication method to use
type AuthMode int

const (
	AuthModeUnknown AuthMode = iota
	AuthModeKubernetes
	AuthModeAppRole
	AuthModeUserPass
	AuthModeToken
)

// getEnvWithPrefix gets environment variable with backend-specific prefix
// Falls back to the other backend's variable if not found
func getEnvWithPrefix(prefix, key, fallback string) string {
	// Try primary prefix first
	envKey := fmt.Sprintf("%s_%s", prefix, key)
	if value := os.Getenv(envKey); value != "" {
		return value
	}

	// Fall back to alternate prefix for backwards compatibility
	var altPrefix string
	if prefix == "VAULT" {
		altPrefix = "BAO"
	} else if prefix == "BAO" {
		altPrefix = "VAULT"
	} else {
		// For any other prefix, no fallback
		return fallback
	}

	altKey := fmt.Sprintf("%s_%s", altPrefix, key)
	if value := os.Getenv(altKey); value != "" {
		return value
	}

	return fallback
}

// detectAuthMode determines which authentication method to use
func detectAuthMode(prefix string) AuthMode {
	// Check for token (simplest)
	if getEnvWithPrefix(prefix, "TOKEN", "") != "" {
		return AuthModeToken
	}

	// Check for UserPass
	if getEnvWithPrefix(prefix, "LOGIN_USER", "") != "" &&
		getEnvWithPrefix(prefix, "LOGIN_PASSWORD", "") != "" {
		return AuthModeUserPass
	}

	// Check for AppRole
	if getEnvWithPrefix(prefix, "APP_ROLE", "") != "" &&
		getEnvWithPrefix(prefix, "SECRET_ID", "") != "" {
		return AuthModeAppRole
	}

	// Default to Kubernetes
	return AuthModeKubernetes
}

// NewSecretsClient creates the appropriate client based on environment configuration
func NewSecretsClient() (SecretsClient, error) {
	backend, err := detectBackend()
	if err != nil {
		return nil, err
	}

	switch backend {
	case BackendOpenBao:
		ctrl.Log.WithName("secrets-backend").Info("Using backend OpenBao")
		return NewOpenBaoClient()
	case BackendVault:
		return NewVaultClient()
	default:
		return nil, fmt.Errorf("unknown backend type: %v", backend)
	}
}

func detectBackend() (BackendType, error) {
	baoAddr := os.Getenv("BAO_ADDR")
	vaultAddr := os.Getenv("VAULT_ADDR")

	// Handle both being set
	if baoAddr != "" && vaultAddr != "" {
		log := ctrl.Log.WithName("secrets-backend")
		log.Info("WARNING: Both BAO_ADDR and VAULT_ADDR are set. Using OpenBao (BAO_ADDR takes precedence)")
		return BackendOpenBao, nil
	}

	// OpenBao takes precedence
	if baoAddr != "" {
		return BackendOpenBao, nil
	}

	if vaultAddr != "" {
		return BackendVault, nil
	}

	return BackendUnknown, fmt.Errorf("no secrets backend configured: set either BAO_ADDR or VAULT_ADDR")
}
