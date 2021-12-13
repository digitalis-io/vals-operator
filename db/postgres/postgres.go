package postgres

import (
	"database/sql"
	"fmt"

	"github.com/lib/pq"

	dbType "digitalis.io/vals-operator/db/types"
	ctrl "sigs.k8s.io/controller-runtime"
)

func runPostgresQuery(dbQuery dbType.DatabaseQuery, host string) error {
	psqlconn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s connect_timeout=5 sslmode=disable",
		host, dbQuery.Port, dbQuery.LoginUsername, dbQuery.LoginPassword, "postgres")

	db, err := sql.Open("postgres", psqlconn)
	if err != nil {
		return err
	}
	defer db.Close()

	_, err = db.Exec(fmt.Sprintf("ALTER ROLE %s WITH PASSWORD %s",
		pq.QuoteIdentifier(dbQuery.Username), pq.QuoteLiteral(dbQuery.Password)))
	if err != nil {
		return err
	}

	return nil
}

func UpdateUserPassword(dbQuery dbType.DatabaseQuery) error {
	log := ctrl.Log.WithName("postgres")

	/* Default user */
	if dbQuery.LoginUsername == "" {
		dbQuery.LoginUsername = "postgres"
	}

	if dbQuery.Port < 1 {
		dbQuery.Port = 5432
	}

	var err error
	for _, host := range dbQuery.Hosts {
		err = runPostgresQuery(dbQuery, host)
		if err != nil {
			log.Error(err, fmt.Sprintf("Cannot run query on host %s", host))
		} else {
			log.Info(fmt.Sprintf("Query successful on host %s", host))
			log.Info("Postgres password updated successfully")
			return nil
		}
	}

	log.Error(err, "Password not updated")

	return err
}
