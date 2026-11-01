package engine

import (
	"strings"
	"testing"
)

func TestDriverDialect(t *testing.T) {
	for driver, want := range map[string]Dialect{"postgres": Postgres, "pgx": Postgres, "mysql": MySQL, "sqlite": SQLite, "sqlite3": SQLite} {
		if d, ok := driverDialect(driver); !ok || d != want {
			t.Errorf("%s: %v, %v; want %v", driver, d, ok, want)
		}
	}
	if _, ok := driverDialect("oracle"); ok {
		t.Error("oracle has a dialect")
	}
}

func TestCheckMySQLDSN(t *testing.T) {
	const pw = "hunter2"
	for _, c := range []struct{ dsn, missing string }{
		{"u:" + pw + "@tcp(h)/d?parseTime=true&clientFoundRows=true", ""},
		{"u:" + pw + "@tcp(h)/d?parseTime=1&clientFoundRows=1", ""},
		{"u:" + pw + "@tcp(h)/d", "parseTime"},
		{"u:" + pw + "@tcp(h)/d?clientFoundRows=true", "parseTime"},
		{"u:" + pw + "@tcp(h)/d?parseTime=false&clientFoundRows=true", "parseTime"},
		{"u:" + pw + "@tcp(h)/d?parseTime=true", "clientFoundRows"},
		{"u:" + pw + "?parseTime=true&clientFoundRows=true@tcp(h)/d", "parseTime"},
		{"u:" + pw + "/?x=1@tcp(h)/d?parseTime=true&clientFoundRows=true", ""},
	} {
		err := checkMySQLDSN(c.dsn)
		switch {
		case c.missing == "" && err != nil:
			t.Errorf("%s: %v", c.dsn, err)
		case c.missing != "" && (err == nil || !strings.Contains(err.Error(), c.missing)):
			t.Errorf("%s: %v; want it to name %s", c.dsn, err, c.missing)
		case err != nil && strings.Contains(err.Error(), pw):
			t.Errorf("the error shows the password: %v", err)
		}
	}
}
