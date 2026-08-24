package clickhouse

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
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

	proto, schemeTLS, hasScheme, rest, err := splitScheme(entry)
	if err != nil {
		return nil, err
	}
	if !hasScheme {
		proto, err = parseProtocol(protocol)
		if err != nil {
			return nil, err
		}
	}

	host, hostPort, err := splitHostPort(rest)
	if err != nil {
		return nil, err
	}
	if hostPort > 0 {
		port = hostPort
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

// splitScheme strips an optional scheme prefix, returning the protocol it
// implies, whether it implies TLS, and the remainder of the entry.
func splitScheme(entry string) (proto ch.Protocol, useTLS bool, hasScheme bool, rest string, err error) {
	idx := strings.Index(entry, "://")
	if idx < 0 {
		return ch.Native, false, false, entry, nil
	}
	rest = entry[idx+3:]
	switch strings.ToLower(entry[:idx]) {
	case "tcp", "native", "clickhouse":
		return ch.Native, false, true, rest, nil
	case "tls", "tcps", "natives", "clickhouses", "clickhouse+tls":
		return ch.Native, true, true, rest, nil
	case "http":
		return ch.HTTP, false, true, rest, nil
	case "https":
		return ch.HTTP, true, true, rest, nil
	}
	return ch.Native, false, false, "", fmt.Errorf("unsupported clickhouse scheme in host %q", entry)
}

// parseProtocol maps the CR's `protocol` field to a driver protocol.
func parseProtocol(protocol string) (ch.Protocol, error) {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "", "tcp", "native", "clickhouse":
		return ch.Native, nil
	case "http", "https":
		return ch.HTTP, nil
	}
	return ch.Native, fmt.Errorf("unsupported clickhouse protocol %q", protocol)
}

// splitHostPort separates an optional port and strips any trailing path.
func splitHostPort(entry string) (string, int, error) {
	if idx := strings.IndexAny(entry, "/?"); idx >= 0 {
		entry = entry[:idx]
	}
	if entry == "" {
		return "", 0, fmt.Errorf("empty clickhouse host")
	}
	host, portStr, err := net.SplitHostPort(entry)
	if err != nil {
		// No port present (or an IPv6 literal without brackets and without a
		// port); use the entry as-is.
		return strings.Trim(entry, "[]"), 0, nil
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 {
		return "", 0, fmt.Errorf("invalid clickhouse port in host %q", entry)
	}
	return host, port, nil
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
