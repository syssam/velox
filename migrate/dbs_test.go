package migrate_test

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"

	"github.com/syssam/velox"
)

// freshDB is a new, empty database of dialect d, closed and dropped when t
// ends: a file in t.TempDir() on SQLite, with foreign keys on; on PostgreSQL
// a database beside VELOX_TEST_POSTGRES's; on MySQL one made through
// VELOX_TEST_MYSQL_ADMIN. It skips t when the dialect's variable is unset.
func freshDB(t *testing.T, d velox.Dialect) (db *sql.DB, dsn string) {
	t.Helper()
	driver := "sqlite"
	switch d {
	case velox.SQLite:
		dsn = filepath.Join(t.TempDir(), "velox.db") + "?_pragma=foreign_keys(1)"
	case velox.Postgres:
		base := os.Getenv("VELOX_TEST_POSTGRES")
		if base == "" {
			t.Skip("needs VELOX_TEST_POSTGRES")
		}
		admin := openDB(t, "pgx", base)
		driver, dsn = "pgx", dsnWithDatabase(base, newDatabase(t, admin, `"`))
	case velox.MySQL:
		base := os.Getenv("VELOX_TEST_MYSQL_ADMIN")
		if base == "" {
			t.Skip("needs VELOX_TEST_MYSQL_ADMIN")
		}
		admin := openDB(t, "mysql", base)
		cfg, err := mysql.ParseDSN(base)
		if err != nil {
			t.Fatal(err)
		}
		cfg.DBName = newDatabase(t, admin, "`")
		driver, dsn = "mysql", cfg.FormatDSN()
	default:
		t.Fatalf("freshDB: no dialect %v", d)
	}
	return openDB(t, driver, dsn), dsn
}

// openDB opens dsn, closed when t ends.
func openDB(t *testing.T, driver, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open(driver, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// databases counts the databases newDatabase made, for a name no other
// test makes: two parallel tests can read the same clock.
var databases struct {
	sync.Mutex
	n int
}

// newDatabase makes a database through admin, whose names are quoted with
// q, and drops it when t ends. It returns its name.
func newDatabase(t *testing.T, admin *sql.DB, q string) string {
	t.Helper()
	databases.Lock()
	databases.n++
	n := databases.n
	databases.Unlock()
	name := fmt.Sprintf("velox_migrate_%d_%d_%d", os.Getpid(), time.Now().UnixNano(), n)
	mustExec(t, admin, "CREATE DATABASE "+q+name+q)
	t.Cleanup(func() {
		drop := "DROP DATABASE IF EXISTS " + q + name + q
		if q == `"` {
			drop += " WITH (FORCE)" // a test's other sessions may be open still
		}
		if _, err := admin.Exec(drop); err != nil {
			t.Errorf("%s: %v", drop, err)
		}
	})
	return name
}

// pgDatabase is a new database beside dsn's, dropped when t ends, with one
// connection.
func pgDatabase(t *testing.T, admin *sql.DB, dsn string) *sql.DB {
	t.Helper()
	db := openDB(t, "pgx", dsnWithDatabase(dsn, newDatabase(t, admin, `"`)))
	db.SetMaxOpenConns(1)
	return db
}

// dsnWithDatabase is the URL dsn with its database replaced by name.
func dsnWithDatabase(dsn, name string) string {
	rest, query, _ := strings.Cut(dsn, "?")
	if i := strings.LastIndex(rest, "/"); i > len("postgres://") {
		rest = rest[:i]
	}
	if query != "" {
		return rest + "/" + name + "?" + query
	}
	return rest + "/" + name
}

// mustExec runs q on db, of any dialect, and fails the test on an error.
func mustExec(t *testing.T, db *sql.DB, q string) {
	t.Helper()
	if _, err := db.Exec(q); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}
