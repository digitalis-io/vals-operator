package types

type DatabaseQuery struct {
	Username      string
	Password      string
	UserHost      string
	LoginUsername string
	LoginPassword string
	Hosts         []string
	Port          int
	Driver        string
}
