package mysql

import (
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/go-sql-driver/mysql"

	dbType "digitalis.io/vals-operator/db/types"
	ctrl "sigs.k8s.io/controller-runtime"
)

func quoteLiteralMysql(literal string) string {
	return "'" + strings.Replace(strings.Replace(literal, "'", "''", -1), "\\", "\\\\", -1) + "'"
}

func runMysqlQuery(dbQuery dbType.DatabaseQuery, host string) error {
	mysqlconn := fmt.Sprintf("%s:%s@tcp(%s:%d)/mysql?tls=preferred",
		dbQuery.LoginUsername, dbQuery.LoginPassword, host, dbQuery.Port)

	db, err := sql.Open("mysql", mysqlconn)
	if err != nil {
		return err
	}
	defer db.Close()

	if dbQuery.UserHost == "" {
		dbQuery.UserHost = "%"
	}

	_, err = db.Exec(fmt.Sprintf("ALTER USER %s@%s IDENTIFIED BY %s",
		quoteLiteralMysql(dbQuery.Username),
		quoteLiteralMysql(dbQuery.UserHost),
		quoteLiteralMysql(dbQuery.Password)))
	if err != nil {
		return err
	}

	_, err = db.Exec("FLUSH PRIVILEGES")
	if err != nil {
		return err
	}

	return nil
}

func UpdateUserPassword(dbQuery dbType.DatabaseQuery) error {
	log := ctrl.Log.WithName("mysql")

	/* Default user */
	if dbQuery.LoginUsername == "" {
		dbQuery.LoginUsername = "root"
	}

	if dbQuery.Port < 1 {
		dbQuery.Port = 3306
	}

	var err error
	for _, host := range dbQuery.Hosts {
		err = runMysqlQuery(dbQuery, host)
		if err != nil {
			log.Error(err, fmt.Sprintf("Cannot run query on host %s", host))
		} else {
			log.Info(fmt.Sprintf("Query successful on host %s", host))
			log.Info("MySQL password updated successfully")
			return nil
		}
	}

	log.Error(err, "Password not updated")

	return err
}
