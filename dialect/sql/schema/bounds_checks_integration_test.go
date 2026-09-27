//go:build integration

package schema

import (
	"context"
	"strings"
	"testing"

	"ariga.io/atlas/sql/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// PostgreSQL stores a CHECK as it parsed it -- (stock >= 0), quotes dropped,
// parentheses added -- so the constraint Atlas reads back is not the text it
// wrote. It must still compare equal, or every migration alters every
// bounded table.
func TestBoundsChecksOnPostgres(t *testing.T) {
	db, drv := openPostgres(t)
	ctx := context.Background()
	tbl := boundsTable()
	tbl.Name = uniqueTableName("items")
	t.Cleanup(func() { _, _ = db.Exec(`DROP TABLE IF EXISTS "` + tbl.Name + `"`) })

	m, err := NewMigrate(drv)
	require.NoError(t, err)
	require.NoError(t, m.Create(ctx, tbl))

	exec := func(q string) error {
		_, err := db.ExecContext(ctx, strings.ReplaceAll(q, "items", `"`+tbl.Name+`"`))
		return err
	}
	require.NoError(t, exec(`INSERT INTO items (stock, rating, discount, "order") VALUES (3, 5, NULL, 1)`))
	for name, q := range map[string]string{
		"negative stock":       `INSERT INTO items (stock, rating, "order") VALUES (-1, 3, 1)`,
		"rating above":         `INSERT INTO items (stock, rating, "order") VALUES (0, 6, 1)`,
		"zero discount":        `INSERT INTO items (stock, rating, discount, "order") VALUES (0, 3, 0, 1)`,
		"reserved-word column": `INSERT INTO items (stock, rating, "order") VALUES (0, 3, 0)`,
		"AddX past the bound":  `UPDATE items SET stock = stock - 5`,
	} {
		err := exec(q)
		if assert.Error(t, err, name) {
			assert.Contains(t, err.Error(), "check constraint", name)
		}
	}

	var changes []schema.Change
	m, err = NewMigrate(drv, WithDiffHook(func(next Differ) Differ {
		return DiffFunc(func(current, desired *schema.Schema) ([]schema.Change, error) {
			c, err := next.Diff(current, desired)
			changes = c
			return c, err
		})
	}))
	require.NoError(t, err)
	require.NoError(t, m.Create(ctx, tbl))
	assert.Empty(t, changes, "the second migration re-diffed")
}
