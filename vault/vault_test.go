package vault

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/vault/api"
)

// mockVaultServer creates a test HTTP server that mimics Vault API responses
type mockVaultServer struct {
	*httptest.Server
	tokenValidUntil time.Time
	currentToken    string
	tokenRenewable  bool
	failAuth        bool
	failWithCode    int
	requestCount    int32
	authCallCount   int32
	renewCallCount  int32
	lookupCallCount int32
	readCallCount   int32
	mu              sync.Mutex
}

func newMockVaultServer() *mockVaultServer {
	m := &mockVaultServer{
		tokenValidUntil: time.Now().Add(1 * time.Hour),
		currentToken:    "test-token",
		tokenRenewable:  true,
		failWithCode:    0,
	}

	m.Server = httptest.NewServer(http.HandlerFunc(m.handler))
	return m
}

func (m *mockVaultServer) handler(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt32(&m.requestCount, 1)
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check token validity
	authHeader := r.Header.Get("X-Vault-Token")
	if authHeader != "" && authHeader != m.currentToken && !strings.Contains(r.URL.Path, "/auth/") {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"errors": []string{"permission denied"},
		})
		return
	}

	// Simulate failure if configured
	if m.failWithCode > 0 {
		w.WriteHeader(m.failWithCode)
		if m.failWithCode == http.StatusForbidden {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"errors": []string{"permission denied"},
			})
		}
		return
	}

	switch {
	// Kubernetes auth
	case r.Method == "POST" && r.URL.Path == "/v1/auth/kubernetes/login":
		atomic.AddInt32(&m.authCallCount, 1)
		if m.failAuth {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"errors": []string{"authentication failed"},
			})
			return
		}
		m.currentToken = fmt.Sprintf("token-%d", time.Now().Unix())
		m.tokenValidUntil = time.Now().Add(30 * time.Second)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"auth": map[string]interface{}{
				"client_token":   m.currentToken,
				"renewable":      m.tokenRenewable,
				"lease_duration": 30,
			},
		})

	// Token lookup
	case r.Method == "GET" && r.URL.Path == "/v1/auth/token/lookup-self":
		atomic.AddInt32(&m.lookupCallCount, 1)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"renewable": m.tokenRenewable,
				"ttl":       int(time.Until(m.tokenValidUntil).Seconds()),
			},
		})

	// Token renewal
	case r.Method == "POST" && r.URL.Path == "/v1/auth/token/renew-self":
		atomic.AddInt32(&m.renewCallCount, 1)
		if time.Now().After(m.tokenValidUntil) {
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"errors": []string{"token expired"},
			})
			return
		}
		m.tokenValidUntil = time.Now().Add(30 * time.Second)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"auth": map[string]interface{}{
				"client_token":   m.currentToken,
				"renewable":      m.tokenRenewable,
				"lease_duration": 30,
			},
		})

	// Database credentials
	case r.Method == "GET" && strings.Contains(r.URL.Path, "/database/creds/"):
		atomic.AddInt32(&m.readCallCount, 1)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"lease_id":       "database/creds/test-role/test-lease",
			"lease_duration": 3600,
			"data": map[string]interface{}{
				"username": "test-user",
				"password": "test-password",
			},
		})

	// Database config
	case r.Method == "GET" && strings.Contains(r.URL.Path, "/database/config/"):
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"connection_details": map[string]interface{}{
					"hosts":          "localhost",
					"port":           json.Number("5432"),
					"connection_url": "postgresql://{{username}}:{{password}}@localhost:5432/testdb",
				},
			},
		})

	// Lease operations
	case r.Method == "PUT" && r.URL.Path == "/v1/sys/leases/lookup":
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"renewable": true,
				"ttl":       3600,
			},
		})

	case r.Method == "PUT" && strings.Contains(r.URL.Path, "/sys/leases/renew"):
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"lease_id":       "database/creds/test-role/test-lease",
			"lease_duration": 3600,
		})

	case r.Method == "PUT" && strings.Contains(r.URL.Path, "/sys/leases/revoke"):
		w.WriteHeader(http.StatusNoContent)

	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (m *mockVaultServer) setTokenExpired() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokenValidUntil = time.Now().Add(-1 * time.Minute)
}

func (m *mockVaultServer) setFailWithCode(code int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failWithCode = code
}

func (m *mockVaultServer) setFailAuth(fail bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failAuth = fail
}

func (m *mockVaultServer) getRequestCount() int32 {
	return atomic.LoadInt32(&m.requestCount)
}

