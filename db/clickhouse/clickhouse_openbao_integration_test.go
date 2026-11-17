//go:build integration

// External test package so this file can import digitalis.io/vals-operator/db
// without creating an import cycle with the internal clickhouse package.
package clickhouse_test

import (
	"context"
	"os"
	"testing"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"
	openbao "github.com/openbao/openbao/api/v2"

	database "digitalis.io/vals-operator/db"
	dbType "digitalis.io/vals-operator/db/types"
)

// These tests run against the e2e compose stack in hack/docker-compose-e2e.yml.
// All tests skip cleanly when OPENBAO_ADDR is not set.
//
// Quick start:
//
//	docker compose -f hack/docker-compose-e2e.yml up -d --build --wait clickhouse openbao
//	docker compose -f hack/docker-compose-e2e.yml run --rm configure
//	OPENBAO_ADDR=http://127.0.0.1:8200 OPENBAO_TOKEN=root \
//	CLICKHOUSE_NATIVE_ADDR=127.0.0.1:9000 CLICKHOUSE_LOGIN_PASSWORD=clickhouse \
//	go test -tags integration -race ./db/clickhouse/...

const e2eTestTimeout = 15 * time.Second

func baoClientForE2E(t *testing.T) *openbao.Client {
	t.Helper()
	addr := os.Getenv("OPENBAO_ADDR")
	if addr == "" {
		t.Skip("OPENBAO_ADDR not set")
	}
	cfg := openbao.DefaultConfig()
	cfg.Address = addr
	c, err := openbao.NewClient(cfg)
	if err != nil {
		t.Fatalf("cannot create OpenBao client: %v", err)
	}
	token := os.Getenv("OPENBAO_TOKEN")
	if token == "" {
		token = os.Getenv("BAO_TOKEN")
	}
	if token != "" {
		c.SetToken(token)
	}
	return c
}

func chAddrE2E(t *testing.T) string {
	t.Helper()
	addr := os.Getenv("CLICKHOUSE_NATIVE_ADDR")
	if addr == "" {
		t.Skip("CLICKHOUSE_NATIVE_ADDR not set")
	}
	return addr
}

func chLoginPassword() string {
	if p := os.Getenv("CLICKHOUSE_LOGIN_PASSWORD"); p != "" {
		return p
	}
	return "clickhouse"
}

// openConn opens a native-protocol ClickHouse connection as the default admin.
func openConn(t *testing.T, addr string) ch.Conn {
	t.Helper()
	conn, err := ch.Open(&ch.Options{
		Addr: []string{addr},
		Auth: ch.Auth{Database: "default", Username: "default", Password: chLoginPassword()},
	})
	if err != nil {
		t.Fatalf("cannot open ClickHouse connection: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// createTestUser creates a ClickHouse user and registers a cleanup to drop it.
func createTestUser(t *testing.T, addr, username, password string) {
	t.Helper()
	conn := openConn(t, addr)
	ctx, cancel := context.WithTimeout(context.Background(), e2eTestTimeout)
	defer cancel()
	if err := conn.Exec(ctx, "DROP USER IF EXISTS `"+username+"`"); err != nil {
		t.Fatalf("cannot drop user: %v", err)
	}
	if err := conn.Exec(ctx, "CREATE USER `"+username+"` IDENTIFIED BY '"+password+"'"); err != nil {
		t.Fatalf("cannot create user: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), e2eTestTimeout)
		defer cancel()
		_ = conn.Exec(ctx, "DROP USER IF EXISTS `"+username+"`")
	})
}

// canAuthE2E reports whether the given ClickHouse user can authenticate.
func canAuthE2E(t *testing.T, addr, username, password string) bool {
	t.Helper()
	conn, err := ch.Open(&ch.Options{
		Addr: []string{addr},
		Auth: ch.Auth{Database: "default", Username: username, Password: password},
	})
	if err != nil {
		return false
	}
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), e2eTestTimeout)
	defer cancel()
	return conn.Ping(ctx) == nil
}

// TestOpenBaoRotationPath covers the ValsSecret.spec.databases[] path at the
// lighter level recommended by issue #107: a password stored in OpenBao KV is
// read back and used to call db.UpdateUserPassword() against ClickHouse.
func TestOpenBaoRotationPath(t *testing.T) {
	bao := baoClientForE2E(t)
	addr := chAddrE2E(t)

	s, err := bao.Logical().Read("secret/data/vals-clickhouse")
	if err != nil {
		t.Fatalf("cannot read rotation secret from OpenBao: %v", err)
	}
	if s == nil {
		t.Skip("rotation secret not found in OpenBao (run the configure container first)")
	}
	data, ok := s.Data["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected KV v2 response format: %T", s.Data["data"])
	}
	newPassword, _ := data["password"].(string)
	if newPassword == "" {
		t.Fatal("rotation secret has no 'password' field")
	}

	createTestUser(t, addr, "vals_openbao_rotation", "initial_password")

	err = database.UpdateUserPassword(dbType.DatabaseBackend{
		Driver:        "clickhouse",
		Username:      "vals_openbao_rotation",
		Password:      newPassword,
		LoginUsername: "default",
		LoginPassword: chLoginPassword(),
		Hosts:         []string{addr},
		TLS:           "disable",
	})
	if err != nil {
		t.Fatalf("rotation failed: %v", err)
	}

	if !canAuthE2E(t, addr, "vals_openbao_rotation", newPassword) {
		t.Error("user cannot authenticate with the password from OpenBao")
	}
	if canAuthE2E(t, addr, "vals_openbao_rotation", "initial_password") {
		t.Error("user can still authenticate with the old password after rotation")
	}
}

