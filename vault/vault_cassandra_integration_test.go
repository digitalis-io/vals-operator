//go:build integration

package vault

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gocql/gocql"
)

// These tests run against the Cassandra profile of the e2e compose stack in
// hack/docker-compose-e2e.yml and exercise vault.GetDbCredentials against the
// built-in OpenBao Cassandra database plugin. They skip when OPENBAO_ADDR or
// CASSANDRA_HOST is not set.
//
// Quick start:
//
//	make test-cassandra-e2e

// TestGetDbCredentialsCassandra covers the DbSecret path for Cassandra: the
// returned credentials authenticate and the connection details are populated.
func TestGetDbCredentialsCassandra(t *testing.T) {
	baoAddr := baoAddrOrSkip(t)
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

	resetClient(t)
	baoToken := os.Getenv("OPENBAO_TOKEN")
	if baoToken == "" {
		baoToken = os.Getenv("BAO_TOKEN")
	}
	t.Setenv("BAO_ADDR", baoAddr)
	t.Setenv("BAO_TOKEN", baoToken)

	creds, err := GetDbCredentials("readonly", "cassandra")
	if err != nil {
		t.Fatalf("GetDbCredentials failed: %v", err)
	}
	if creds.Username == "" || creds.Password == "" {
		t.Fatalf("returned empty credentials: %+v", creds)
	}
	if creds.LeaseId == "" || creds.LeaseDuration <= 0 {
		t.Errorf("expected a lease with a positive duration, got %q / %d", creds.LeaseId, creds.LeaseDuration)
	}
	// GetDbCredentials reads <mount>/config/<mount>, and the plugin config is
	// named like the mount ("cassandra"), so the host list is populated.
	// TODO(#124): assert host:port once string ports are parsed.
	if !strings.Contains(creds.Hosts, "cassandra") {
		t.Errorf("Hosts should carry the plugin host, got %q", creds.Hosts)
	}

	cluster := gocql.NewCluster(host)
	cluster.Port = port
	cluster.Authenticator = gocql.PasswordAuthenticator{Username: creds.Username, Password: creds.Password}
	cluster.Timeout = vaultIntegTestTimeout
	cluster.ConnectTimeout = vaultIntegTestTimeout
	cluster.DisableInitialHostLookup = true

	// Cassandra caches role state for up to 2s.
	deadline := time.Now().Add(15 * time.Second)
	var lastErr error
	for {
		session, err := cluster.CreateSession()
		if err == nil {
			lastErr = session.Query("SELECT release_version FROM system.local").Exec()
			session.Close()
			if lastErr == nil {
				return
			}
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) {
			t.Fatalf("OpenBao-issued credentials do not authenticate against Cassandra: %v", lastErr)
		}
		time.Sleep(time.Second)
	}
}

// TestGetDbCredentialsCassandraMissingRole verifies that an unknown Cassandra
// role returns an error.
func TestGetDbCredentialsCassandraMissingRole(t *testing.T) {
	baoAddr := baoAddrOrSkip(t)
	if os.Getenv("CASSANDRA_HOST") == "" {
		t.Skip("CASSANDRA_HOST not set")
	}

	resetClient(t)
	baoToken := os.Getenv("OPENBAO_TOKEN")
	if baoToken == "" {
		baoToken = os.Getenv("BAO_TOKEN")
	}
	t.Setenv("BAO_ADDR", baoAddr)
	t.Setenv("BAO_TOKEN", baoToken)

	if _, err := GetDbCredentials("nonexistent_role_xyz", "cassandra"); err == nil {
		t.Error("expected an error for a non-existent role, got nil")
	}
}
