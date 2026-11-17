//go:build integration

package cassandra_test

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gocql/gocql"
	openbao "github.com/openbao/openbao/api/v2"

	database "digitalis.io/vals-operator/db"
	dbType "digitalis.io/vals-operator/db/types"
)

// These tests run against the Cassandra profile of the e2e compose stack in
// hack/docker-compose-e2e.yml. They skip when OPENBAO_ADDR or CASSANDRA_HOST
// is not set.
//
// Quick start:
//
//	make test-cassandra-e2e

// All credentials below are throwaway values for the local/CI compose stack.
const (
	adminUser = "cassandra"
	adminPass = "cassandra"

	// Cassandra caches role credentials for 2s by default, so a changed or
	// dropped role can keep working briefly.
	authSettleTimeout = 15 * time.Second
	// shortlived role has a 15s TTL; leave headroom for the expiry manager.
	expiryTimeout = 90 * time.Second
	// Cassandra rejects ALTER ROLE ... PASSWORD on a role changed within the
	// last 5s ("can only be changed every 5000ms").
	passwordChangeInterval = 6 * time.Second
)

func baoClient(t *testing.T) *openbao.Client {
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
	c.SetToken(token)
	return c
}

func cassandraTarget(t *testing.T) (string, int) {
	t.Helper()
	host := os.Getenv("CASSANDRA_HOST")
	if host == "" {
		t.Skip("CASSANDRA_HOST not set")
	}
	port := 9042
	if p := os.Getenv("CASSANDRA_PORT"); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil {
			t.Fatalf("invalid CASSANDRA_PORT %q: %v", p, err)
		}
		port = n
	}
	return host, port
}

func newCluster(host string, port int, user, pass string) *gocql.ClusterConfig {
	c := gocql.NewCluster(host)
	c.Port = port
	c.Authenticator = gocql.PasswordAuthenticator{Username: user, Password: pass}
	c.Timeout = 10 * time.Second
	c.ConnectTimeout = 10 * time.Second
	c.DisableInitialHostLookup = true
	return c
}

// canAuth reports whether the role can log in and run a query.
func canAuth(host string, port int, user, pass string) bool {
	session, err := newCluster(host, port, user, pass).CreateSession()
	if err != nil {
		return false
	}
	defer session.Close()
	return session.Query("SELECT release_version FROM system.local").Exec() == nil
}

// authDenied reports whether Cassandra rejected the login because the role or
// password is invalid. Connection errors are not a denial, so a stopped
// server cannot make a "credentials were revoked" check pass.
func authDenied(host string, port int, user, pass string) bool {
	session, err := newCluster(host, port, user, pass).CreateSession()
	if err == nil {
		session.Close()
		return false
	}
	return strings.Contains(err.Error(), "password are incorrect")
}

func eventually(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for: %s", timeout, what)
		}
		time.Sleep(time.Second)
	}
}

// createRole creates a login role as the admin and drops it on cleanup.
func createRole(t *testing.T, host string, port int, name, password string) {
	t.Helper()
	session, err := newCluster(host, port, adminUser, adminPass).CreateSession()
	if err != nil {
		t.Fatalf("cannot connect as admin: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Query(fmt.Sprintf("DROP ROLE IF EXISTS %q", name)).Exec()
		session.Close()
	})
	if err := session.Query(fmt.Sprintf("DROP ROLE IF EXISTS %q", name)).Exec(); err != nil {
		t.Fatalf("cannot drop role: %v", err)
	}
	q := fmt.Sprintf("CREATE ROLE %q WITH PASSWORD = '%s' AND LOGIN = true", name, strings.ReplaceAll(password, "'", "''"))
	if err := session.Query(q).Exec(); err != nil {
		t.Fatalf("cannot create role: %v", err)
	}
	time.Sleep(passwordChangeInterval)
}

func readCreds(t *testing.T, bao *openbao.Client, role string) (user, pass, leaseID string) {
	t.Helper()
	s, err := bao.Logical().Read("cassandra/creds/" + role)
	if err != nil {
		t.Fatalf("cannot read %s creds from OpenBao: %v", role, err)
	}
	if s == nil {
		t.Fatalf("OpenBao returned no credentials for role %q (run configure-cassandra first)", role)
	}
	t.Cleanup(func() { _ = bao.Sys().Revoke(s.LeaseID) })
	user, _ = s.Data["username"].(string)
	pass, _ = s.Data["password"].(string)
	if user == "" || pass == "" {
		t.Fatalf("OpenBao returned empty credentials: username set=%t password set=%t", user != "", pass != "")
	}
	return user, pass, s.LeaseID
}

// TestDynamicCredentialsAuthenticate verifies that credentials issued by the
// OpenBao Cassandra plugin can log in.
func TestDynamicCredentialsAuthenticate(t *testing.T) {
	bao := baoClient(t)
	host, port := cassandraTarget(t)

	user, pass, _ := readCreds(t, bao, "readonly")
	eventually(t, authSettleTimeout, "issued credentials to authenticate", func() bool {
		return canAuth(host, port, user, pass)
	})
	if !authDenied(host, port, user, pass+"x") {
		t.Error("role authenticated with a wrong password")
	}
}

