//go:build integration

package clickhouse

import (
	"context"
	"os"
	"testing"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"

	dbType "digitalis.io/vals-operator/db/types"
)

// These tests run against a real ClickHouse server with SQL-driven access
// control enabled for the login user. Set the addresses to run them:
//
//	CLICKHOUSE_NATIVE_ADDR=127.0.0.1:9000 \
//	CLICKHOUSE_HTTP_ADDR=127.0.0.1:8123 \
//	CLICKHOUSE_NATIVE_TLS_ADDR=127.0.0.1:9440 \
//	CLICKHOUSE_HTTPS_ADDR=127.0.0.1:8443 \
//	CLICKHOUSE_LOGIN_PASSWORD=rootpass \
//	go test -tags integration -race ./db/clickhouse/...
//
// hack/clickhouse-test-server.sh starts a server configured this way.
// testTimeout bounds every server call the tests make, so an unresponsive
// server fails the test instead of hanging it.
const testTimeout = 10 * time.Second

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

// loginUsername is the account used to run ALTER USER. It needs SQL-driven
// access control.
func loginUsername() string {
	if username := os.Getenv("CLICKHOUSE_LOGIN_USERNAME"); username != "" {
		return username
	}
	return DefaultUser
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
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	return conn.Ping(ctx) == nil
}

func createUser(t *testing.T, addr, username, password string) {
	t.Helper()
	conn, err := ch.Open(&ch.Options{
		Addr: []string{addr},
		Auth: ch.Auth{Database: "default", Username: loginUsername(), Password: loginPassword()},
	})
	if err != nil {
		t.Fatalf("cannot open connection: %v", err)
	}
	defer func() {
		_ = conn.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	if err := conn.Exec(ctx, "DROP USER IF EXISTS "+quoteIdentifier(username)); err != nil {
		t.Fatalf("cannot drop user: %v", err)
	}
	if err := conn.Exec(ctx, "CREATE USER "+quoteIdentifier(username)+" IDENTIFIED BY "+quoteLiteral(password)); err != nil {
		t.Fatalf("cannot create user: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		_ = conn.Exec(ctx, "DROP USER IF EXISTS "+quoteIdentifier(username))
	})
}

func TestRotationOverNativeProtocol(t *testing.T) {
	addr := addrOrSkip(t, "CLICKHOUSE_NATIVE_ADDR")
	createUser(t, addr, "vals_native", "oldpass")

	err := UpdateUserPassword(dbType.DatabaseBackend{
		Username:      "vals_native",
		Password:      "newpass",
		LoginUsername: loginUsername(),
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
		LoginUsername: loginUsername(),
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
		LoginUsername: loginUsername(),
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
		LoginUsername: loginUsername(),
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
	// only proves the defaults when the server listens on 9000 and the
	// `default` user is the one with SQL-driven access control.
	if os.Getenv("CLICKHOUSE_NATIVE_ADDR_IS_DEFAULT_PORT") == "" || loginUsername() != DefaultUser {
		t.Skip("the server is not reachable as default@:9000")
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

func TestRotationOverNativeTLS(t *testing.T) {
	addr := addrOrSkip(t, "CLICKHOUSE_NATIVE_TLS_ADDR")
	nativeAddr := addrOrSkip(t, "CLICKHOUSE_NATIVE_ADDR")
	createUser(t, nativeAddr, "vals_native_tls", "oldpass")

	err := UpdateUserPassword(dbType.DatabaseBackend{
		Username:      "vals_native_tls",
		Password:      "newpass",
		LoginUsername: loginUsername(),
		LoginPassword: loginPassword(),
		Hosts:         []string{"tls://" + addr},
		TLS:           TLSSkipVerify,
	})
	if err != nil {
		t.Fatalf("rotation failed: %v", err)
	}

	if !canAuthenticate(t, nativeAddr, "vals_native_tls", "newpass") {
		t.Error("the user cannot authenticate with the new password")
	}
}

func TestRotationOverHTTPS(t *testing.T) {
	addr := addrOrSkip(t, "CLICKHOUSE_HTTPS_ADDR")
	nativeAddr := addrOrSkip(t, "CLICKHOUSE_NATIVE_ADDR")
	createUser(t, nativeAddr, "vals_https", "oldpass")

	err := UpdateUserPassword(dbType.DatabaseBackend{
		Username:      "vals_https",
		Password:      "newpass",
		LoginUsername: loginUsername(),
		LoginPassword: loginPassword(),
		Hosts:         []string{"https://" + addr},
		TLS:           TLSSkipVerify,
	})
	if err != nil {
		t.Fatalf("rotation failed: %v", err)
	}

	if !canAuthenticate(t, nativeAddr, "vals_https", "newpass") {
		t.Error("the user cannot authenticate with the new password")
	}
}

func TestRotationPrefersTLSAndFallsBackToPlaintext(t *testing.T) {
	// The default TLS mode against the plaintext native port: the TLS
	// attempt must fail and the plaintext one must succeed.
	addr := addrOrSkip(t, "CLICKHOUSE_NATIVE_ADDR")
	createUser(t, addr, "vals_preferred", "oldpass")

	err := UpdateUserPassword(dbType.DatabaseBackend{
		Username:      "vals_preferred",
		Password:      "newpass",
		LoginUsername: loginUsername(),
		LoginPassword: loginPassword(),
		Hosts:         []string{addr},
	})
	if err != nil {
		t.Fatalf("rotation failed: %v", err)
	}

	if !canAuthenticate(t, addr, "vals_preferred", "newpass") {
		t.Error("the user cannot authenticate with the new password")
	}
}
