package migrate_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/go-sql-driver/mysql"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/migrate"
)

// zoned is a fresh database of d whose new sessions start in UTC+8.
func zoned(t *testing.T, d velox.Dialect) *sql.DB {
	t.Helper()
	db, dsn := freshDB(t, d)
	switch d {
	case velox.Postgres:
		var name string
		if err := db.QueryRow(`SELECT current_database()`).Scan(&name); err != nil {
			t.Fatal(err)
		}
		pgExec(t, db, `ALTER DATABASE "`+name+`" SET TimeZone = 'Asia/Hong_Kong'`)
		_ = db.Close()
		return openDB(t, "pgx", dsn)
	case velox.MySQL:
		cfg, err := mysql.ParseDSN(dsn)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Params == nil {
			cfg.Params = map[string]string{}
		}
		cfg.Params["time_zone"] = "'+08:00'"
		_ = db.Close()
		return openDB(t, "mysql", cfg.FormatDSN())
	}
	t.Fatalf("zoned: no dialect %v", d)
	return nil
}

// TestUpSessionInUTC: a file runs with its session in UTC whatever the
// server's or the connection's zone, so a timestamp literal default means
// what it says and Inspect, which reads in UTC, finds no drift; the zone
// does not reach the pool.
func TestUpSessionInUTC(t *testing.T) {
	for _, c := range []struct {
		d                 velox.Dialect
		typ, probe, query string
		zone              string // the file's session's
	}{
		{velox.Postgres, "timestamp with time zone", `CREATE TABLE zone AS SELECT current_setting('TimeZone') AS tz;`, `SELECT current_setting('TimeZone')`, "UTC"},
		{velox.MySQL, "timestamp(6)", "CREATE TABLE zone AS SELECT @@session.time_zone AS tz;", "SELECT @@session.time_zone", "+00:00"},
	} {
		t.Run(c.d.String(), func(t *testing.T) {
			db := zoned(t, c.d)
			snap := &migrate.Schema{Dialect: c.d, Tables: []migrate.Table{{
				Name:    "ev",
				Columns: []migrate.Column{{Name: "at", Type: c.typ, Default: "'2020-01-01 10:00:00'"}},
			}}}
			if c.d == velox.MySQL {
				snap.Tables[0].Collation = "utf8mb4_bin"
			}
			mustUp(t, db, dirOf(t, c.d, step{to: snap, name: "ev"}, files("0002_zone.sql", c.probe+"\n")))
			var tz string
			if err := db.QueryRow(`SELECT tz FROM zone`).Scan(&tz); err != nil {
				t.Fatal(err)
			}
			if tz != c.zone {
				t.Errorf("the file's session zone %q, want %q", tz, c.zone)
			}
			r, err := migrate.Drift(context.Background(), db, snap)
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Changes) != 0 || len(r.Warnings) != 0 {
				t.Errorf("drift: %v, %v", r.Changes, r.Warnings)
			}
			// The pool's sessions keep their own zone.
			db.SetMaxOpenConns(1)
			if err := db.QueryRow(c.query).Scan(&tz); err != nil || tz == c.zone {
				t.Errorf("the pool's zone %q, %v", tz, err)
			}
		})
	}
}
