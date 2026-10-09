package shapes

import (
	"bytes"
	"context"
	stdsql "database/sql"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect"
	"github.com/syssam/velox/vx/orm"
	"github.com/syssam/velox/vx/tests/shapes/db/migrate"
)

// ddlOf returns the statement that created every table and index of db.
func ddlOf(t *testing.T, db *stdsql.DB) map[string]string {
	t.Helper()
	rows, err := db.Query("SELECT type || ' ' || name, COALESCE(sql, '') FROM sqlite_master WHERE name NOT LIKE 'sqlite_%'")
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var name, ddl string
		require.NoError(t, rows.Scan(&name, &ddl))
		out[name] = ddl
	}
	require.NoError(t, rows.Err())
	return out
}

// The generated migrate package creates, for every shape of the matrix, the
// tables velox's migrator creates from the schema graph: same columns, same
// constraints, same names. A project that moves from velox to vx must find
// its database already up to date.
func TestGeneratedMigrate(t *testing.T) {
	ctx := context.Background()
	want := ddlOf(t, open(t).SQL())
	require.NotEmpty(t, want)

	raw, err := stdsql.Open("sqlite", fmt.Sprintf("file:migrate_%d?mode=memory&cache=shared&_pragma=foreign_keys(1)", dbSeq.Add(1)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = raw.Close() })
	db := orm.OpenDB(dialect.SQLite, raw)

	// WriteTo prints the statements and runs none.
	var plan bytes.Buffer
	require.NoError(t, migrate.WriteTo(ctx, db, &plan))
	assert.Contains(t, plan.String(), "CREATE TABLE")
	assert.Empty(t, ddlOf(t, raw), "WriteTo changed the database")

	require.NoError(t, migrate.Create(ctx, db))
	got := ddlOf(t, raw)
	assert.Len(t, got, len(want))
	for name, ddl := range want {
		assert.Equal(t, ddl, got[name], name)
	}

	// A database that is up to date is left alone.
	plan.Reset()
	require.NoError(t, migrate.WriteTo(ctx, db, &plan))
	assert.NotContains(t, plan.String(), "CREATE", "a second migration of the same schema is not empty")
	assert.NotContains(t, plan.String(), "ALTER", "a second migration of the same schema is not empty")
	require.NoError(t, migrate.Create(ctx, db))
	assert.Equal(t, got, ddlOf(t, raw))
}
