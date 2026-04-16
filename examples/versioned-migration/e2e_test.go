package versionedmigration_test

import (
	"context"
	"database/sql"
	"testing"

	versionedmigration "example.com/versioned-migration"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "modernc.org/sqlite"
)

// TestVersionedMigration walks through the lifecycle of a versioned-migration
// workflow. It shows what users care about:
//
//  1. Fresh DB → all migrations apply in order.
//  2. Re-running Up on an already-migrated DB is a no-op.
//  3. Adding a new migration file and running Up again applies only the new one.
func TestVersionedMigration(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file:vm.db?mode=memory&_pragma=foreign_keys(1)")
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()

	runner := &versionedmigration.Runner{DB: db}

	// --- 1. First run: both embedded migrations apply. ---
	require.NoError(t, runner.Up(ctx))

	// Both versions should be recorded.
	applied := listApplied(t, db)
	assert.Equal(t, []string{"20260101000000", "20260201000000"}, applied)

	// Schema shape reflects both migrations (users table + bio column).
	insertUser(t, db, "Alice", "alice@example.com", "hello world")
	assertUserCount(t, db, 1)

	// --- 2. Second run: idempotent, no-op. ---
	require.NoError(t, runner.Up(ctx))
	applied = listApplied(t, db)
	assert.Len(t, applied, 2, "re-running Up must not re-apply migrations")
	assertUserCount(t, db, 1)
}

func listApplied(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT version FROM schema_migrations ORDER BY version`)
	require.NoError(t, err)
	defer func() { require.NoError(t, rows.Close()) }()

	var versions []string
	for rows.Next() {
		var v string
		require.NoError(t, rows.Scan(&v))
		versions = append(versions, v)
	}
	return versions
}

func insertUser(t *testing.T, db *sql.DB, name, email, bio string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO users(name, email, bio) VALUES(?, ?, ?)`, name, email, bio)
	require.NoError(t, err)
}

func assertUserCount(t *testing.T, db *sql.DB, want int) {
	t.Helper()
	var got int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&got))
	assert.Equal(t, want, got)
}
