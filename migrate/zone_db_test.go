package migrate_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

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
		mustExec(t, db, `ALTER DATABASE "`+name+`" SET TimeZone = 'Asia/Hong_Kong'`)
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
		pid               string // the session's own number
	}{
		{velox.Postgres, "timestamp with time zone", `CREATE TABLE zone AS SELECT current_setting('TimeZone') AS tz;`, `SELECT current_setting('TimeZone')`, "UTC", `SELECT pg_backend_pid()`},
		{velox.MySQL, "timestamp(6)", "CREATE TABLE zone AS SELECT @@session.time_zone AS tz;", "SELECT @@session.time_zone", "+00:00", `SELECT CONNECTION_ID()`},
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
			// One connection: the run takes the pool's own, and must not give
			// it back in UTC.
			db.SetMaxOpenConns(1)
			var before, after int64
			if err := db.QueryRow(c.pid).Scan(&before); err != nil {
				t.Fatal(err)
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
			// The run's connection was discarded, not pooled in UTC.
			if err := db.QueryRow(c.pid).Scan(&after); err != nil || after == before {
				t.Errorf("the pool's connection %d after the run, %d before; %v", after, before, err)
			}
			if err := db.QueryRow(c.query).Scan(&tz); err != nil || tz == c.zone {
				t.Errorf("the pool's zone %q, %v", tz, err)
			}
		})
	}
}

// TestUpSessionPerFile: each file starts with the session as Up opened it,
// whatever the file before it set: the zone, PostgreSQL's search_path,
// MySQL's sql_mode.
func TestUpSessionPerFile(t *testing.T) {
	for _, c := range []struct {
		d            velox.Dialect
		first, probe string
		query        string
		want         string
	}{
		{velox.Postgres, "CREATE SCHEMA other;\nSET search_path = other, public;\nSET TimeZone = 'Asia/Tokyo';\nSET lock_timeout = '1s';\n",
			"CREATE TABLE probe AS SELECT current_setting('TimeZone') || ' ' || current_schema() || ' ' || current_setting('lock_timeout') AS v;\n",
			`SELECT v FROM public.probe`, "UTC public 5s"},
		{velox.MySQL, "SET SESSION sql_mode = 'ANSI_QUOTES';\nSET time_zone = '+09:00';\n",
			"CREATE TABLE probe (v varchar(200) DEFAULT \"x\");\nINSERT INTO probe (v) SELECT CONCAT(@@session.time_zone, ' ', @@session.sql_mode LIKE '%ANSI_QUOTES%');\n",
			`SELECT v FROM probe`, "+00:00 0"},
	} {
		t.Run(c.d.String(), func(t *testing.T) {
			db := zoned(t, c.d)
			mustUp(t, db, dirOf(t, c.d, files("0001_set.sql", c.first, "0002_probe.sql", c.probe)))
			var got string
			if err := db.QueryRow(c.query).Scan(&got); err != nil || got != c.want {
				t.Errorf("%q, %v; want %q", got, err, c.want)
			}
		})
	}
}

// TestUpPostgresSessionGiven: the settings the session had when Up took it,
// as an AfterConnect hook makes them (a role, a search_path,
// standard_conforming_strings over the database's default), hold for every
// file, and what a file sets does not reach the next.
func TestUpPostgresSessionGiven(t *testing.T) {
	base := os.Getenv("VELOX_TEST_POSTGRES")
	if base == "" {
		t.Skip("needs VELOX_TEST_POSTGRES")
	}
	admin := openDB(t, "pgx", base)
	role := fmt.Sprintf("velox_owner_%d_%d", os.Getpid(), time.Now().UnixNano())
	mustExec(t, admin, `CREATE ROLE "`+role+`"`)
	t.Cleanup(func() { _, _ = admin.Exec(`DROP ROLE IF EXISTS "` + role + `"`) }) // after the database is dropped
	setup, dsn := freshDB(t, velox.Postgres)
	var name string
	if err := setup.QueryRow(`SELECT current_database()`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`ALTER DATABASE "` + name + `" SET standard_conforming_strings = off`,
		`CREATE SCHEMA app`, `CREATE SCHEMA other`,
		`GRANT ALL ON SCHEMA app, other TO "` + role + `"`,
	} {
		mustExec(t, setup, q)
	}
	db := openDB(t, "pgx", dsn) // its sessions start with the database's settings
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	ctx := context.Background()
	conn, err := db.Conn(ctx) // as an AfterConnect hook would
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`SET standard_conforming_strings = on`, `SET search_path TO app`, `SET ROLE "` + role + `"`} {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	_ = conn.Close() // back to the pool, for Up to take
	mustUp(t, db, dirOf(t, velox.Postgres, files(
		"0001_set.sql", "SET search_path TO other, app;\nSET standard_conforming_strings = off;\nRESET ROLE;\n",
		"0002_probe.sql", "CREATE TABLE probe AS SELECT current_user::text || ' ' || current_schema() || ' ' || current_setting('standard_conforming_strings') AS v;\n")))
	var got string
	if err := db.QueryRow(`SELECT v FROM app.probe`).Scan(&got); err != nil || got != role+" app on" {
		t.Errorf("%q, %v; want %q", got, err, role+" app on")
	}
}
