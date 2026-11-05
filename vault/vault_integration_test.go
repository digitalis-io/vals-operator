//go:build integration

package vault

import (
	"context"
	"os"
	"testing"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"
)

// These tests run against the e2e compose stack in hack/docker-compose-e2e.yml
// and exercise vault.GetDbCredentials against the OpenBao ClickHouse database
// plugin.  All tests skip when OPENBAO_ADDR is not set.
//
// Quick start:
//
//	docker compose -f hack/docker-compose-e2e.yml up -d --build --wait clickhouse openbao
//	docker compose -f hack/docker-compose-e2e.yml run --rm configure
//	OPENBAO_ADDR=http://127.0.0.1:8200 OPENBAO_TOKEN=root \
//	BAO_ADDR=http://127.0.0.1:8200 BAO_TOKEN=root \
//	CLICKHOUSE_NATIVE_ADDR=127.0.0.1:9000 CLICKHOUSE_LOGIN_PASSWORD=clickhouse \
//	go test -tags integration -race ./vault/...

const vaultIntegTestTimeout = 10 * time.Second

// resetClient clears the package-level client before and after each test so
// each test sees fresh environment variables.
func resetClient(t *testing.T) {
	t.Helper()
	client = nil
	t.Cleanup(func() { client = nil })
}

func baoAddrOrSkip(t *testing.T) string {
	t.Helper()
	addr := os.Getenv("OPENBAO_ADDR")
	if addr == "" {
		t.Skip("OPENBAO_ADDR not set")
	}
	return addr
}

// TestGetDbCredentialsOpenBao covers the DbSecret path: GetDbCredentials reads
// dynamic credentials from the OpenBao ClickHouse database plugin, and those
// credentials authenticate against a running ClickHouse server.
func TestGetDbCredentialsOpenBao(t *testing.T) {
	baoAddr := baoAddrOrSkip(t)
	chAddr := os.Getenv("CLICKHOUSE_NATIVE_ADDR")
	if chAddr == "" {
		t.Skip("CLICKHOUSE_NATIVE_ADDR not set")
	}

	resetClient(t)
	baoToken := os.Getenv("OPENBAO_TOKEN")
	if baoToken == "" {
		baoToken = os.Getenv("BAO_TOKEN")
	}
	t.Setenv("BAO_ADDR", baoAddr)
	t.Setenv("BAO_TOKEN", baoToken)

	creds, err := GetDbCredentials("readonly", "clickhouse")
	if err != nil {
		t.Fatalf("GetDbCredentials failed: %v", err)
	}
	if creds.Username == "" || creds.Password == "" {
		t.Fatalf("returned empty credentials: %+v", creds)
	}
	// The mount and connection share the same name ("clickhouse"), so vault.go
	// reads clickhouse/config/clickhouse and populates ConnectionURL.
	if creds.ConnectionURL == "" {
		t.Error("ConnectionURL should be non-empty when mount and connection share a name")
	}

	// Verify that the issued credentials actually authenticate.
	conn, err := ch.Open(&ch.Options{
		Addr: []string{chAddr},
		Auth: ch.Auth{Database: "default", Username: creds.Username, Password: creds.Password},
	})
	if err != nil {
		t.Fatalf("cannot open ClickHouse connection with OpenBao credentials: %v", err)
	}
	defer func() { _ = conn.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), vaultIntegTestTimeout)
	defer cancel()
	if err := conn.Ping(ctx); err != nil {
		t.Errorf("OpenBao-issued credentials do not authenticate against ClickHouse: %v", err)
	}
}

// TestGetDbCredentialsUnreachable verifies that an unreachable OpenBao server
// surfaces an error.
func TestGetDbCredentialsUnreachable(t *testing.T) {
	baoAddrOrSkip(t)

	resetClient(t)
	t.Setenv("BAO_ADDR", "http://127.0.0.1:1") // port 1 is unreachable
	t.Setenv("BAO_TOKEN", "root")
	t.Setenv("BAO_MAX_RETRIES", "0")

	_, err := GetDbCredentials("readonly", "clickhouse")
	if err == nil {
		t.Error("expected an error from an unreachable OpenBao, got nil")
	}
}

// TestGetDbCredentialsMissingRole verifies that requesting a non-existent role
// or mount returns an error.
func TestGetDbCredentialsMissingRole(t *testing.T) {
	baoAddr := baoAddrOrSkip(t)

	resetClient(t)
	baoToken := os.Getenv("OPENBAO_TOKEN")
	if baoToken == "" {
		baoToken = os.Getenv("BAO_TOKEN")
	}
	t.Setenv("BAO_ADDR", baoAddr)
	t.Setenv("BAO_TOKEN", baoToken)

	_, err := GetDbCredentials("nonexistent_role_xyz", "clickhouse")
	if err == nil {
		t.Error("expected an error for a non-existent role, got nil")
	}
}
