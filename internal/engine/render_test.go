package engine

import (
	"testing"
)

func TestDialectString(t *testing.T) {
	if Postgres.String() != "postgres" || MySQL.String() != "mysql" || SQLite.String() != "sqlite" {
		t.Error(Postgres, MySQL, SQLite)
	}
}
