//go:build integratio
// +build integratio

package vault

import (
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/vault/api"
)

// Integration tests that run against a real Vault server
//
// Prerequisites:
// 1. Start Vault in dev mode: vault server -dev -dev-root-token-id=root-token
// 2. Export VAULT_ADDR=http://127.0.0.1:8200
// 3. Run tests: go test -tags=integration ./vault -v
//
// Or use the setup script:
// ./scripts/setup-vault-integration-test.sh

func skipIfNoVault(t *testing.T) {
	// Check if VAULT_INTEGRATION_TEST is set
	if os.Getenv("VAULT_INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test. Set VAULT_INTEGRATION_TEST=true and ensure Vault is running")
	}

	// Check if we can connect to Vault
	vaultAddr := os.Getenv("VAULT_ADDR")
	if vaultAddr == "" {
		vaultAddr = "http://127.0.0.1:8200"
	}

	client, err := api.NewClient(&api.Config{
		Address: vaultAddr,
	})
	if err != nil {
		t.Skipf("Cannot create Vault client: %v", err)
	}

	// Try to check Vault health
	_, err = client.Sys().Health()
	if err != nil {
		t.Skipf("Cannot connect to Vault at %s: %v", vaultAddr, err)
	}
}

func setupVaultForTesting(t *testing.T) {
	rootToken := os.Getenv("VAULT_ROOT_TOKEN")
	if rootToken == "" {
		rootToken = "root-token"
	}

	vaultAddr := os.Getenv("VAULT_ADDR")
	if vaultAddr == "" {
		vaultAddr = "http://127.0.0.1:8200"
	}

	client, err := api.NewClient(&api.Config{
		Address: vaultAddr,
	})
	if err != nil {
		t.Fatalf("Failed to create Vault client: %v", err)
	}

	client.SetToken(rootToken)

	// Create test policy
	policy := `
path "database/*" {
  capabilities = ["read", "list"]
}

path "auth/token/lookup-self" {
  capabilities = ["read"]
}

path "auth/token/renew-self" {
  capabilities = ["update"]
}

path "sys/leases/renew" {
  capabilities = ["update"]
}

path "sys/leases/lookup" {
  capabilities = ["update"]
}

path "sys/leases/revoke" {
  capabilities = ["update"]
}
`
	if err := client.Sys().PutPolicy("test-policy", policy); err != nil {
		t.Logf("Warning: Failed to create policy (may already exist): %v", err)
	}

	// Enable userpass auth if not already enabled
	auths, err := client.Sys().ListAuth()
	if err != nil {
		t.Fatalf("Failed to list auth methods: %v", err)
	}

	if _, ok := auths["userpass/"]; !ok {
		if err := client.Sys().EnableAuthWithOptions("userpass", &api.EnableAuthOptions{
			Type: "userpass",
		}); err != nil {
			t.Fatalf("Failed to enable userpass auth: %v", err)
		}
	}

	// Create or update test user with short TTL for testing renewal
	_, err = client.Logical().Write("auth/userpass/users/testuser", map[string]interface{}{
		"password": "testpass",
		"policies": "test-policy",
		"ttl":      "30s", // Short TTL for testing renewal
		"max_ttl":  "5m",  // Max TTL of 5 minutes
	})
	if err != nil {
		t.Fatalf("Failed to create test user: %v", err)
	}

	// Enable approle auth if not already enabled
	if _, ok := auths["approle/"]; !ok {
		if err := client.Sys().EnableAuthWithOptions("approle", &api.EnableAuthOptions{
			Type: "approle",
		}); err != nil {
			t.Fatalf("Failed to enable approle auth: %v", err)
		}
	}

	// Create test approle
	_, err = client.Logical().Write("auth/approle/role/test-role", map[string]interface{}{
		"policies":      "test-policy",
		"token_ttl":     "30s",
		"token_max_ttl": "5m",
	})
	if err != nil {
		t.Fatalf("Failed to create approle: %v", err)
	}

	// Get role ID
	roleIDResp, err := client.Logical().Read("auth/approle/role/test-role/role-id")
	if err != nil {
		t.Fatalf("Failed to read role ID: %v", err)
	}

	// Create secret ID
	secretIDResp, err := client.Logical().Write("auth/approle/role/test-role/secret-id", nil)
	if err != nil {
		t.Fatalf("Failed to create secret ID: %v", err)
	}

	// Set environment variables for tests
	os.Setenv("TEST_ROLE_ID", roleIDResp.Data["role_id"].(string))
	os.Setenv("TEST_SECRET_ID", secretIDResp.Data["secret_id"].(string))

	t.Logf("Vault test environment configured successfully")
}

