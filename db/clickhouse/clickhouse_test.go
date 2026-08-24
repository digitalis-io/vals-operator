package clickhouse

import (
	"fmt"
	"testing"

	ch "github.com/ClickHouse/clickhouse-go/v2"

	dbType "digitalis.io/vals-operator/db/types"
)

func TestQuoteIdentifier(t *testing.T) {
	tests := map[string]string{
		"alice":       "`alice`",
		"al`ice":      "`al``ice`",
		`al\ice`:      "`al\\\\ice`",
		"a` OR 1=1--": "`a`` OR 1=1--`",
	}
	for in, want := range tests {
		if got := quoteIdentifier(in); got != want {
			t.Errorf("quoteIdentifier(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestQuoteLiteral(t *testing.T) {
	tests := map[string]string{
		"s3cret":        "'s3cret'",
		"s3'cret":       "'s3''cret'",
		`s3\cret`:       `'s3\\cret'`,
		"' OR ''='":     "''' OR ''''='''",
		`\' OR 1=1 -- `: `'\\'' OR 1=1 -- '`,
	}
	for in, want := range tests {
		if got := quoteLiteral(in); got != want {
			t.Errorf("quoteLiteral(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTLSModes(t *testing.T) {
	tests := []struct {
		mode       string
		attempts   []bool
		skipVerify bool
		wantErr    bool
	}{
		{mode: "", attempts: []bool{true, false}, skipVerify: true},
		{mode: "preferred", attempts: []bool{true, false}, skipVerify: true},
		{mode: "disable", attempts: []bool{false}},
		{mode: "require", attempts: []bool{true}},
		{mode: "skip-verify", attempts: []bool{true}, skipVerify: true},
		{mode: "REQUIRE", attempts: []bool{true}},
		{mode: "banana", wantErr: true},
	}
	for _, tt := range tests {
		attempts, skipVerify, err := tlsModes(tt.mode)
		if tt.wantErr {
			if err == nil {
				t.Errorf("tlsModes(%q) expected an error", tt.mode)
			}
			continue
		}
		if err != nil {
			t.Errorf("tlsModes(%q) returned %v", tt.mode, err)
			continue
		}
		if fmt.Sprint(attempts) != fmt.Sprint(tt.attempts) || skipVerify != tt.skipVerify {
			t.Errorf("tlsModes(%q) = %v/%v, want %v/%v",
				tt.mode, attempts, skipVerify, tt.attempts, tt.skipVerify)
		}
	}
}

func TestParseHost(t *testing.T) {
	tests := []struct {
		name     string
		entry    string
		protocol string
		tlsMode  string
		port     int
		want     []string
		wantErr  bool
	}{
		{
			name:  "bare host defaults to native with a TLS attempt first",
			entry: "ch.example.com",
			want:  []string{"tcps://ch.example.com:9440", "tcp://ch.example.com:9000"},
		},
		{
			name:    "tls disabled uses the plaintext native port only",
			entry:   "ch.example.com",
			tlsMode: "disable",
			want:    []string{"tcp://ch.example.com:9000"},
		},
		{
			name:     "http protocol defaults to 8123",
			entry:    "ch.example.com",
			protocol: "http",
			tlsMode:  "disable",
			want:     []string{"http://ch.example.com:8123"},
		},
		{
			name:     "http protocol with TLS required defaults to 8443",
			entry:    "ch.example.com",
			protocol: "http",
			tlsMode:  "require",
			want:     []string{"https://ch.example.com:8443"},
		},
		{
			name:  "https scheme pins TLS and the HTTP protocol",
			entry: "https://ch.example.com",
			want:  []string{"https://ch.example.com:8443"},
		},
		{
			name:    "tcp scheme pins plaintext native even when TLS is preferred",
			entry:   "tcp://ch.example.com",
			tlsMode: "preferred",
			want:    []string{"tcp://ch.example.com:9000"},
		},
		{
			name:  "tls scheme pins TLS native",
			entry: "tls://ch.example.com",
			want:  []string{"tcps://ch.example.com:9440"},
		},
		{
			name:  "port in the host entry wins over the default",
			entry: "http://ch.example.com:9999/path",
			want:  []string{"http://ch.example.com:9999"},
		},
		{
			name:    "CR port wins over the protocol default",
			entry:   "ch.example.com",
			tlsMode: "disable",
			port:    9001,
			want:    []string{"tcp://ch.example.com:9001"},
		},
		{
			name:    "IPv6 literal with a port",
			entry:   "[2001:db8::1]:9000",
			tlsMode: "disable",
			want:    []string{"tcp://[2001:db8::1]:9000"},
		},
		{
			name:    "IPv6 literal without a port",
			entry:   "[2001:db8::1]",
			tlsMode: "disable",
			want:    []string{"tcp://[2001:db8::1]:9000"},
		},
		{name: "empty host", entry: "  ", wantErr: true},
		{name: "unknown scheme", entry: "ftp://ch.example.com", wantErr: true},
		{name: "unknown protocol", entry: "ch.example.com", protocol: "carrier-pigeon", wantErr: true},
		{name: "invalid port", entry: "ch.example.com:zero", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attempts, _, err := tlsModes(tt.tlsMode)
			if err != nil {
				t.Fatalf("tlsModes(%q) returned %v", tt.tlsMode, err)
			}
			conns, err := parseHost(tt.entry, tt.protocol, attempts, tt.port)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseHost(%q) expected an error, got %v", tt.entry, conns)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseHost(%q) returned %v", tt.entry, err)
			}
			var got []string
			for _, c := range conns {
				got = append(got, c.String())
			}
			if fmt.Sprint(got) != fmt.Sprint(tt.want) {
				t.Errorf("parseHost(%q) = %v, want %v", tt.entry, got, tt.want)
			}
		})
	}
}

func TestOptionsCarryCredentialsAndTLS(t *testing.T) {
	dbQuery := dbType.DatabaseBackend{LoginUsername: "admin", LoginPassword: "hunter2"}
	conn := connection{host: "ch.example.com", port: 9440, protocol: ch.Native, useTLS: true}

	opts := options(dbQuery, conn, true)
	if opts.Auth.Username != "admin" || opts.Auth.Password != "hunter2" {
		t.Errorf("credentials not passed through: %+v", opts.Auth)
	}
	if opts.Addr[0] != "ch.example.com:9440" {
		t.Errorf("unexpected address %q", opts.Addr[0])
	}
	if opts.TLS == nil || !opts.TLS.InsecureSkipVerify || opts.TLS.ServerName != "ch.example.com" {
		t.Errorf("unexpected TLS config %+v", opts.TLS)
	}

	if opts := options(dbQuery, connection{host: "h", port: 9000, protocol: ch.Native}, false); opts.TLS != nil {
		t.Errorf("expected no TLS config for a plaintext connection")
	}
}

func TestUpdateUserPasswordDefaultsAndFailure(t *testing.T) {
	// Every host is unreachable, so the call must return the last error
	// rather than nil.
	err := UpdateUserPassword(dbType.DatabaseBackend{
		Username: "alice",
		Password: "s3cret",
		Hosts:    []string{"127.0.0.1:1", "127.0.0.1:2"},
		TLS:      TLSDisable,
	})
	if err == nil {
		t.Fatal("expected an error when every host fails")
	}
}

func TestUpdateUserPasswordWithoutHosts(t *testing.T) {
	if err := UpdateUserPassword(dbType.DatabaseBackend{Username: "alice"}); err == nil {
		t.Fatal("expected an error when no hosts are configured")
	}
}

func TestUpdateUserPasswordRejectsBadTLSMode(t *testing.T) {
	err := UpdateUserPassword(dbType.DatabaseBackend{
		Username: "alice",
		Hosts:    []string{"ch.example.com"},
		TLS:      "banana",
	})
	if err == nil {
		t.Fatal("expected an error for an unsupported TLS mode")
	}
}
