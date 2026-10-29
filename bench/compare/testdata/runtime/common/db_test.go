package bench

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

// dbDialect is $COMPARE_DIALECT: sqlite (the default) or postgres.
func dbDialect() string {
	if d := os.Getenv("COMPARE_DIALECT"); d != "" {
		return d
	}
	return "sqlite"
}

// openDB opens an empty database for this ORM, with one connection, closed
// when tb ends. On sqlite it is a new file; on postgres it is the schema
// cmp_<ormName>, dropped and created again, of the database
// $VELOX_COMPARE_POSTGRES.
func openDB(tb testing.TB) *sql.DB {
	tb.Helper()
	var db *sql.DB
	switch d := dbDialect(); d {
	case "sqlite":
		var err error
		db, err = sql.Open("sqlite", filepath.Join(tb.TempDir(), "b.db")+"?_pragma=foreign_keys(1)")
		if err != nil {
			tb.Fatal(err)
		}
	case "postgres":
		dsn := os.Getenv("VELOX_COMPARE_POSTGRES")
		if dsn == "" {
			tb.Fatal("VELOX_COMPARE_POSTGRES is unset")
		}
		schema := "cmp_" + ormName
		admin, err := sql.Open("pgx", dsn)
		if err != nil {
			tb.Fatal(err)
		}
		_, err = admin.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE; CREATE SCHEMA " + schema)
		if cerr := admin.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			tb.Fatal(err)
		}
		sep := "?"
		if strings.Contains(dsn, "?") {
			sep = "&"
		}
		if db, err = sql.Open("pgx", dsn+sep+"search_path="+schema); err != nil {
			tb.Fatal(err)
		}
	default:
		tb.Fatalf("COMPARE_DIALECT is %q, want sqlite or postgres", d)
	}
	db.SetMaxOpenConns(1)
	tb.Cleanup(func() { _ = db.Close() })
	return db
}

// prevIDIndexed reports whether e1s has an index on prev_id.
func prevIDIndexed(tb testing.TB, db *sql.DB) bool {
	tb.Helper()
	q := `SELECT count(*) FROM sqlite_master WHERE type = 'index' AND tbl_name = 'e1s' AND sql LIKE '%prev_id%'`
	if dbDialect() == "postgres" {
		q = `SELECT count(*) FROM pg_indexes WHERE schemaname = current_schema() AND tablename = 'e1s' AND indexdef LIKE '%(prev_id)%'`
	}
	var n int
	if err := db.QueryRow(q).Scan(&n); err != nil {
		tb.Fatal(err)
	}
	return n > 0
}
