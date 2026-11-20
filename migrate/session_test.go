package migrate_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/migrate"
)

// TestCheckVersion: each dialect's minimum, and MariaDB refused.
func TestCheckVersion(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		d  velox.Dialect
		v  string
		ok bool
	}{
		{velox.Postgres, "130000", false},
		{velox.Postgres, "140000", true},
		{velox.Postgres, "180001", true},
		{velox.Postgres, "160004", true},
		{velox.Postgres, "sixteen", false},
		{velox.MySQL, "8.0.19", false},
		{velox.MySQL, "8.0.36-log", false},
		{velox.MySQL, "8.3.0", false},
		{velox.MySQL, "8.4.0", true},
		{velox.MySQL, "8.4.3", true},
		{velox.MySQL, "9.1.0", true},
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
		if e, ok := errors.AsType[*migrate.Error](err); err != nil && (!ok || e.Code != "session") {
			t.Errorf("%v %s: %#v; want Code session", c.d, c.v, err)
		}
	}
}

// TestCheckVersionText: the refusal names the version as PostgreSQL does.
func TestCheckVersionText(t *testing.T) {
	t.Parallel()
	for v, want := range map[string]string{"90624": "PostgreSQL 9.6;", "110005": "PostgreSQL 11;", "130000": "PostgreSQL 13;"} {
		if err := migrate.CheckVersion(velox.Postgres, v); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v; want %q", v, err, want)
		}
	}
}

// TestCheckVersionFloors: the refusal names the minimum.
func TestCheckVersionFloors(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		d    velox.Dialect
		v    string
		want string
	}{
		{velox.MySQL, "8.0.36", "needs 8.4 or later"},
		{velox.Postgres, "130000", "needs 14 or later"},
	} {
		if err := migrate.CheckVersion(c.d, c.v); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v %s: %v; want %q", c.d, c.v, err, c.want)
		}
	}
}

func TestLockName(t *testing.T) {
	t.Parallel()
	if got, want := migrate.LockName("velox", "velox_migrations"), "velox_migrate_28873223e55a53b9"; got != want {
		t.Errorf("LockName = %s; want %s", got, want)
	}
}

func TestLockKey(t *testing.T) {
	t.Parallel()
	if got, want := migrate.LockKey("public", "velox_migrations"), int64(-4521918464851113452); got != want {
		t.Errorf("LockKey = %d; want %d", got, want)
	}
}
