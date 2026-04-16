package runtime

// Tests for ambiguous-column-reference prevention in BuildSelectorFrom.
//
// The bug: BuildSelectorFrom was calling selector.Select(columns...) with raw
// unqualified column names. When a M2O (FromEdgeOwner) edge traversal produces
// a JOIN where both the main table and the subquery share a column name (e.g.
// "terms_id" is both the FK on sales_order AND the PK of terms), the outer
// SELECT became ambiguous and the DB rejected the query.
//
// The fix: selector.Select(selector.Columns(columns...)...) — uses .C() per
// column to add the table qualifier.
//
// This file tests:
//   - SQL string qualification (unit, all edge shapes)
//   - Actual `sqlgraph.Neighbors` / `sqlgraph.SetNeighbors` call in the path
//   - Real SQLite execution that would error before the fix

import (
	"context"
	stdsql "database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect"
	"github.com/syssam/velox/dialect/sql"
	"github.com/syssam/velox/dialect/sql/sqlgraph"

	_ "modernc.org/sqlite"
)

// openSQLite opens a real in-memory SQLite DB wrapped in a velox Driver.
func openSQLite(t *testing.T) *sql.Driver {
	t.Helper()
	db, err := stdsql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	return sql.OpenDB(dialect.SQLite, db)
}

// execAll runs each statement against the DB and fails the test on any error.
func execAll(t *testing.T, drv *sql.Driver, stmts ...string) {
	t.Helper()
	for _, s := range stmts {
		require.NoError(t, drv.Exec(context.Background(), s, []any{}, nil))
	}
}

// -------------------------------------------------------------------
// SQL-string tests: verify BuildSelectorFrom qualifies columns for all
// edge shapes that produce JOINs when used via sqlgraph.Neighbors or
// sqlgraph.SetNeighbors.
// -------------------------------------------------------------------

// TestBuildSelectorFrom_Neighbors_M2O uses the actual sqlgraph.Neighbors call
// (not a hand-crafted selector) to mirror exactly what generated QueryXxx
// methods inject as q.path for a M2O (FromEdgeOwner) edge.
func TestBuildSelectorFrom_Neighbors_M2O(t *testing.T) {
	// sales_order.terms_id  →  terms.terms_id (FK name = target PK name).
	step := sqlgraph.NewStep(
		sqlgraph.From("sales_order", "id", 42),
		sqlgraph.To("terms", "terms_id"),
		sqlgraph.Edge(sqlgraph.M2O, true, "sales_order", "terms_id"),
	)

	drv := &mockDriver{dialectName: dialect.SQLite}
	base := NewQueryBase(drv, "terms", []string{"terms_id", "name"}, "terms_id", nil, "Terms")
	base.Path = func(_ context.Context) (*sql.Selector, error) {
		return sqlgraph.Neighbors(dialect.SQLite, step), nil
	}

	sel, err := BuildSelectorFrom(context.Background(), base)
	require.NoError(t, err)
	query, _ := sel.Query()

	assert.Contains(t, query, "`terms`.`terms_id`", "terms_id must be table-qualified")
	assert.Contains(t, query, "`terms`.`name`", "name must be table-qualified")
}

// TestBuildSelectorFrom_Neighbors_O2OInverse uses the actual sqlgraph.Neighbors
// for a O2O inverse edge (also FromEdgeOwner), e.g. cards.owner_id → users.id
// where owner_id happens to match the source PK name.
func TestBuildSelectorFrom_Neighbors_O2OInverse(t *testing.T) {
	// nodes.prev_id  →  nodes.id (self-referential, FK = "prev_id", target PK = "id").
	// Not a collision, but verifies the path is exercised.
	// Use a case where FK name = target PK:  child.parent_id → parent.parent_id.
	step := sqlgraph.NewStep(
		sqlgraph.From("child", "id", 7),
		sqlgraph.To("parent", "parent_id"),
		sqlgraph.Edge(sqlgraph.O2O, true, "child", "parent_id"),
	)

	drv := &mockDriver{dialectName: dialect.SQLite}
	base := NewQueryBase(drv, "parent", []string{"parent_id", "label"}, "parent_id", nil, "Parent")
	base.Path = func(_ context.Context) (*sql.Selector, error) {
		return sqlgraph.Neighbors(dialect.SQLite, step), nil
	}

	sel, err := BuildSelectorFrom(context.Background(), base)
	require.NoError(t, err)
	query, _ := sel.Query()

	assert.Contains(t, query, "`parent`.`parent_id`")
	assert.Contains(t, query, "`parent`.`label`")
}

