# Vault Integration Tests

This directory contains integration tests that verify the Vault authentication and token renewal logic against a real Vault server.

## Overview

The integration tests cover:

1. **Authentication Methods**
   - Userpass authentication
   - AppRole authentication  
   - Token-only authentication

2. **Token Lifecycle**
   - Initial authentication
   - Token renewal before expiration
   - Re-authentication when max TTL is reached
   - Recovery from 403 errors

3. **Concurrency**
   - Multiple goroutines accessing Vault simultaneously
   - Thread-safe token management

4. **Long-running scenarios**
   - 2-minute test observing multiple renewal cycles
   - Ensures stability over time

## Prerequisites

1. Install Vault: https://www.vaultproject.io/downloads
2. Vault server running in dev mode (see setup below)

## Running the Tests

### Quick Setup

Use the provided script to start Vault:

```bash
./scripts/setup-vault-integration-test.sh
```

Then in another terminal:

```bash
export VAULT_ADDR=http://127.0.0.1:8200
export VAULT_ROOT_TOKEN=root-token
export VAULT_INTEGRATION_TEST=true
go test -tags=integration ./vault -v
```

### Manual Setup

1. Start Vault in dev mode:
   ```bash
   vault server -dev -dev-root-token-id=root-token
   ```

2. In another terminal, set environment variables:
   ```bash
   export VAULT_ADDR=http://127.0.0.1:8200
   export VAULT_ROOT_TOKEN=root-token
   export VAULT_INTEGRATION_TEST=true
   ```

3. Run the integration tests:
   ```bash
   go test -tags=integration ./vault -v
   ```

### Running Specific Tests

To run a single test:
```bash
go test -tags=integration ./vault -v -run TestIntegrationUserpassAuth
```

To skip long-running tests:
```bash
go test -tags=integration ./vault -v -short
```

## Test Descriptions

### TestIntegrationUserpassAuth
- Tests authentication using username/password
- Verifies token renewal after 30 seconds
- Ensures API calls continue working after renewal

### TestIntegrationApproleAuth  
- Tests authentication using AppRole method
- Verifies role ID and secret ID authentication
- Tests database credential operations (expected to fail)

### TestIntegrationTokenOnlyAuth
- Tests using a pre-existing token
- Verifies token renewal for renewable tokens
- Tests non-renewable token handling

### TestIntegration403ErrorRecovery
- Simulates a 403 error by corrupting the token
- Verifies automatic recovery through retry logic
- Ensures new authentication after token corruption

### TestIntegrationConcurrentAccess
- Runs 20 goroutines with 50 operations each
- Tests thread-safety of token management
- Verifies no race conditions during concurrent access

### TestIntegrationLongRunning
- Runs for 2 minutes continuously
- Observes multiple token renewal cycles
- Verifies long-term stability

## Configuration

The tests use short TTLs to observe renewal behavior quickly:
- Token TTL: 30 seconds
- Token Max TTL: 5 minutes

## Troubleshooting

### Tests are skipped
- Ensure `VAULT_INTEGRATION_TEST=true` is set
- Check that Vault is running and accessible
- Verify `VAULT_ADDR` is correct

### Authentication failures
- Check Vault logs for errors
- Ensure the root token is correct
- Verify auth methods are enabled

### Token renewal not happening
- Check that tokens are created with `renewable=true`
- Verify the TTL settings in test setup
- Look for errors in test output

## CI/CD Integration

To run these tests in CI:

1. Use HashiCorp's official Vault Docker image:
   ```yaml
   services:
     vault:
       image: vault:1.15
       environment:
         VAULT_DEV_ROOT_TOKEN_ID: root-token
         VAULT_DEV_LISTEN_ADDRESS: 0.0.0.0:8200
       ports:
         - 8200:8200
   ```

2. Set required environment variables:
   ```yaml
   env:
     VAULT_ADDR: http://localhost:8200
     VAULT_ROOT_TOKEN: root-token
     VAULT_INTEGRATION_TEST: true
   ```

3. Run tests with integration tag:
   ```yaml
   - name: Run integration tests
     run: go test -tags=integration ./vault -v
   ```