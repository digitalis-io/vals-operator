package database

import (
	"strings"
	"testing"

	dbType "digitalis.io/vals-operator/db/types"
)

func TestUpdateUserPasswordDispatchesClickhouse(t *testing.T) {
	err := UpdateUserPassword(dbType.DatabaseBackend{Driver: "clickhouse", Username: "alice"})
	if err == nil || !strings.Contains(err.Error(), "clickhouse") {
		t.Fatalf("expected the clickhouse driver to handle the query, got %v", err)
	}
}

func TestUpdateUserPasswordIgnoresUnknownDriver(t *testing.T) {
	if err := UpdateUserPassword(dbType.DatabaseBackend{Driver: "sqlite"}); err != nil {
		t.Fatalf("expected nil for an unsupported driver, got %v", err)
	}
}