// TestBuildSelectorFrom_SetNeighbors_M2O uses sqlgraph.SetNeighbors with a
// set-based parent selector for the M2O (FromEdgeOwner) path. This mirrors
// what generated QueryXxx on a query builder (not an entity) sets as path.
func TestBuildSelectorFrom_SetNeighbors_M2O(t *testing.T) {
	// Build a parent selector that acts as the "from" set.
	parentSel := sql.Dialect(dialect.SQLite).
		Select().
		From(sql.Table("sales_order")).
		Where(sql.InValues("id", 1, 2, 3))

	step := sqlgraph.NewStep(
		sqlgraph.From("sales_order", "id"),
		sqlgraph.To("terms", "terms_id"),
		sqlgraph.Edge(sqlgraph.M2O, true, "sales_order", "terms_id"),
	)
	step.From.V = parentSel

	drv := &mockDriver{dialectName: dialect.SQLite}
	base := NewQueryBase(drv, "terms", []string{"terms_id", "name"}, "terms_id", nil, "Terms")
	base.Path = func(_ context.Context) (*sql.Selector, error) {
		return sqlgraph.SetNeighbors(dialect.SQLite, step), nil
	}

	sel, err := BuildSelectorFrom(context.Background(), base)
	require.NoError(t, err)
	query, _ := sel.Query()

	assert.Contains(t, query, "`terms`.`terms_id`")
	assert.Contains(t, query, "`terms`.`name`")
}

// TestBuildSelectorFrom_SetNeighbors_O2M uses sqlgraph.SetNeighbors for a O2M
// (ToEdgeOwner) path. In this shape the JOIN is (target FK col) vs parent id,
// so the risk column is the FK on the target table (e.g. "user_id"). Verify
// the target-table columns are qualified.
func TestBuildSelectorFrom_SetNeighbors_O2M(t *testing.T) {
	parentSel := sql.Dialect(dialect.SQLite).
		Select().
		From(sql.Table("users")).
		Where(sql.InValues("id", 1, 2))

	step := sqlgraph.NewStep(
		sqlgraph.From("users", "id"),
		sqlgraph.To("posts", "id"),
		sqlgraph.Edge(sqlgraph.O2M, false, "posts", "user_id"),
	)
	step.From.V = parentSel

	drv := &mockDriver{dialectName: dialect.SQLite}
	base := NewQueryBase(drv, "posts", []string{"id", "title", "user_id"}, "id", nil, "Post")
	base.Path = func(_ context.Context) (*sql.Selector, error) {
		return sqlgraph.SetNeighbors(dialect.SQLite, step), nil
	}

	sel, err := BuildSelectorFrom(context.Background(), base)
	require.NoError(t, err)
	query, _ := sel.Query()

	assert.Contains(t, query, "`posts`.`id`")
	assert.Contains(t, query, "`posts`.`title`")
	assert.Contains(t, query, "`posts`.`user_id`")
}

// -------------------------------------------------------------------
// Live DB tests: execute against real SQLite so an ambiguous column
// reference would surface as a DB error, not just a string mismatch.
// -------------------------------------------------------------------

// TestLiveDB_M2O_FKNameEqualsTargetPK is the definitive regression test.
// It creates two tables where the FK column on the source table has the same
// name as the PK of the target table, then executes a real M2O edge traversal.
// Before the fix this returned "SQL logic error: ambiguous column name".
func TestLiveDB_M2O_FKNameEqualsTargetPK(t *testing.T) {
	drv := openSQLite(t)
	ctx := context.Background()

	execAll(t, drv,
		`CREATE TABLE terms (terms_id INTEGER PRIMARY KEY, name TEXT NOT NULL)`,
		`CREATE TABLE sales_order (id INTEGER PRIMARY KEY, terms_id INTEGER REFERENCES terms(terms_id))`,
		`INSERT INTO terms VALUES (1, 'Net30'), (2, 'Net60')`,
		`INSERT INTO sales_order VALUES (10, 1), (11, 2), (12, 1)`,
	)

	// Traverse: given sales_order id=10, find its terms row.
	step := sqlgraph.NewStep(
		sqlgraph.From("sales_order", "id", 10),
		sqlgraph.To("terms", "terms_id"),
		sqlgraph.Edge(sqlgraph.M2O, true, "sales_order", "terms_id"),
	)

	base := NewQueryBase(drv, "terms", []string{"terms_id", "name"}, "terms_id", nil, "Terms")
	base.Path = func(_ context.Context) (*sql.Selector, error) {
		return sqlgraph.Neighbors(drv.Dialect(), step), nil
	}

	sel, err := BuildSelectorFrom(ctx, base)
	require.NoError(t, err)

	rows := &sql.Rows{}
	query, args := sel.Query()
	require.NoError(t, drv.Query(ctx, query, args, rows),
		"query must not fail with ambiguous column name; SQL: %s", query)
	defer rows.Close()

	cols, err := rows.Columns()
	require.NoError(t, err)

	type row struct {
		id   int
		name string
	}
	var got []row
	for rows.Next() {
		var id int
		var name string
		require.NoError(t, rows.Scan(scanByColumn(cols, &id, &name)...))
		got = append(got, row{id, name})
	}
	require.NoError(t, rows.Err())
	require.Len(t, got, 1)
	assert.Equal(t, 1, got[0].id)
	assert.Equal(t, "Net30", got[0].name)
}

