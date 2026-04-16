// Package versionedmigration provides a minimal migration runner that applies
// versioned .sql files in order. This is an illustrative pattern — for a
// production system, consider Atlas (https://atlasgo.io/), golang-migrate,
// or goose.
package versionedmigration

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strings"
)

// Migrations are the .sql files shipped with this example. The `//go:embed`
// directive lets us package migrations into the binary — no filesystem
// access required at runtime.
//
//go:embed migrations/*.sql
var Migrations embed.FS

// Runner applies versioned migrations against a *sql.DB. It records each
// applied migration in a `schema_migrations` table so re-running is safe.
type Runner struct {
	DB *sql.DB
}

// Up applies all pending migrations in filename order. Each migration
// runs inside a transaction; the schema_migrations row is inserted in
// the same tx so crashes don't leave partial state.
func (r *Runner) Up(ctx context.Context) error {
	if err := r.ensureTable(ctx); err != nil {
		return err
	}

	applied, err := r.appliedVersions(ctx)
	if err != nil {
		return err
	}

	files, err := Migrations.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(files))
	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".sql") {
			names = append(names, f.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		version := versionFromFilename(name)
		if applied[version] {
			continue
		}
		content, err := Migrations.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		if err := r.apply(ctx, version, string(content)); err != nil {
			return fmt.Errorf("apply %s: %w", name, err)
		}
	}
	return nil
}

func (r *Runner) ensureTable(ctx context.Context) error {
	_, err := r.DB.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`)
	return err
}

func (r *Runner) appliedVersions(ctx context.Context) (map[string]bool, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	applied := map[string]bool{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		applied[v] = true
	}
	return applied, rows.Err()
}

func (r *Runner) apply(ctx context.Context, version, sqlText string) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, sqlText); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version) VALUES(?)`, version); err != nil {
		return err
	}
	return tx.Commit()
}

// versionFromFilename extracts the leading timestamp from a filename like
// "20260101000000_create_users.sql".
func versionFromFilename(name string) string {
	if i := strings.Index(name, "_"); i > 0 {
		return name[:i]
	}
	return name
}
