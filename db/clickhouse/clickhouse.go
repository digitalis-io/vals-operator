package clickhouse

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"

	dbType "digitalis.io/vals-operator/db/types"
	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	// DefaultUser is the ClickHouse user used to log in when none is given
	DefaultUser = "default"
	// DefaultNativePort is the plaintext native protocol port
	DefaultNativePort = 9000
	// DefaultNativeTLSPort is the TLS native protocol port
	DefaultNativeTLSPort = 9440
	// DefaultHTTPPort is the plaintext HTTP protocol port
	DefaultHTTPPort = 8123
	// DefaultHTTPSPort is the TLS HTTP protocol port
	DefaultHTTPSPort = 8443

	connectTimeout = 10 * time.Second
)

// TLS modes accepted in the CR's `tls` field.
const (
	// TLSPreferred connects over TLS and falls back to plaintext if the
	// server does not offer it. This is the default and mirrors the
	// `tls=preferred` behaviour of the MySQL driver.
	TLSPreferred = "preferred"
	// TLSDisable never uses TLS
	TLSDisable = "disable"
	// TLSRequire always uses TLS and verifies the server certificate
	TLSRequire = "require"
	// TLSSkipVerify always uses TLS but does not verify the server certificate
	TLSSkipVerify = "skip-verify"
)

// connection is a resolved endpoint to talk to: where, over which protocol
// and whether TLS is used.
type connection struct {
	host     string
	port     int
	protocol ch.Protocol
	useTLS   bool
}

func (c connection) addr() string {
	return net.JoinHostPort(c.host, strconv.Itoa(c.port))
}

// String renders the connection as a URL for logging. It never contains
// credentials.
func (c connection) String() string {
	scheme := "tcp"
	if c.protocol == ch.HTTP {
		scheme = "http"
	}
	if c.useTLS {
		scheme += "s"
	}
	return fmt.Sprintf("%s://%s", scheme, c.addr())
}