// TestDynamicCredentialsRevokedWithLease verifies that revoking the lease
// drops the role in Cassandra.
func TestDynamicCredentialsRevokedWithLease(t *testing.T) {
	bao := baoClient(t)
	host, port := cassandraTarget(t)

	user, pass, lease := readCreds(t, bao, "readonly")
	eventually(t, authSettleTimeout, "issued credentials to authenticate", func() bool {
		return canAuth(host, port, user, pass)
	})

	if err := bao.Sys().Revoke(lease); err != nil {
		t.Fatalf("cannot revoke lease: %v", err)
	}
	eventually(t, authSettleTimeout, "revoked credentials to stop authenticating", func() bool {
		return authDenied(host, port, user, pass)
	})
}

// TestDynamicCredentialsExpire verifies that credentials stop working once
// the role TTL elapses and OpenBao revokes the lease.
func TestDynamicCredentialsExpire(t *testing.T) {
	bao := baoClient(t)
	host, port := cassandraTarget(t)

	user, pass, _ := readCreds(t, bao, "shortlived")
	eventually(t, authSettleTimeout, "issued credentials to authenticate", func() bool {
		return canAuth(host, port, user, pass)
	})
	eventually(t, expiryTimeout, "credentials to expire after the role TTL", func() bool {
		return authDenied(host, port, user, pass)
	})
}

// TestDynamicCredentialsAreUnique verifies that each read issues a new role.
func TestDynamicCredentialsAreUnique(t *testing.T) {
	bao := baoClient(t)
	cassandraTarget(t)

	u1, p1, _ := readCreds(t, bao, "readonly")
	u2, p2, _ := readCreds(t, bao, "readonly")
	if u1 == u2 || p1 == p2 {
		t.Errorf("two reads returned the same credentials: %q / %q", u1, u2)
	}
}

// TestRotationPath covers the ValsSecret.spec.databases[] path: a new password
// is applied with db.UpdateUserPassword(), after which only
// the new password works.
func TestRotationPath(t *testing.T) {
	baoClient(t)
	host, port := cassandraTarget(t)

	const role = "vals_openbao_rotation"
	createRole(t, host, port, role, "initial_password")

	err := database.UpdateUserPassword(dbType.DatabaseBackend{
		Driver:        "cassandra",
		Username:      role,
		Password:      "rotated_password_123",
		LoginUsername: adminUser,
		LoginPassword: adminPass,
		Hosts:         []string{host},
		Port:          port,
	})
	if err != nil {
		t.Fatalf("rotation failed: %v", err)
	}

	eventually(t, authSettleTimeout, "new password to authenticate", func() bool {
		return canAuth(host, port, role, "rotated_password_123")
	})
	eventually(t, authSettleTimeout, "old password to stop authenticating", func() bool {
		return authDenied(host, port, role, "initial_password")
	})
}

// TestRotationQuotesPassword verifies that a password containing quote
// characters is applied verbatim.
func TestRotationQuotesPassword(t *testing.T) {
	baoClient(t)
	host, port := cassandraTarget(t)

	const role = "vals_openbao_quotes"
	const pass = `it's "quoted"; DROP ROLE x`
	createRole(t, host, port, role, "initial_password")

	err := database.UpdateUserPassword(dbType.DatabaseBackend{
		Driver:        "cassandra",
		Username:      role,
		Password:      pass,
		LoginUsername: adminUser,
		LoginPassword: adminPass,
		Hosts:         []string{host},
		Port:          port,
	})
	if err != nil {
		t.Fatalf("rotation failed: %v", err)
	}
	eventually(t, authSettleTimeout, "quoted password to authenticate", func() bool {
		return canAuth(host, port, role, pass)
	})
}

// TestRotationDeniedForUnprivilegedLogin verifies that a login role without
// ALTER permission cannot rotate, and that the failure leaves the target
// password untouched.
func TestRotationDeniedForUnprivilegedLogin(t *testing.T) {
	bao := baoClient(t)
	host, port := cassandraTarget(t)

	limitedUser, limitedPass, _ := readCreds(t, bao, "readonly")
	eventually(t, authSettleTimeout, "issued credentials to authenticate", func() bool {
		return canAuth(host, port, limitedUser, limitedPass)
	})

	const role = "vals_alter_target"
	createRole(t, host, port, role, "somepassword")

	err := database.UpdateUserPassword(dbType.DatabaseBackend{
		Driver:        "cassandra",
		Username:      role,
		Password:      "newpassword",
		LoginUsername: limitedUser,
		LoginPassword: limitedPass,
		Hosts:         []string{host},
		Port:          port,
	})
	if err == nil {
		t.Error("expected ALTER ROLE to be denied for a read-only role, got nil error")
	} else if !strings.Contains(err.Error(), "sufficient privileges") {
		t.Errorf("expected a privileges error, got: %v", err)
	}
	if !canAuth(host, port, role, "somepassword") {
		t.Error("original password no longer works after a failed rotation")
	}
}

// TestRotationBadLogin verifies that wrong admin credentials surface an error.
func TestRotationBadLogin(t *testing.T) {
	baoClient(t)
	host, port := cassandraTarget(t)

	err := database.UpdateUserPassword(dbType.DatabaseBackend{
		Driver:        "cassandra",
		Username:      "whoever",
		Password:      "newpassword",
		LoginUsername: adminUser,
		LoginPassword: "wrong-password",
		Hosts:         []string{host},
		Port:          port,
	})
	if err == nil {
		t.Error("expected an error for invalid login credentials, got nil")
	}
}

// TestMissingRole verifies that an unknown role returns no credentials.
func TestMissingRole(t *testing.T) {
	bao := baoClient(t)

	s, err := bao.Logical().Read("cassandra/creds/nonexistent_role_xyz")
	if err == nil && s != nil {
		t.Error("expected nil secret or error for a non-existent role, got a valid secret")
	}
}