func TestIntegrationUserpassAuth(t *testing.T) {
	skipIfNoVault(t)
	setupVaultForTesting(t)

	// Save and restore environment
	originalURL := vaultURL
	originalClient := client
	originalToken := currentToken
	defer func() {
		vaultURL = originalURL
		client = originalClient
		currentToken = originalToken
		os.Unsetenv("VAULT_LOGIN_USER")
		os.Unsetenv("VAULT_LOGIN_PASSWORD")
	}()

	// Configure test environment
	vaultURL = os.Getenv("VAULT_ADDR")
	if vaultURL == "" {
		vaultURL = "http://127.0.0.1:8200"
	}
	client = nil
	currentToken = ""

	os.Setenv("VAULT_LOGIN_USER", "testuser")
	os.Setenv("VAULT_LOGIN_PASSWORD", "testpass")

	// Test initial authentication
	err := Start()
	if err != nil {
		t.Fatalf("Failed to start: %v", err)
	}

	// Verify we have a token
	token := getCurrentToken()
	if token == "" {
		t.Fatal("No token after authentication")
	}
	t.Logf("Successfully authenticated, token: %s...", token[:8])

	// Test making Vault calls
	valid := IsLeaseValid("non-existent-lease")
	if valid {
		t.Error("Non-existent lease should not be valid")
	}

	// Wait for token renewal (tokens have 30s TTL)
	t.Log("Waiting 35 seconds for token renewal...")
	time.Sleep(35 * time.Second)

	// Check if token is still valid
	newToken := getCurrentToken()
	if newToken == "" {
		t.Fatal("Token became empty after renewal period")
	}

	// Token might be the same if renewed, or different if re-authenticated
	t.Logf("Token after renewal period: %s...", newToken[:8])

	// Verify we can still make API calls
	valid = IsLeaseValid("another-non-existent-lease")
	if valid {
		t.Error("Non-existent lease should not be valid")
	}

	t.Log("Token renewal working correctly")
}

func TestIntegrationApproleAuth(t *testing.T) {
	skipIfNoVault(t)
	setupVaultForTesting(t)

	// Save and restore environment
	originalURL := vaultURL
	originalClient := client
	originalToken := currentToken
	defer func() {
		vaultURL = originalURL
		client = originalClient
		currentToken = originalToken
		os.Unsetenv("VAULT_APP_ROLE")
		os.Unsetenv("VAULT_SECRET_ID")
	}()

	// Configure test environment
	vaultURL = os.Getenv("VAULT_ADDR")
	if vaultURL == "" {
		vaultURL = "http://127.0.0.1:8200"
	}
	client = nil
	currentToken = ""

	os.Setenv("VAULT_APP_ROLE", os.Getenv("TEST_ROLE_ID"))
	os.Setenv("VAULT_SECRET_ID", os.Getenv("TEST_SECRET_ID"))

	// Test authentication
	err := Start()
	if err != nil {
		t.Fatalf("Failed to start: %v", err)
	}

	// Verify we have a token
	token := getCurrentToken()
	if token == "" {
		t.Fatal("No token after authentication")
	}
	t.Logf("Successfully authenticated with AppRole, token: %s...", token[:8])

	// Test making Vault calls
	_, err = GetDbCredentials("test-role", "database")
	// This will fail because we don't have database backend configured
	if err == nil {
		t.Error("Expected error for unconfigured database backend")
	}
	t.Logf("Database call failed as expected: %v", err)
}

