package veloxtest_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/dbenv"
	"github.com/syssam/velox/internal/testmodel"
	"github.com/syssam/velox/veloxtest"
)

// foreignKeys counts the foreign keys of db's tables.
func foreignKeys(t *testing.T, db *velox.DB) int {
	t.Helper()
	q := map[velox.Dialect]string{
		velox.SQLite:   "SELECT count(*) FROM sqlite_master m, pragma_foreign_key_list(m.name) WHERE m.type = 'table'",
		velox.Postgres: "SELECT count(*) FROM information_schema.table_constraints WHERE constraint_type = 'FOREIGN KEY' AND table_schema = current_schema()",
		velox.MySQL:    "SELECT count(*) FROM information_schema.table_constraints WHERE constraint_type = 'FOREIGN KEY' AND table_schema = DATABASE()",
	}[db.Dialect()]
	var n int
	if err := velox.Pool(db).QueryRow(q).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestOpenWithoutForeignKeys: on SQLite, WithoutForeignKeys makes the
// tables without a foreign key, so a line may name a sample that does not
// exist, and the copies of the tables with and without keys are kept apart,
// whichever Open comes first.
func TestOpenWithoutForeignKeys(t *testing.T) {
	ctx := context.Background()
	orphan := func(db *velox.DB) error { return db.Insert(&testmodel.Line{SampleID: 999}).Exec(ctx) }
	for range 2 {
		off := veloxtest.Open(t, "sqlite", "", schemas, veloxtest.WithoutForeignKeys())
		if n := foreignKeys(t, off); n != 0 {
			t.Errorf("WithoutForeignKeys: %d foreign keys", n)
		}
		if err := orphan(off); err != nil {
			t.Errorf("WithoutForeignKeys: a line of no sample: %v", err)
		}
		on := veloxtest.Open(t, "sqlite", "", schemas)
		if n := foreignKeys(t, on); n == 0 {
			t.Error("the default has no foreign key")
		}
		if err := orphan(on); err == nil {
			t.Error("the default stored a line of no sample")
		}
	}
	// WithOptions reaches velox.Connect.
	db := veloxtest.Open(t, "sqlite", "", schemas, veloxtest.WithOptions(velox.WithMaxLimit(3)), veloxtest.WithoutForeignKeys())
	if _, err := db.Query[testmodel.Sample]().Limit(4).All(ctx); err == nil {
		t.Error("WithOptions(WithMaxLimit(3)) let a Limit of 4 through")
	}
}

// TestOpenWithoutForeignKeysServers: on PostgreSQL and MySQL, each when its
// variable is set, WithoutForeignKeys makes the tables without a foreign
// key in a new database, and the default makes them with.
func TestOpenWithoutForeignKeysServers(t *testing.T) {
	for _, c := range []struct {
		d        velox.Dialect
		variable string
	}{{velox.Postgres, "VELOX_TEST_POSTGRES"}, {velox.MySQL, "VELOX_TEST_MYSQL_ADMIN"}} {
		t.Run(c.d.String(), func(t *testing.T) {
			base := dbenv.Need(t, c.variable)
			for _, noFKs := range []bool{true, false} {
				driver, dsn := serverDatabase(t, c.d, base)
				var opts []veloxtest.Option
				if noFKs {
					opts = append(opts, veloxtest.WithoutForeignKeys())
				}
				db := veloxtest.Open(t, driver, dsn, schemas, opts...)
				if n := foreignKeys(t, db); (n == 0) != noFKs {
					t.Errorf("WithoutForeignKeys %t: %d foreign keys", noFKs, n)
				}
			}
		})
	}
}

// serverDatabase is a new database beside base's, of dialect d, dropped
// when t ends: its driver and DSN.
func serverDatabase(t *testing.T, d velox.Dialect, base string) (string, string) {
	t.Helper()
	driver := map[velox.Dialect]string{velox.Postgres: "pgx", velox.MySQL: "mysql"}[d]
	admin, err := sql.Open(driver, base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	name := fmt.Sprintf("veloxtest_fk_%d_%d", os.Getpid(), time.Now().UnixNano())
	q := map[velox.Dialect]string{velox.Postgres: `"`, velox.MySQL: "`"}[d]
	if _, err := admin.Exec("CREATE DATABASE " + q + name + q); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		drop := "DROP DATABASE IF EXISTS " + q + name + q
		if d == velox.Postgres {
			drop += " WITH (FORCE)"
		}
		if _, err := admin.Exec(drop); err != nil {
			t.Errorf("%s: %v", drop, err)
		}
	})
	if d == velox.MySQL {
		cfg, err := mysql.ParseDSN(base)
		if err != nil {
			t.Fatal(err)
		}
		cfg.DBName = name
		return driver, cfg.FormatDSN()
	}
	rest, query, _ := strings.Cut(base, "?")
	if i := strings.LastIndex(rest, "/"); i > len("postgres://") {
		rest = rest[:i]
	}
	if query != "" {
		return driver, rest + "/" + name + "?" + query
	}
	return driver, rest + "/" + name
}
