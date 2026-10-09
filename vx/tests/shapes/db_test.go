package shapes

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler"
	vgen "github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/dialect"
	velsql "github.com/syssam/velox/dialect/sql"
	"github.com/syssam/velox/dialect/sql/schema"
	"github.com/syssam/velox/vx/orm"

	_ "modernc.org/sqlite"
)

const dbPackage = "github.com/syssam/velox/vx/tests/shapes/db"

// graph is the schema graph of ./schema, loaded once. vx has no migration
// generator yet, so the tests create their tables from it with velox's
// migrator.
var graph *vgen.Graph

func TestMain(m *testing.M) {
	cfg, err := vgen.NewConfig(vgen.WithTarget("./db"), vgen.WithPackage(dbPackage))
	if err != nil {
		slog.Error("shapes: config", "error", err)
		os.Exit(1)
	}
	graph, err = compiler.LoadGraph("./schema", cfg)
	if err != nil {
		slog.Error("shapes: loading ./schema", "error", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// ddl says which CREATE TABLE statements a test database gets.
type ddl int

const (
	// ddlVelox is exactly what velox's own migration creates for the schema,
	// and what vx follows: the column of an Optional field that is not
	// Nillable is NOT NULL, and a create that does not set such a field
	// stores its zero value. It is the default of the tests.
	ddlVelox ddl = iota
	// ddlNullableOptional makes the column of every Optional field nullable.
	// It is for the tests that are about READING a NULL from such a column
	// (a database migrated by something else, a row written by raw SQL).
	ddlNullableOptional
)

var dbSeq atomic.Int64

// openDDL returns a vx handle over a fresh in-memory SQLite database with
// every table of the schema created. Every call gets its own database, so
// tests do not depend on each other or on -count.
func openDDL(t *testing.T, mode ddl) *orm.DB {
	t.Helper()
	use(t)
	name := strings.NewReplacer("/", "_", " ", "_", "#", "_").Replace(t.Name())
	dsn := fmt.Sprintf("file:%s_%d?mode=memory&cache=shared&_pragma=foreign_keys(1)", name, dbSeq.Add(1))
	drv, err := velsql.Open(dialect.SQLite, dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = drv.Close() })

	tables, err := graph.Tables()
	require.NoError(t, err)
	if mode == ddlNullableOptional {
		byName := make(map[string]*schema.Table, len(tables))
		for _, tbl := range tables {
			byName[tbl.Name] = tbl
		}
		for _, n := range graph.Nodes {
			tbl := byName[n.Table()]
			require.NotNil(t, tbl, "no table for %s", n.Name)
			for _, f := range n.Fields {
				if !f.Optional {
					continue
				}
				for _, c := range tbl.Columns {
					if c.Name == f.StorageKey() {
						c.Nullable = true
					}
				}
			}
		}
	}
	m, err := schema.NewMigrate(drv)
	require.NoError(t, err)
	require.NoError(t, m.Create(context.Background(), tables...))
	return orm.OpenDB(dialect.SQLite, drv.DB())
}

// open is openDDL with velox's own tables.
func open(t *testing.T) *orm.DB {
	t.Helper()
	return openDDL(t, ddlVelox)
}

// cur is the test whose database was opened last. The tests of this package
// do not run in parallel, so it is the running test (a subtest that wants its
// own failures calls use).
var cur atomic.Pointer[testing.T]

// use makes t the test must reports to.
func use(t *testing.T) { cur.Store(t) }

// must fails the running test on err and passes v through:
// must(q.Only(ctx, db)). A generic function cannot take t next to a
// (value, error) pair, hence cur.
func must[T any](v T, err error) T {
	t := cur.Load()
	t.Helper()
	require.NoError(t, err)
	return v
}

// ptr returns a pointer to v.
func ptr[T any](v T) *T { return &v }

// rawExec runs a statement on the database behind db, for fixtures the ORM
// cannot or must not write (a NULL in a column it would not leave NULL).
func rawExec(t *testing.T, db *orm.DB, query string, args ...any) {
	t.Helper()
	_, err := db.SQL().ExecContext(context.Background(), query, args...)
	require.NoError(t, err)
}

// rawValue reads one value straight from the database.
func rawValue[T any](t *testing.T, db *orm.DB, query string, args ...any) T {
	t.Helper()
	var v T
	require.NoError(t, db.SQL().QueryRowContext(context.Background(), query, args...).Scan(&v))
	return v
}