// quoteIdentifier quotes a ClickHouse identifier (user name) with backticks.
func quoteIdentifier(identifier string) string {
	return "`" + strings.ReplaceAll(strings.ReplaceAll(identifier, `\`, `\\`), "`", "``") + "`"
}

// quoteLiteral quotes a ClickHouse string literal.
func quoteLiteral(literal string) string {
	return "'" + strings.ReplaceAll(strings.ReplaceAll(literal, `\`, `\\`), "'", "''") + "'"
}

// defaultPort returns the well-known port for a protocol/TLS combination.
func defaultPort(protocol ch.Protocol, useTLS bool) int {
	if protocol == ch.HTTP {
		if useTLS {
			return DefaultHTTPSPort
		}
		return DefaultHTTPPort
	}
	if useTLS {
		return DefaultNativeTLSPort
	}
	return DefaultNativePort
}

// tlsModes returns the TLS settings to try, in order, for the given mode.
// `preferred` yields two attempts: TLS first, then plaintext.
func tlsModes(mode string) (attempts []bool, skipVerify bool, err error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", TLSPreferred:
		return []bool{true, false}, true, nil
	case TLSDisable, "false", "off", "none":
		return []bool{false}, false, nil
	case TLSRequire, "true", "on", "enable", "enabled", "verify":
		return []bool{true}, false, nil
	case TLSSkipVerify, "insecure", "insecure-skip-verify":
		return []bool{true}, true, nil
	}
	return nil, false, fmt.Errorf("unsupported clickhouse tls mode %q", mode)
}

// schemes maps an explicit URL scheme to the protocol it implies and whether
// it pins TLS.
var schemes = map[string]struct {
	protocol ch.Protocol
	useTLS   bool
}{
	"tcp":            {ch.Native, false},
	"native":         {ch.Native, false},
	"clickhouse":     {ch.Native, false},
	"tls":            {ch.Native, true},
	"tcps":           {ch.Native, true},
	"natives":        {ch.Native, true},
	"clickhouses":    {ch.Native, true},
	"clickhouse+tls": {ch.Native, true},
	"http":           {ch.HTTP, false},
	"https":          {ch.HTTP, true},
}

// parseHost turns a host entry into the connection(s) to attempt. A host may
// carry an explicit scheme (`tcp://`, `native://`, `clickhouse://`, `tls://`,
// `clickhouses://`, `http://`, `https://`) and an explicit `:port`, either of
// which overrides the CR-level defaults. Without a scheme the CR's protocol
// and TLS settings decide, and `preferred` produces a TLS attempt followed by
// a plaintext one.
func parseHost(entry string, protocol string, tlsAttempts []bool, port int) ([]connection, error) {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return nil, fmt.Errorf("empty clickhouse host")
	}

	// A scheme-less entry such as `ch.example.com:9000` is not a valid URL:
	// url.Parse would read the host as the scheme. Prefixing `//` makes it a
	// network-path reference, which parses as a bare authority.
	raw := entry
	if !strings.Contains(raw, "://") {
		raw = "//" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid clickhouse host %q: %w", entry, err)
	}

	host := u.Hostname()
	if host == "" {
		return nil, fmt.Errorf("empty clickhouse host")
	}

	hasScheme := u.Scheme != ""
	var proto ch.Protocol
	var schemeTLS bool
	if hasScheme {
		s, ok := schemes[u.Scheme]
		if !ok {
			return nil, fmt.Errorf("unsupported clickhouse scheme in host %q", entry)
		}
		proto, schemeTLS = s.protocol, s.useTLS
	} else if proto, err = parseProtocol(protocol); err != nil {
		return nil, err
	}

	if p := u.Port(); p != "" {
		parsed, err := strconv.Atoi(p)
		if err != nil || parsed < 1 {
			return nil, fmt.Errorf("invalid clickhouse port in host %q", entry)
		}
		port = parsed
	}

	// An explicit scheme pins the TLS decision, otherwise the tls mode does.
	attempts := tlsAttempts
	if hasScheme {
		attempts = []bool{schemeTLS}
	}

	var conns []connection
	for _, useTLS := range attempts {
		p := port
		if p < 1 {
			p = defaultPort(proto, useTLS)
		}
		conns = append(conns, connection{host: host, port: p, protocol: proto, useTLS: useTLS})
	}
	return conns, nil
}

// parseProtocol maps the CR's `protocol` field to a driver protocol.
func parseProtocol(protocol string) (ch.Protocol, error) {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "", "tcp", "native", "clickhouse":
		return ch.Native, nil
	case "http":
		return ch.HTTP, nil
	}
	return ch.Native, fmt.Errorf("unsupported clickhouse protocol %q", protocol)
}

// options builds the driver options for a connection.
func options(dbQuery dbType.DatabaseBackend, conn connection, skipVerify bool) *ch.Options {
	opts := &ch.Options{
		Addr:     []string{conn.addr()},
		Protocol: conn.protocol,
		Auth: ch.Auth{
			Database: "default",
			Username: dbQuery.LoginUsername,
			Password: dbQuery.LoginPassword,
		},
		DialTimeout: connectTimeout,
	}
	if conn.useTLS {
		opts.TLS = &tls.Config{
			ServerName:         conn.host,
			InsecureSkipVerify: skipVerify,
			MinVersion:         tls.VersionTLS12,
		}
	}
	return opts
}

// runClickhouseQuery rotates the password over a single resolved connection.
func runClickhouseQuery(dbQuery dbType.DatabaseBackend, conn connection, skipVerify bool) error {
	db, err := ch.Open(options(dbQuery, conn, skipVerify))
	if err != nil {
		return err
	}
	defer func() {
		_ = db.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()

	if err := db.Ping(ctx); err != nil {
		return err
	}

	// ClickHouse has no bind parameters for DDL, so the identifier and the
	// literal are quoted and escaped instead of interpolated raw.
	query := fmt.Sprintf("ALTER USER %s IDENTIFIED BY %s",
		quoteIdentifier(dbQuery.Username), quoteLiteral(dbQuery.Password))

	return db.Exec(ctx, query)
}

// UpdateUserPassword updates the user's password
func UpdateUserPassword(dbQuery dbType.DatabaseBackend) error {
	log := ctrl.Log.WithName("clickhouse")

	/* Default user */
	if dbQuery.LoginUsername == "" {
		dbQuery.LoginUsername = DefaultUser
	}

	tlsAttempts, skipVerify, err := tlsModes(dbQuery.TLS)
	if err != nil {
		log.Error(err, "Password not updated")
		return err
	}

	for _, host := range dbQuery.Hosts {
		var conns []connection
		conns, err = parseHost(host, dbQuery.Protocol, tlsAttempts, dbQuery.Port)
		if err != nil {
			log.Error(err, fmt.Sprintf("Cannot parse host %s", host))
			continue
		}
		for _, conn := range conns {
			err = runClickhouseQuery(dbQuery, conn, skipVerify)
			if err != nil {
				log.Error(err, fmt.Sprintf("Cannot run query on host %s", conn))
				continue
			}
			log.Info(fmt.Sprintf("Query successful on host %s", conn))
			log.Info("ClickHouse password updated successfully")
			return nil
		}
	}

	if err == nil {
		err = fmt.Errorf("no clickhouse hosts configured")
	}
	log.Error(err, "Password not updated")

	return err
}
