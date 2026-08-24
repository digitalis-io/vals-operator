//go:build integration

package clickhouse

import (
	"context"
	"os"
	"testing"

	ch "github.com/ClickHouse/clickhouse-go/v2"

	dbType "digitalis.io/vals-operator/db/types"
)

// These tests run against a real ClickHouse server with SQL-driven access
// control enabled for the login user. Set the addresses to run them:
//
//	CLICKHOUSE_NATIVE_ADDR=127.0.0.1:9000 \
//	CLICKHOUSE_HTTP_ADDR=127.0.0.1:8123 \
//	CLICKHOUSE_LOGIN_PASSWORD=rootpass \
//	go test -tags integration -race ./db/clickhouse/...
func addrOrSkip(t *testing.T, env string) string {
	t.Helper()
	addr := os.Getenv(env)
	if addr == "" {
		t.Skipf("%s not set", env)
	}
	return addr
}

func loginPassword() string {
	return os.Getenv("CLICKHOUSE_LOGIN_PASSWORD")
}

// canAuthenticate reports whether the user can log in with the given password.
func canAuthenticate(t *testing.T, addr, username, password string) bool {
	t.Helper()
	conn, err := ch.Open(&ch.Options{
		Addr: []string{addr},
		Auth: ch.Auth{Database: "default", Username: username, Password: password},
	})
	if err != nil {
		t.Fatalf("cannot open connection: %v", err)
	}
	defer func() {
		_ = conn.Close()
	}()
	return conn.Ping(context.Background()) == nil
}

func createUser(t *testing.T, addr, username, password string) {
	t.Helper()
	conn, err := ch.Open(&ch.Options{
		Addr: []string{addr},
		Auth: ch.Auth{Database: "default", Username: "default", Password: loginPassword()},
	})
	if err != nil {
		t.Fatalf("cannot open connection: %v", err)
	}
	defer func() {
		_ = conn.Close()
	}()

	ctx := context.Background()
	if err := conn.Exec(ctx, "DROP USER IF EXISTS "+quoteIdentifier(username)); err != nil {
		t.Fatalf("cannot drop user: %v", err)
	}
	if err := conn.Exec(ctx, "CREATE USER "+quoteIdentifier(username)+" IDENTIFIED BY "+quoteLiteral(password)); err != nil {
		t.Fatalf("cannot create user: %v", err)
	}
	t.Cleanup(func() {
		_ = conn.Exec(context.Background(), "DROP USER IF EXISTS "+quoteIdentifier(username))
	})
}

func TestRotationOverNativeProtocol(t *testing.T) {
	addr := addrOrSkip(t, "CLICKHOUSE_NATIVE_ADDR")
	createUser(t, addr, "vals_native", "oldpass")

	err := UpdateUserPassword(dbType.DatabaseBackend{
		Username:      "vals_native",
		Password:      "newpass",
		LoginPassword: loginPassword(),
		Hosts:         []string{addr},
		TLS:           TLSDisable,
	})
	if err != nil {
		t.Fatalf("rotation failed: %v", err)
	}

	if !canAuthenticate(t, addr, "vals_native", "newpass") {
		t.Error("the user cannot authenticate with the new password")
	}
	if canAuthenticate(t, addr, "vals_native", "oldpass") {
		t.Error("the user can still authenticate with the old password")
	}
}

func TestRotationOverHTTPProtocol(t *testing.T) {
	addr := addrOrSkip(t, "CLICKHOUSE_HTTP_ADDR")
	nativeAddr := addrOrSkip(t, "CLICKHOUSE_NATIVE_ADDR")
	createUser(t, nativeAddr, "vals_http", "oldpass")

	err := UpdateUserPassword(dbType.DatabaseBackend{
		Username:      "vals_http",
		Password:      "newpass",
		LoginPassword: loginPassword(),
		Hosts:         []string{"http://" + addr},
	})
	if err != nil {
		t.Fatalf("rotation failed: %v", err)
	}

	if !canAuthenticate(t, nativeAddr, "vals_http", "newpass") {
		t.Error("the user cannot authenticate with the new password")
	}
}

func TestRotationEscapesHostileNames(t *testing.T) {
	addr := addrOrSkip(t, "CLICKHOUSE_NATIVE_ADDR")
	const username = "o'brien`; DROP USER alice --"
	const password = `p'; GRANT ALL ON *.* TO o\'brien --`
	createUser(t, addr, username, "oldpass")

	err := UpdateUserPassword(dbType.DatabaseBackend{
		Username:      username,
		Password:      password,
		LoginPassword: loginPassword(),
		Hosts:         []string{addr},
		TLS:           TLSDisable,
	})
	if err != nil {
		t.Fatalf("rotation failed: %v", err)
	}

	if !canAuthenticate(t, addr, username, password) {
		t.Error("the user cannot authenticate with the new password")
	}
}

func TestRotationFailsOverToTheNextHost(t *testing.T) {
	addr := addrOrSkip(t, "CLICKHOUSE_NATIVE_ADDR")
	createUser(t, addr, "vals_failover", "oldpass")

	err := UpdateUserPassword(dbType.DatabaseBackend{
		Username:      "vals_failover",
		Password:      "newpass",
		LoginPassword: loginPassword(),
		Hosts:         []string{"127.0.0.1:1", addr},
		TLS:           TLSDisable,
	})
	if err != nil {
		t.Fatalf("rotation failed: %v", err)
	}

	if !canAuthenticate(t, addr, "vals_failover", "newpass") {
		t.Error("the user cannot authenticate with the new password")
	}
}

func TestRotationUsesTheDefaultLoginUser(t *testing.T) {
	addr := addrOrSkip(t, "CLICKHOUSE_NATIVE_ADDR")
	createUser(t, addr, "vals_defaults", "oldpass")

	// No LoginUsername and no Port: `default` and 9000 must be used. This
	// only proves the defaults when the server listens on 9000.
	if os.Getenv("CLICKHOUSE_NATIVE_ADDR_IS_DEFAULT_PORT") == "" {
		t.Skip("CLICKHOUSE_NATIVE_ADDR_IS_DEFAULT_PORT not set")
	}
	host, _, err := splitHostPort(addr)
	if err != nil {
		t.Fatalf("cannot parse %q: %v", addr, err)
	}

	err = UpdateUserPassword(dbType.DatabaseBackend{
		Username:      "vals_defaults",
		Password:      "newpass",
		LoginPassword: loginPassword(),
		Hosts:         []string{host},
		TLS:           TLSDisable,
	})
	if err != nil {
		t.Fatalf("rotation failed: %v", err)
	}

	if !canAuthenticate(t, addr, "vals_defaults", "newpass") {
		t.Error("the user cannot authenticate with the new password")
	}
}