func TestIntegrationTokenOnlyAuth(t *testing.T) {
	skipIfNoVault(t)
	setupVaultForTesting(t)

	// First, get a token using userpass
	vaultAddr := os.Getenv("VAULT_ADDR")
	if vaultAddr == "" {
		vaultAddr = "http://127.0.0.1:8200"
	}

	client, err := api.NewClient(&api.Config{
		Address: vaultAddr,
	})
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// Login with userpass to get a token
	loginData := map[string]interface{}{
		"password": "testpass",
	}
	resp, err := client.Logical().Write("auth/userpass/login/testuser", loginData)
	if err != nil {
		t.Fatalf("Failed to login: %v", err)
	}

	testToken := resp.Auth.ClientToken

	// Save and restore environment
	originalURL := vaultURL
	originalClient := client
	originalToken := currentToken
	originalVaultToken := os.Getenv("VAULT_TOKEN")
	defer func() {
		vaultURL = originalURL
		client = originalClient
		currentToken = originalToken
		if originalVaultToken != "" {
			os.Setenv("VAULT_TOKEN", originalVaultToken)
		} else {
			os.Unsetenv("VAULT_TOKEN")
		}
	}()

	// Configure test environment
	vaultURL = vaultAddr
	client = nil
	currentToken = ""
	os.Setenv("VAULT_TOKEN", testToken)

	// Test token-only authentication
	err = Start()
	if err != nil {
		t.Fatalf("Failed to start: %v", err)
	}

	// Verify the token is set
	if getCurrentToken() != testToken {
		t.Errorf("Expected token %s, got %s", testToken, getCurrentToken())
	}

	// Test making Vault calls
	valid := IsLeaseValid("non-existent-lease")
	if valid {
		t.Error("Non-existent lease should not be valid")
	}

	t.Log("Token-only authentication working correctly")
}

func TestIntegration403ErrorRecovery(t *testing.T) {
	skipIfNoVault(t)
	setupVaultForTesting(t)

	// Save and restore environment
	originalURL := vaultURL
	originalClient := client
	originalToken := currentToken
	defer func() {
		vaultURL = originalURL
		client = originalClient
		currentToken = originalToken
		os.Unsetenv("VAULT_LOGIN_USER")
		os.Unsetenv("VAULT_LOGIN_PASSWORD")
	}()

	// Configure test environment
	vaultURL = os.Getenv("VAULT_ADDR")
	if vaultURL == "" {
		vaultURL = "http://127.0.0.1:8200"
	}
	client = nil
	currentToken = ""

	os.Setenv("VAULT_LOGIN_USER", "testuser")
	os.Setenv("VAULT_LOGIN_PASSWORD", "testpass")

	// Authenticate
	err := Start()
	if err != nil {
		t.Fatalf("Failed to start: %v", err)
	}

	validToken := getCurrentToken()
	t.Logf("Initial valid token: %s...", validToken[:8])

	// Corrupt the token to simulate 403 error
	setCurrentToken("invalid-token-that-will-cause-403")

	// Force client to use the invalid token
	if err := refreshClient(); err != nil {
		t.Logf("Client refresh error (expected): %v", err)
	}

	// This call should trigger the retry logic and recover
	valid := IsLeaseValid("test-lease")
	if valid {
		t.Error("Lease should not be valid")
	}

	// Check if we recovered
	recoveredToken := getCurrentToken()
	if recoveredToken == "invalid-token-that-will-cause-403" {
		t.Fatal("Failed to recover from 403 error")
	}

	if recoveredToken == "" {
		t.Fatal("No token after recovery")
	}

	t.Logf("Successfully recovered with new token: %s...", recoveredToken[:8])

	// Verify we can make more calls
	valid = IsLeaseValid("another-test-lease")
	if valid {
		t.Error("Lease should not be valid")
	}
}