func TestVaultClientCreation(t *testing.T) {
	// Save original values
	originalURL := vaultURL
	originalClient := client
	defer func() {
		vaultURL = originalURL
		client = originalClient
	}()

	mockServer := newMockVaultServer()
	defer mockServer.Close()

	vaultURL = mockServer.URL
	client = nil

	// Test client creation
	c, err := getOrCreateClient()
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	if c == nil {
		t.Fatal("Client should not be nil")
	}

	// Test that subsequent calls return the same client
	c2, err := getOrCreateClient()
	if err != nil {
		t.Fatalf("Failed to get client: %v", err)
	}
	if c != c2 {
		t.Error("Expected same client instance")
	}
}

func TestConcurrentClientAccess(t *testing.T) {
	// Save original values
	originalURL := vaultURL
	originalClient := client
	defer func() {
		vaultURL = originalURL
		client = originalClient
	}()

	mockServer := newMockVaultServer()
	defer mockServer.Close()

	vaultURL = mockServer.URL
	client = nil

	// Test concurrent client access
	var wg sync.WaitGroup
	errors := make([]error, 100)
	clients := make([]*api.Client, 100)

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			c, err := getOrCreateClient()
			errors[idx] = err
			clients[idx] = c
		}(i)
	}

	wg.Wait()

	// All operations should succeed
	for i, err := range errors {
		if err != nil {
			t.Errorf("Error at index %d: %v", i, err)
		}
	}

	// All clients should be the same instance
	firstClient := clients[0]
	for i, c := range clients {
		if c != firstClient {
			t.Errorf("Client mismatch at index %d", i)
		}
	}
}

func TestTokenLifecycleManagement(t *testing.T) {
	// Save original values
	originalURL := vaultURL
	originalClient := client
	originalToken := currentToken
	defer func() {
		vaultURL = originalURL
		client = originalClient
		currentToken = originalToken
	}()

	mockServer := newMockVaultServer()
	defer mockServer.Close()

	vaultURL = mockServer.URL
	client = nil

	// Create client
	c, err := getOrCreateClient()
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// Test successful token lifecycle management
	token := &api.Secret{
		Auth: &api.SecretAuth{
			ClientToken:   mockServer.currentToken,
			Renewable:     true,
			LeaseDuration: 30,
		},
	}
	if err := setCurrentToken(mockServer.currentToken); err != nil {
		t.Fatalf("Failed to set current token: %v", err)
	}

	// Run lifecycle management in background with short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	go func() {
		err := manageTokenLifecycle(c, token)
		if err != nil {
			t.Logf("Token lifecycle ended: %v", err)
		}
	}()

	// Wait and check for renewals
	<-ctx.Done()

	// The lifecycle manager should have attempted renewal
	// (Note: actual renewal count depends on timing)
	t.Logf("Renewal attempts: %d", atomic.LoadInt32(&mockServer.renewCallCount))
}

func TestExecuteWithRetry403Errors(t *testing.T) {
	// Save original values
	originalURL := vaultURL
	originalClient := client
	originalToken := currentToken
	defer func() {
		vaultURL = originalURL
		client = originalClient
		currentToken = originalToken
	}()

	mockServer := newMockVaultServer()
	defer mockServer.Close()

	vaultURL = mockServer.URL
	client = nil
	if err := setCurrentToken("valid-token"); err != nil {
		t.Fatalf("Failed to set current token: %v", err)
	}

	// Test successful operation
	result, err := executeWithRetry(func(c *api.Client) (string, error) {
		// Simulate a successful Vault operation
		return "success", nil
	})
	if err != nil {
		t.Fatalf("Operation should succeed: %v", err)
	}
	if result != "success" {
		t.Errorf("Expected 'success', got %s", result)
	}

	// Test 403 error with retry
	attempt := 0
	mockServer.setFailWithCode(http.StatusForbidden)
	result, err = executeWithRetry(func(c *api.Client) (string, error) {
		attempt++
		if attempt < 2 {
			// First attempt will fail with 403
			return "", fmt.Errorf("403 permission denied")
		}
		// Second attempt succeeds
		return "retry-success", nil
	})
	if err != nil {
		t.Fatalf("Retry should succeed: %v", err)
	}
	if result != "retry-success" {
		t.Errorf("Expected 'retry-success', got %s", result)
	}
	if attempt != 2 {
		t.Errorf("Expected 2 attempts, got %d", attempt)
	}

	// Test max retries exceeded
	mockServer.setFailWithCode(http.StatusForbidden)
	attempt = 0
	result, err = executeWithRetry(func(c *api.Client) (string, error) {
		attempt++
		return "", fmt.Errorf("403 permission denied")
	})
	if err == nil {
		t.Fatal("Expected error after max retries")
	}
	if !strings.Contains(err.Error(), "operation failed after 3 retries") {
		t.Errorf("Expected retry error message, got: %v", err)
	}
	if attempt != 3 {
		t.Errorf("Expected 3 attempts, got %d", attempt)
	}
}