// TestOpenBaoUnreachable verifies that an unreachable OpenBao server produces
// an error rather than a silent failure.
func TestOpenBaoUnreachable(t *testing.T) {
	if os.Getenv("OPENBAO_ADDR") == "" {
		t.Skip("OPENBAO_ADDR not set")
	}

	cfg := openbao.DefaultConfig()
	cfg.Address = "http://127.0.0.1:1" // port 1 is unreachable
	cfg.MaxRetries = 0
	c, err := openbao.NewClient(cfg)
	if err != nil {
		t.Fatalf("cannot create OpenBao client: %v", err)
	}
	c.SetToken("root")

	_, err = c.Logical().Read("clickhouse/creds/readonly")
	if err == nil {
		t.Error("expected an error from an unreachable OpenBao, got nil")
	}
}

// TestOpenBaoMissingRole verifies that requesting credentials for a
// non-existent role returns no secret (nil secret or error).
func TestOpenBaoMissingRole(t *testing.T) {
	bao := baoClientForE2E(t)

	s, err := bao.Logical().Read("clickhouse/creds/nonexistent_role_xyz")
	// OpenBao returns (nil, nil) for a 404; either that or an error is acceptable.
	// A non-nil secret means the role unexpectedly exists.
	if err == nil && s != nil {
		t.Error("expected nil secret or error for a non-existent role, got a valid secret")
	}
}

// TestOpenBaoAlterUserDenied verifies that using login credentials that lack
// SQL-driven access control causes UpdateUserPassword to return an error
// rather than silently succeeding or panicking.
func TestOpenBaoAlterUserDenied(t *testing.T) {
	bao := baoClientForE2E(t)
	addr := chAddrE2E(t)

	// Issue credentials from the readonly role; they have only SELECT access,
	// not ALTER USER / ACCESS MANAGEMENT.
	s, err := bao.Logical().Read("clickhouse/creds/readonly")
	if err != nil {
		t.Fatalf("cannot read readonly creds from OpenBao: %v", err)
	}
	if s == nil {
		t.Fatal("cannot read readonly creds from OpenBao: nil secret")
	}
	limitedUser, _ := s.Data["username"].(string)
	limitedPass, _ := s.Data["password"].(string)
	if limitedUser == "" || limitedPass == "" {
		t.Fatalf("OpenBao returned empty credentials: %v", s.Data)
	}
	// No cleanup needed: OpenBao revokes the dynamic user when the lease expires.

	createTestUser(t, addr, "vals_alter_target", "somepassword")

	err = database.UpdateUserPassword(dbType.DatabaseBackend{
		Driver:        "clickhouse",
		Username:      "vals_alter_target",
		Password:      "newpassword",
		LoginUsername: limitedUser,
		LoginPassword: limitedPass,
		Hosts:         []string{addr},
		TLS:           "disable",
	})
	if err == nil {
		t.Error("expected ALTER USER to be denied for a read-only credential, got nil error")
	}

	// The old password must still authenticate since rotation failed.
	if !canAuthE2E(t, addr, "vals_alter_target", "somepassword") {
		t.Error("original password no longer works after a failed rotation")
	}
}
