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

	"github.com/syssam/velox"
	"github.com/syssam/velox/compiler"
	vgen "github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/dialect"
	velsql "github.com/syssam/velox/dialect/sql"
	"github.com/syssam/velox/dialect/sql/schema"

	_ "modernc.org/sqlite"
)

const dbPackage = "github.com/syssam/velox/tests/orm/shapes/velox"

// graph is the schema graph of ./schema, loaded once. The generated code has no migration
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
	// and what the runtime follows: the column of an Optional field that is not
	// Nillable is NOT NULL, and a create that does not set such a field
	// stores its zero value. It is the default of the tests.
	ddlVelox ddl = iota
	// ddlNullableOptional makes the column of every Optional field nullable.
	// It is for the tests that are about READING a NULL from such a column
	// (a database migrated by something else, a row written by raw SQL).
	ddlNullableOptional
)

var dbSeq atomic.Int64

// openDDL returns a handle over a fresh in-memory SQLite database with
// every table of the schema created. Every call gets its own database, so
// tests do not depend on each other or on -count.
func openDDL(t *testing.T, mode ddl) *velox.DB {
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
	return velox.OpenDB(dialect.SQLite, drv.DB())
}

// open is openDDL with velox's own tables.
func open(t *testing.T) *velox.DB {
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
func rawExec(t *testing.T, db *velox.DB, query string, args ...any) {
	t.Helper()
	_, err := velox.ExecSQL(context.Background(), db, query, args...)
	require.NoError(t, err)
}

// rawValue reads one value straight from the database.
func rawValue[T any](t *testing.T, db *velox.DB, query string, args ...any) T {
	t.Helper()
	var v T
	rows, err := velox.QuerySQL(context.Background(), db, query, args...)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	require.True(t, rows.Next(), "no row: %s", query)
	require.NoError(t, rows.Scan(&v))
	require.NoError(t, rows.Err())
	return v
}
