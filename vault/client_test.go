package vault

import (
	"os"
	"testing"
)

func TestBackendDetection(t *testing.T) {
	tests := []struct {
		name          string
		baoAddr       string
		vaultAddr     string
		expectedType  BackendType
		expectedError bool
	}{
		{
			name:          "OpenBao takes precedence when both set",
			baoAddr:       "http://openbao:8200",
			vaultAddr:     "http://vault:8200",
			expectedType:  BackendOpenBao,
			expectedError: false,
		},
		{
			name:          "Vault when OpenBao not set",
			baoAddr:       "",
			vaultAddr:     "http://vault:8200",
			expectedType:  BackendVault,
			expectedError: false,
		},
		{
			name:          "Error when neither set",
			baoAddr:       "",
			vaultAddr:     "",
			expectedType:  BackendUnknown,
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			os.Setenv("BAO_ADDR", tt.baoAddr)
			os.Setenv("VAULT_ADDR", tt.vaultAddr)
			defer os.Unsetenv("BAO_ADDR")
			defer os.Unsetenv("VAULT_ADDR")

			// Test
			backend, err := detectBackend()

			// Verify
			if tt.expectedError && err == nil {
				t.Error("Expected error but got nil")
			}
			if !tt.expectedError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
			if backend != tt.expectedType {
				t.Errorf("Expected %v but got %v", tt.expectedType, backend)
			}
		})
	}
}

func TestEnvVarFallback(t *testing.T) {
	tests := []struct {
		name          string
		prefix        string
		key           string
		primaryValue  string
		fallbackValue string
		defaultValue  string
		expected      string
	}{
		{
			name:          "Primary value used",
			prefix:        "BAO",
			key:           "ADDR",
			primaryValue:  "http://openbao:8200",
			fallbackValue: "http://vault:8200",
			defaultValue:  "http://default:8200",
			expected:      "http://openbao:8200",
		},
		{
			name:          "Fallback value used",
			prefix:        "BAO",
			key:           "ADDR",
			primaryValue:  "",
			fallbackValue: "http://vault:8200",
			defaultValue:  "http://default:8200",
			expected:      "http://vault:8200",
		},
		{
			name:          "Default value used",
			prefix:        "BAO",
			key:           "ADDR",
			primaryValue:  "",
			fallbackValue: "",
			defaultValue:  "http://default:8200",
			expected:      "http://default:8200",
		},
		{
			name:          "Vault prefix with OpenBao fallback",
			prefix:        "VAULT",
			key:           "ROLE_ID",
			primaryValue:  "vault-role",
			fallbackValue: "openbao-role",
			defaultValue:  "default-role",
			expected:      "vault-role",
		},
		{
			name:          "Vault prefix falls back to OpenBao",
			prefix:        "VAULT",
			key:           "ROLE_ID",
			primaryValue:  "",
			fallbackValue: "openbao-role",
			defaultValue:  "default-role",
			expected:      "openbao-role",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			primaryKey := tt.prefix + "_" + tt.key
			var fallbackKey string
			if tt.prefix == "BAO" {
				fallbackKey = "VAULT_" + tt.key
			} else {
				fallbackKey = "BAO_" + tt.key
			}

			os.Setenv(primaryKey, tt.primaryValue)
			os.Setenv(fallbackKey, tt.fallbackValue)
			defer os.Unsetenv(primaryKey)
			defer os.Unsetenv(fallbackKey)

			// Test
			result := getEnvWithPrefix(tt.prefix, tt.key, tt.defaultValue)

			// Verify
			if result != tt.expected {
				t.Errorf("Expected %s but got %s", tt.expected, result)
			}
		})
	}
}

func TestAuthModeDetection(t *testing.T) {
	tests := []struct {
		name         string
		prefix       string
		envVars      map[string]string
		expectedMode AuthMode
	}{
		{
			name:   "Token auth detected for OpenBao",
			prefix: "BAO",
			envVars: map[string]string{
				"BAO_TOKEN": "s.abc123",
			},
			expectedMode: AuthModeToken,
		},
		{
			name:   "Kubernetes auth detected for Vault",
			prefix: "VAULT",
			envVars: map[string]string{
				"VAULT_ROLE_ID": "my-role",
			},
			expectedMode: AuthModeKubernetes,
		},
		{
			name:   "AppRole auth detected for OpenBao",
			prefix: "BAO",
			envVars: map[string]string{
				"BAO_APP_ROLE":  "my-app-role",
				"BAO_SECRET_ID": "secret-id",
			},
			expectedMode: AuthModeAppRole,
		},
		{
			name:   "UserPass auth detected for Vault",
			prefix: "VAULT",
			envVars: map[string]string{
				"VAULT_LOGIN_USER":     "user",
				"VAULT_LOGIN_PASSWORD": "pass",
			},
			expectedMode: AuthModeUserPass,
		},
		{
			name:   "Token takes precedence over others",
			prefix: "BAO",
			envVars: map[string]string{
				"BAO_TOKEN":          "s.abc123",
				"BAO_APP_ROLE":       "my-app-role",
				"BAO_SECRET_ID":      "secret-id",
				"BAO_LOGIN_USER":     "user",
				"BAO_LOGIN_PASSWORD": "pass",
			},
			expectedMode: AuthModeToken,
		},
		{
			name:         "Default to Kubernetes when nothing set",
			prefix:       "VAULT",
			envVars:      map[string]string{},
			expectedMode: AuthModeKubernetes,
		},
		{
			name:   "Cross-backend fallback for auth",
			prefix: "BAO",
			envVars: map[string]string{
				"VAULT_APP_ROLE":  "my-app-role",
				"VAULT_SECRET_ID": "secret-id",
			},
			expectedMode: AuthModeAppRole,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup - clear all possible env vars first
			envKeys := []string{
				"BAO_TOKEN", "VAULT_TOKEN",
				"BAO_ROLE_ID", "VAULT_ROLE_ID",
				"BAO_APP_ROLE", "VAULT_APP_ROLE",
				"BAO_SECRET_ID", "VAULT_SECRET_ID",
				"BAO_LOGIN_USER", "VAULT_LOGIN_USER",
				"BAO_LOGIN_PASSWORD", "VAULT_LOGIN_PASSWORD",
			}
			for _, key := range envKeys {
				os.Unsetenv(key)
			}

			// Set test env vars
			for key, value := range tt.envVars {
				os.Setenv(key, value)
				defer os.Unsetenv(key)
			}

			// Test
			mode := detectAuthMode(tt.prefix)

			// Verify
			if mode != tt.expectedMode {
				t.Errorf("Expected %v but got %v", tt.expectedMode, mode)
			}
		})
	}
}

func TestBackendTypeString(t *testing.T) {
	tests := []struct {
		backend  BackendType
		expected string
	}{
		{BackendVault, "vault"},
		{BackendOpenBao, "openbao"},
		{BackendUnknown, "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := tt.backend.String()
			if result != tt.expected {
				t.Errorf("Expected %s but got %s", tt.expected, result)
			}
		})
	}
}