func TestGetDbCredentials(t *testing.T) {
	// Save original values
	originalURL := vaultURL
	originalClient := client
	originalToken := currentToken
	defer func() {
		vaultURL = originalURL
		client = originalClient
		currentToken = originalToken
	}()

	mockServer := newMockVaultServer()
	defer mockServer.Close()

	vaultURL = mockServer.URL
	client = nil
	if err := setCurrentToken(mockServer.currentToken); err != nil { // Use the mock's current token
		t.Fatalf("Failed to set current token: %v", err)
	}

	// Test successful credential retrieval
	creds, err := GetDbCredentials("test-role", "database")
	if err != nil {
		t.Fatalf("Failed to get credentials: %v", err)
	}
	if creds.Username != "test-user" {
		t.Errorf("Expected username 'test-user', got %s", creds.Username)
	}
	if creds.Password != "test-password" {
		t.Errorf("Expected password 'test-password', got %s", creds.Password)
	}
	if creds.LeaseId != "database/creds/test-role/test-lease" {
		t.Errorf("Expected lease ID 'database/creds/test-role/test-lease', got %s", creds.LeaseId)
	}
	if creds.LeaseDuration != 3600 {
		t.Errorf("Expected lease duration 3600, got %d", creds.LeaseDuration)
	}
	if creds.Hosts != "localhost:5432" {
		t.Errorf("Expected hosts 'localhost:5432', got %s", creds.Hosts)
	}
	if creds.ConnectionURL != "postgresql://test-user:test-password@localhost:5432/testdb" {
		t.Errorf("Expected connection URL, got %s", creds.ConnectionURL)
	}

	// Test with expired token (should trigger retry)
	mockServer.setTokenExpired()
	mockServer.currentToken = "expired-token"
	creds, err = GetDbCredentials("test-role", "database")
	// This will fail because our mock doesn't handle the full auth flow
	if err == nil {
		t.Error("Expected error with expired token")
	}
}

func TestLeaseOperations(t *testing.T) {
	// Save original values
	originalURL := vaultURL
	originalClient := client
	originalToken := currentToken
	defer func() {
		vaultURL = originalURL
		client = originalClient
		currentToken = originalToken
	}()

	mockServer := newMockVaultServer()
	defer mockServer.Close()

	vaultURL = mockServer.URL
	client = nil
	if err := setCurrentToken(mockServer.currentToken); err != nil { // Use the mock's current token
		t.Fatalf("Failed to set current token: %v", err)
	}

	// Test lease validation
	valid := IsLeaseValid("database/creds/test-role/test-lease")
	if !valid {
		t.Error("Lease should be valid")
	}

	valid = IsLeaseValid("")
	if valid {
		t.Error("Empty lease should be invalid")
	}

	// Test lease renewal
	err := RenewDbCredentials("database/creds/test-role/test-lease", 3600)
	if err != nil {
		t.Errorf("Lease renewal failed: %v", err)
	}

	err = RenewDbCredentials("", 3600)
	if err == nil {
		t.Error("Expected error for empty lease ID")
	}

	// Test lease revocation
	err = RevokeDbCredentials("database/creds/test-role/test-lease")
	if err != nil {
		t.Errorf("Lease revocation failed: %v", err)
	}

	err = RevokeDbCredentials("")
	if err == nil {
		t.Error("Expected error for empty lease ID")
	}
}

func TestTokenSetAndGet(t *testing.T) {
	// Save original
	originalToken := currentToken
	defer func() {
		currentToken = originalToken
	}()

	// Test setting and getting token
	if err := setCurrentToken("test-token-123"); err != nil {
		t.Fatalf("Failed to set test token: %v", err)
	}
	if getCurrentToken() != "test-token-123" {
		t.Errorf("Expected 'test-token-123', got %s", getCurrentToken())
	}

	// Test concurrent access
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func(idx int) {
			defer wg.Done()
			if err := setCurrentToken(fmt.Sprintf("token-%d", idx)); err != nil {
				t.Errorf("Failed to set token: %v", err)
			}
		}(i)
		go func() {
			defer wg.Done()
			_ = getCurrentToken()
		}()
	}
	wg.Wait()

	// Token should be set to one of the values
	finalToken := getCurrentToken()
	if !strings.Contains(finalToken, "token-") {
		t.Errorf("Expected token to contain 'token-', got %s", finalToken)
	}
}

