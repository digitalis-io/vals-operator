#!/bin/bash

# Script to set up Vault for integration testing
# This script starts a Vault server in dev mode and configures it for testing

set -e

echo "Setting up Vault for integration testing..."

# Check if vault is installed
if ! command -v vault &> /dev/null; then
    echo "Error: vault command not found. Please install Vault first."
    echo "Visit: https://www.vaultproject.io/downloads"
    exit 1
fi

# Kill any existing vault dev server
echo "Stopping any existing Vault dev servers..."
pkill -f "vault server -dev" || true
sleep 2

# Start vault in dev mode with a known root token
echo "Starting Vault in dev mode..."
vault server -dev -dev-root-token-id=root-token -dev-listen-address=127.0.0.1:8200 > vault.log 2>&1 &
VAULT_PID=$!

# Wait for Vault to start
echo "Waiting for Vault to start..."
sleep 3

# Export Vault address
export VAULT_ADDR="http://127.0.0.1:8200"
export VAULT_ROOT_TOKEN="root-token"

# Check if Vault is running
if ! vault status &> /dev/null; then
    echo "Error: Vault failed to start. Check vault.log for details."
    exit 1
fi

echo "Vault is running at $VAULT_ADDR with PID $VAULT_PID"
echo ""
echo "To run integration tests:"
echo "  export VAULT_ADDR=$VAULT_ADDR"
echo "  export VAULT_ROOT_TOKEN=$VAULT_ROOT_TOKEN" 
echo "  export VAULT_INTEGRATION_TEST=true"
echo "  go test -tags=integration ./vault -v"
echo ""
echo "To run a specific test:"
echo "  go test -tags=integration ./vault -v -run TestIntegrationUserpassAuth"
echo ""
echo "To stop Vault when done:"
echo "  kill $VAULT_PID"
echo ""
echo "Vault logs are being written to: vault.log"