// TestLiveDB_O2OInverse_FKNameEqualsTargetPK covers the O2O inverse edge shape
// (also FromEdgeOwner) with FK name = target PK name.
func TestLiveDB_O2OInverse_FKNameEqualsTargetPK(t *testing.T) {
	drv := openSQLite(t)
	ctx := context.Background()

	execAll(t, drv,
		`CREATE TABLE category (category_id INTEGER PRIMARY KEY, label TEXT NOT NULL)`,
		`CREATE TABLE product (id INTEGER PRIMARY KEY, category_id INTEGER REFERENCES category(category_id))`,
		`INSERT INTO category VALUES (5, 'Electronics')`,
		`INSERT INTO product VALUES (100, 5)`,
	)

	step := sqlgraph.NewStep(
		sqlgraph.From("product", "id", 100),
		sqlgraph.To("category", "category_id"),
		sqlgraph.Edge(sqlgraph.O2O, true, "product", "category_id"),
	)

	base := NewQueryBase(drv, "category", []string{"category_id", "label"}, "category_id", nil, "Category")
	base.Path = func(_ context.Context) (*sql.Selector, error) {
		return sqlgraph.Neighbors(drv.Dialect(), step), nil
	}

	sel, err := BuildSelectorFrom(ctx, base)
	require.NoError(t, err)

	rows := &sql.Rows{}
	query, args := sel.Query()
	require.NoError(t, drv.Query(ctx, query, args, rows),
		"query must not fail; SQL: %s", query)
	defer rows.Close()

	cols, err := rows.Columns()
	require.NoError(t, err)

	var count int
	for rows.Next() {
		var id int
		var label string
		require.NoError(t, rows.Scan(scanByColumn(cols, &id, &label)...))
		assert.Equal(t, 5, id)
		assert.Equal(t, "Electronics", label)
		count++
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, 1, count)
}

// TestLiveDB_SetNeighbors_M2O_FKNameEqualsTargetPK covers the set-based M2O
// path (multiple parents at once — what query-level traversal uses).
func TestLiveDB_SetNeighbors_M2O_FKNameEqualsTargetPK(t *testing.T) {
	drv := openSQLite(t)
	ctx := context.Background()

	execAll(t, drv,
		`CREATE TABLE terms (terms_id INTEGER PRIMARY KEY, name TEXT NOT NULL)`,
		`CREATE TABLE sales_order (id INTEGER PRIMARY KEY, terms_id INTEGER REFERENCES terms(terms_id))`,
		`INSERT INTO terms VALUES (1, 'Net30'), (2, 'Net60')`,
		`INSERT INTO sales_order VALUES (10, 1), (11, 2), (12, 1)`,
	)

	// Simulate query-level traversal: "for orders 10, 11, 12 — fetch their terms".
	parentSel := sql.Dialect(drv.Dialect()).
		Select().
		From(sql.Table("sales_order")).
		Where(sql.InValues("id", 10, 11, 12))

	step := sqlgraph.NewStep(
		sqlgraph.From("sales_order", "id"),
		sqlgraph.To("terms", "terms_id"),
		sqlgraph.Edge(sqlgraph.M2O, true, "sales_order", "terms_id"),
	)
	step.From.V = parentSel

	base := NewQueryBase(drv, "terms", []string{"terms_id", "name"}, "terms_id", nil, "Terms")
	base.Path = func(_ context.Context) (*sql.Selector, error) {
		return sqlgraph.SetNeighbors(drv.Dialect(), step), nil
	}

	sel, err := BuildSelectorFrom(ctx, base)
	require.NoError(t, err)

	rows := &sql.Rows{}
	query, args := sel.Query()
	require.NoError(t, drv.Query(ctx, query, args, rows),
		"query must not fail; SQL: %s", query)
	defer rows.Close()

	var count int
	for rows.Next() {
		count++
	}
	require.NoError(t, rows.Err())
	// SetNeighbors joins each sales_order row to its terms row — one result
	// per order, not per distinct terms. Orders 10,12 both point to Net30,
	// order 11 points to Net60: 3 rows total (duplicates expected).
	assert.Equal(t, 3, count)
}

// scanByColumn builds a []any scan target whose positions match cols.
// This avoids depending on a generated entity's ScanValues method.
func scanByColumn(cols []string, id *int, name *string) []any {
	dest := make([]any, len(cols))
	for i, c := range cols {
		switch c {
		case cols[0]: // first column → id-like field
			dest[i] = id
		default:
			dest[i] = name
		}
	}
	return dest
}