func TestIntegrationConcurrentAccess(t *testing.T) {
	skipIfNoVault(t)
	setupVaultForTesting(t)

	// Save and restore environment
	originalURL := vaultURL
	originalClient := client
	originalToken := currentToken
	defer func() {
		vaultURL = originalURL
		client = originalClient
		currentToken = originalToken
		os.Unsetenv("VAULT_LOGIN_USER")
		os.Unsetenv("VAULT_LOGIN_PASSWORD")
	}()

	// Configure test environment
	vaultURL = os.Getenv("VAULT_ADDR")
	if vaultURL == "" {
		vaultURL = "http://127.0.0.1:8200"
	}
	client = nil
	currentToken = ""

	os.Setenv("VAULT_LOGIN_USER", "testuser")
	os.Setenv("VAULT_LOGIN_PASSWORD", "testpass")

	// Authenticate
	err := Start()
	if err != nil {
		t.Fatalf("Failed to start: %v", err)
	}

	// Run concurrent operations
	numGoroutines := 20
	numOperations := 50
	var wg sync.WaitGroup
	errors := make(chan error, numGoroutines*numOperations)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for j := 0; j < numOperations; j++ {
				// Mix of different operations
				switch j % 3 {
				case 0:
					valid := IsLeaseValid(fmt.Sprintf("lease-%d-%d", id, j))
					if valid {
						errors <- fmt.Errorf("lease should not be valid")
					}
				case 1:
					// This will fail but shouldn't cause issues
					_, err := GetDbCredentials("role", "mount")
					if err == nil {
						errors <- fmt.Errorf("expected error for unconfigured database")
					}
				case 2:
					// Just check the token
					if getCurrentToken() == "" {
						errors <- fmt.Errorf("token is empty during concurrent access")
					}
				}

				// Small random delay
				time.Sleep(time.Duration(j%10) * time.Millisecond)
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// Check for errors
	errorCount := 0
	for err := range errors {
		t.Error(err)
		errorCount++
	}

	if errorCount == 0 {
		t.Logf("Successfully completed %d concurrent operations across %d goroutines",
			numOperations*numGoroutines, numGoroutines)
	}
}

func TestIntegrationLongRunning(t *testing.T) {
	skipIfNoVault(t)

	// This test runs for 2 minutes to observe multiple renewal cycles
	if testing.Short() {
		t.Skip("Skipping long-running test in short mode")
	}

	setupVaultForTesting(t)

	// Save and restore environment
	originalURL := vaultURL
	originalClient := client
	originalToken := currentToken
	defer func() {
		vaultURL = originalURL
		client = originalClient
		currentToken = originalToken
		os.Unsetenv("VAULT_LOGIN_USER")
		os.Unsetenv("VAULT_LOGIN_PASSWORD")
	}()

	// Configure test environment
	vaultURL = os.Getenv("VAULT_ADDR")
	if vaultURL == "" {
		vaultURL = "http://127.0.0.1:8200"
	}
	client = nil
	currentToken = ""

	os.Setenv("VAULT_LOGIN_USER", "testuser")
	os.Setenv("VAULT_LOGIN_PASSWORD", "testpass")

	// Authenticate
	err := Start()
	if err != nil {
		t.Fatalf("Failed to start: %v", err)
	}

	initialToken := getCurrentToken()
	t.Logf("Starting long-running test with token: %s...", initialToken[:8])

	// Run for 2 minutes, checking every 10 seconds
	testDuration := 2 * time.Minute
	checkInterval := 10 * time.Second

	tokenChanges := 0
	lastToken := initialToken
	start := time.Now()

	for time.Since(start) < testDuration {
		time.Sleep(checkInterval)

		currentToken := getCurrentToken()
		if currentToken == "" {
			t.Fatal("Token became empty during long-running test")
		}

		// Check if token changed (renewal or re-authentication)
		if currentToken != lastToken {
			tokenChanges++
			elapsed := time.Since(start)
			t.Logf("[%s] Token changed (#%d): %s... -> %s...",
				elapsed.Round(time.Second), tokenChanges,
				lastToken[:8], currentToken[:8])
			lastToken = currentToken
		}

		// Verify we can still make API calls
		valid := IsLeaseValid("test-lease")
		if valid {
			t.Error("Lease should not be valid")
		}
	}

	t.Logf("Long-running test completed. Token changed %d times over %s",
		tokenChanges, testDuration)

	// We expect at least 3 token renewals/re-authentications in 2 minutes
	// (tokens have 30s TTL)
	if tokenChanges < 3 {
		t.Errorf("Expected at least 3 token changes, got %d", tokenChanges)
	}
}
