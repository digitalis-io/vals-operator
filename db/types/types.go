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
}