func TestChildProcessInheritsVaultToken(t *testing.T) {
	// Save original values
	originalToken := currentToken
	originalEnvToken := os.Getenv("VAULT_TOKEN")
	defer func() {
		currentToken = originalToken
		if originalEnvToken != "" {
			os.Setenv("VAULT_TOKEN", originalEnvToken)
		} else {
			os.Unsetenv("VAULT_TOKEN")
		}
	}()

	// Set a test token
	testToken := "test-child-process-token-12345"
	if err := setCurrentToken(testToken); err != nil {
		t.Fatalf("Failed to set current token: %v", err)
	}

	// Create a simple child process that echoes the VAULT_TOKEN environment variable
	cmd := exec.Command("sh", "-c", "echo $VAULT_TOKEN")

	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Failed to run child process: %v", err)
	}

	// Check that the child process sees our token
	childToken := strings.TrimSpace(string(output))
	if childToken != testToken {
		t.Errorf("Child process did not inherit correct VAULT_TOKEN. Expected %s, got %s", testToken, childToken)
	}

	t.Logf("✓ Child process correctly inherited VAULT_TOKEN: %s", childToken)

	// Test with another token to ensure changes propagate
	newToken := "updated-child-token-67890"
	if err := setCurrentToken(newToken); err != nil {
		t.Fatalf("Failed to set updated token: %v", err)
	}

	cmd = exec.Command("sh", "-c", "echo $VAULT_TOKEN")
	output, err = cmd.Output()
	if err != nil {
		t.Fatalf("Failed to run second child process: %v", err)
	}

	childToken = strings.TrimSpace(string(output))
	if childToken != newToken {
		t.Errorf("Child process did not inherit updated VAULT_TOKEN. Expected %s, got %s", newToken, childToken)
	}

	t.Logf("✓ Child process correctly inherited updated VAULT_TOKEN: %s", childToken)
}

func TestSetCurrentTokenSetsEnvironmentVariable(t *testing.T) {
	// Save original values
	originalToken := currentToken
	originalEnvToken := os.Getenv("VAULT_TOKEN")
	defer func() {
		currentToken = originalToken
		if originalEnvToken != "" {
			os.Setenv("VAULT_TOKEN", originalEnvToken)
		} else {
			os.Unsetenv("VAULT_TOKEN")
		}
	}()

	// Test setting a token
	testToken := "test-token-12345"
	if err := setCurrentToken(testToken); err != nil {
		t.Fatalf("Failed to set current token: %v", err)
	}

	// Verify the token is set in memory
	if getCurrentToken() != testToken {
		t.Errorf("Expected current token %s, got %s", testToken, getCurrentToken())
	}

	// Verify the environment variable is set
	envToken := os.Getenv("VAULT_TOKEN")
	if envToken != testToken {
		t.Errorf("Expected VAULT_TOKEN env var %s, got %s", testToken, envToken)
	}

	// Test setting a different token
	newToken := "new-token-67890"
	if err := setCurrentToken(newToken); err != nil {
		t.Fatalf("Failed to set new token: %v", err)
	}

	// Verify both memory and environment are updated
	if getCurrentToken() != newToken {
		t.Errorf("Expected current token %s, got %s", newToken, getCurrentToken())
	}

	envToken = os.Getenv("VAULT_TOKEN")
	if envToken != newToken {
		t.Errorf("Expected VAULT_TOKEN env var %s, got %s", newToken, envToken)
	}
}

func TestEnvironmentVariableForChildProcesses(t *testing.T) {
	// Save original values
	originalToken := currentToken
	originalEnvToken := os.Getenv("VAULT_TOKEN")
	defer func() {
		currentToken = originalToken
		if originalEnvToken != "" {
			os.Setenv("VAULT_TOKEN", originalEnvToken)
		} else {
			os.Unsetenv("VAULT_TOKEN")
		}
	}()

	// Set a token
	testToken := "child-process-token"
	if err := setCurrentToken(testToken); err != nil {
		t.Fatalf("Failed to set current token: %v", err)
	}

	// Verify the environment variable can be read by child processes
	// We'll simulate this by checking os.Getenv directly
	childEnvToken := os.Getenv("VAULT_TOKEN")
	if childEnvToken != testToken {
		t.Errorf("Child processes would not see correct token. Expected %s, got %s", testToken, childEnvToken)
	}

	t.Logf("Child processes will inherit VAULT_TOKEN=%s", childEnvToken)
}
