package database

import (
	"digitalis.io/vals-operator/db/cassandra"
	"digitalis.io/vals-operator/db/mysql"
	"digitalis.io/vals-operator/db/postgres"
	dbType "digitalis.io/vals-operator/db/types"
)

// UpdateUserPassword triggers a password update on supported backends
func UpdateUserPassword(dbQuery dbType.DatabaseBackend) error {
	switch dbQuery.Driver {
	case "cassandra":
		return cassandra.UpdateUserPassword(dbQuery)
	case "postgres":
		return postgres.UpdateUserPassword(dbQuery)
	case "mysql":
		return mysql.UpdateUserPassword(dbQuery)
	}
	return nil
}
