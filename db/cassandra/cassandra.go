package cassandra

import (
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	"github.com/gocql/gocql"

	dbType "digitalis.io/vals-operator/db/types"
	ctrl "sigs.k8s.io/controller-runtime"
)

// CQL Quoting...
func quoteIdentifier(identifier string) string {
	return `"` + strings.Replace(identifier, `"`, `""`, -1) + `"`
}

func quoteLiteral(literal string) string {
	return "'" + strings.Replace(literal, `'`, `''`, -1) + "'"
}

// UpdateUserPassword updates the user's password
func UpdateUserPassword(dbQuery dbType.DatabaseBackend) error {
	var log logr.Logger

	log = ctrl.Log.WithName("cassandra")

	cluster := gocql.NewCluster(dbQuery.Hosts...)
	if dbQuery.LoginPassword != "" {
		cluster.Authenticator = gocql.PasswordAuthenticator{
			Username: dbQuery.LoginUsername,
			Password: dbQuery.LoginPassword,
		}
	}
	if dbQuery.Port > 0 {
		cluster.Port = int(dbQuery.Port)
	}

	cluster.Consistency = gocql.Quorum
	session, err := cluster.CreateSession()
	if err != nil {
		log.Error(err, "Cannot create cassandra session")
		return err
	}
	defer session.Close()

	err = session.Query(fmt.Sprintf("ALTER ROLE %s WITH PASSWORD = %s",
		quoteIdentifier(dbQuery.Username),
		quoteLiteral(dbQuery.Password))).Exec()
	if err != nil {
		log.Error(err, "Failed to rotate secret in backend Cassandra")
		return err
	}

	log.Info("Cassandra password updated successfully")
	return nil
}
