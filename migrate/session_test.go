package migrate_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/migrate"
)

// TestCheckVersion: each dialect's minimum, and MariaDB refused.
func TestCheckVersion(t *testing.T) {
	for _, c := range []struct {
		d  velox.Dialect
		v  string
		ok bool
	}{
		{velox.Postgres, "110000", false},
		{velox.Postgres, "120000", true},
		{velox.Postgres, "160004", true},
		{velox.Postgres, "sixteen", false},
		{velox.MySQL, "8.0.18", false},
		{velox.MySQL, "8.0.19", true},
		{velox.MySQL, "8.4.3", true},
		{velox.MySQL, "8.0.36-log", true},
		{velox.MySQL, "5.7.44", false},
		{velox.MySQL, "10.11.2-MariaDB", false},
		{velox.MySQL, "eight", false},
		{velox.SQLite, "3.34.1", false},
		{velox.SQLite, "3.35.0", true},
		{velox.SQLite, "3.46.1", true},
	} {
		err := migrate.CheckVersion(c.d, c.v)
		if (err == nil) != c.ok {
			t.Errorf("%v %s: %v; want ok %t", c.d, c.v, err, c.ok)
		}
		var e *migrate.Error
		if err != nil && (!errors.As(err, &e) || e.Code != "session") {
			t.Errorf("%v %s: %#v; want Code session", c.d, c.v, err)
		}
	}
}

// TestCheckVersionText: the refusal names the version as PostgreSQL does.
func TestCheckVersionText(t *testing.T) {
	for v, want := range map[string]string{"90624": "PostgreSQL 9.6;", "110005": "PostgreSQL 11;"} {
		if err := migrate.CheckVersion(velox.Postgres, v); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v; want %q", v, err, want)
		}
	}
}

func TestLockName(t *testing.T) {
	if got, want := migrate.LockName("velox", "velox_migrations"), "velox_migrate_28873223e55a53b9"; got != want {
		t.Errorf("LockName = %s; want %s", got, want)
	}
}

func TestLockKey(t *testing.T) {
	if got, want := migrate.LockKey("velox_migrations"), int64(-6634131438283035561); got != want {
		t.Errorf("LockKey = %d; want %d", got, want)
	}
}
