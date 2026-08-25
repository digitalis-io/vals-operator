package types

// DatabaseBackend object for database queries
type DatabaseBackend struct {
	Username      string
	Password      string
	UserHost      string
	LoginUsername string
	LoginPassword string
	Hosts         []string
	Port          int
	Driver        string
	// Protocol selects the wire protocol where the backend supports more
	// than one (ClickHouse: native/tcp or http)
	Protocol string
	// TLS selects the TLS mode where the backend supports it (ClickHouse:
	// preferred, disable, require, skip-verify)
	TLS string
}
