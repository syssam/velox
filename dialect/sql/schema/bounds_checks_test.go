package schema

import (
	"context"
	"strings"
	"testing"

	"ariga.io/atlas/sql/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/schema/field"
)

// boundsTable is an inventory row whose validators set bounds: a stock that
// must not go negative, a rating in 1..5, a nullable discount, and a column
// named order, which only a quoted identifier can refer to.
func boundsTable() *Table {
	id := &Column{Name: "id", Type: field.TypeInt, Increment: true}
	return &Table{
		Name: "items",
		Columns: []*Column{
			id,
			{Name: "stock", Type: field.TypeInt, Bounds: []field.Bound{{Op: ">=", Value: "0"}}},
			{Name: "rating", Type: field.TypeInt, Bounds: []field.Bound{{Op: ">=", Value: "1"}, {Op: "<=", Value: "5"}}},
			{Name: "discount", Type: field.TypeFloat64, Nullable: true, Bounds: []field.Bound{{Op: ">", Value: "0"}}},
			{Name: "order", Type: field.TypeInt, Bounds: []field.Bound{{Op: ">=", Value: "1"}}},
		},
		PrimaryKey: []*Column{id},
	}
}

// A validator sees SetX, never the SET x = x - 5 that AddX writes; the
// constraint sees both. NULL passes a CHECK, as SQL has it, so a nullable
// bounded field stays nullable.
func TestBoundsChecksEnforcedOnSQLite(t *testing.T) {
	db, drv := openSQLite(t)
	ctx := context.Background()
	require.NoError(t, newSQLiteMigrate(t, drv).Create(ctx, boundsTable()))

	exec := func(q string, args ...any) error {
		_, err := db.ExecContext(ctx, q, args...)
		return err
	}
	require.NoError(t, exec(`INSERT INTO items (stock, rating, discount, "order") VALUES (3, 5, NULL, 1)`))
	for name, q := range map[string]string{
		"negative stock":         `INSERT INTO items (stock, rating, "order") VALUES (-1, 3, 1)`,
		"rating above the range": `INSERT INTO items (stock, rating, "order") VALUES (0, 6, 1)`,
		"rating below the range": `INSERT INTO items (stock, rating, "order") VALUES (0, 0, 1)`,
		"a zero discount":        `INSERT INTO items (stock, rating, discount, "order") VALUES (0, 3, 0, 1)`,
		"reserved-word column":   `INSERT INTO items (stock, rating, "order") VALUES (0, 3, 0)`,
		"AddX past the bound":    `UPDATE items SET stock = stock - 5`,
	} {
		err := exec(q)
		if assert.Error(t, err, name) {
			assert.Contains(t, strings.ToLower(err.Error()), "check constraint", name)
		}
	}
	var stock int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT stock FROM items`).Scan(&stock))
	assert.Equal(t, 3, stock, "the rejected update must leave the row alone")
}

// A migration that re-diffs a constraint on every run is a migration nobody
// can trust. The second run against the same schema must change nothing.
func TestBoundsChecksAreStableAcrossMigrations(t *testing.T) {
	_, drv := openSQLite(t)
	ctx := context.Background()
	require.NoError(t, newSQLiteMigrate(t, drv).Create(ctx, boundsTable()))

	var changes []schema.Change
	m := newSQLiteMigrate(t, drv, WithDiffHook(func(next Differ) Differ {
		return DiffFunc(func(current, desired *schema.Schema) ([]schema.Change, error) {
			c, err := next.Diff(current, desired)
			changes = c
			return c, err
		})
	}))
	require.NoError(t, m.Create(ctx, boundsTable()))
	assert.Empty(t, changes, "the second migration re-diffed: %v", changes)
}

func TestBoundsCheckName(t *testing.T) {
	assert.Equal(t, "items_stock_check", boundsCheckName("items", "stock"))
	long := boundsCheckName(strings.Repeat("t", 40), strings.Repeat("c", 40))
	assert.Len(t, long, 63, "PostgreSQL truncates past 63 bytes; the name must already fit")
	assert.NotEqual(t, long, boundsCheckName(strings.Repeat("t", 40), strings.Repeat("c", 41)), "two long names must not collide on their prefix")
}
