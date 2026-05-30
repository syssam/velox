package runner

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	// lib/pq registers the "postgres" database/sql driver used by both the velox
	// and ent executors against Postgres. It lives here (an ORM-free file) so the
	// driver registration happens once for the package and the ORM client
	// constructors (run_velox_dialects.go) deal only with the ORM clients, not
	// driver wiring. The architecture guard
	// (architecture_test.go::TestBrainHasNoORMImports) does NOT flag this file —
	// lib/pq is a SQL driver, not an ORM.
	_ "github.com/lib/pq"
)

// pgVeloxDB / pgEntDB are the database names the two ORMs migrate into on a
// shared Postgres server. velox and ent migrate the SAME table names
// (authors/posts/tags/comments/post_tags), so on one server they MUST live in
// separate databases or their migrations collide. Convention: velox owns the
// base database, ent owns a sibling "<base>_ent" database.
const pgEntSuffix = "_ent"

// pgEnvVar is the env var holding the velox Postgres DSN (key=value form), e.g.
// "host=localhost port=5433 user=postgres password=test dbname=parity sslmode=disable".
const pgEnvVar = "VELOX_TEST_POSTGRES"

// deriveEntPostgresDSN returns a sibling DSN that switches dbname=<base> to
// dbname=<base>_ent so the ent client migrates into its own database. It parses
// the libpq key=value DSN, rewrites only the dbname key, and re-emits the rest
// untouched. Returns the base db name and the derived DSN.
func deriveEntPostgresDSN(dsn string) (baseDB, entDSN string) {
	fields := strings.Fields(dsn)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		k, v, ok := strings.Cut(f, "=")
		if ok && k == "dbname" {
			baseDB = v
			out = append(out, "dbname="+v+pgEntSuffix)
			continue
		}
		out = append(out, f)
	}
	return baseDB, strings.Join(out, " ")
}

// pgMaintenanceDSN returns a DSN pointing at the "postgres" maintenance
// database, used to issue CREATE DATABASE (which cannot run inside the target
// database / a transaction). It rewrites dbname=<anything> to dbname=postgres.
func pgMaintenanceDSN(dsn string) string {
	fields := strings.Fields(dsn)
	out := make([]string, 0, len(fields))
	seen := false
	for _, f := range fields {
		k, _, ok := strings.Cut(f, "=")
		if ok && k == "dbname" {
			out = append(out, "dbname=postgres")
			seen = true
			continue
		}
		out = append(out, f)
	}
	if !seen {
		out = append(out, "dbname=postgres")
	}
	return strings.Join(out, " ")
}

// ensurePostgresDatabases makes sure both the velox (base) and ent (_ent)
// databases exist. CREATE DATABASE cannot run inside the target db, so we
// connect to the "postgres" maintenance database and create whichever is
// missing (guarded by a pg_database catalog check). If the connection user
// lacks CREATEDB, the create fails; the caller treats that as "skip" rather
// than a hard failure so a low-privilege CI/dev box still passes via skip.
func ensurePostgresDatabases(ctx context.Context, dsn, baseDB string) error {
	maint, err := sql.Open("postgres", pgMaintenanceDSN(dsn))
	if err != nil {
		return err
	}
	defer func() { _ = maint.Close() }()
	if err := maint.PingContext(ctx); err != nil {
		return err
	}
	for _, db := range []string{baseDB, baseDB + pgEntSuffix} {
		if err := createPostgresDBIfMissing(ctx, maint, db); err != nil {
			return err
		}
	}
	return nil
}

// createPostgresDBIfMissing creates db on maint if it does not already exist.
// The catalog check avoids the "database already exists" error and keeps the
// operation idempotent across repeated test runs.
func createPostgresDBIfMissing(ctx context.Context, maint *sql.DB, db string) error {
	var exists bool
	if err := maint.QueryRowContext(ctx,
		"SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)", db).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	// db is an identifier we control (derived from the env DSN), but quote it
	// defensively; CREATE DATABASE does not accept a placeholder.
	if !isSafeIdent(db) {
		return fmt.Errorf("refusing to create database with unsafe name %q", db)
	}
	_, err := maint.ExecContext(ctx, "CREATE DATABASE "+quoteIdent(db))
	return err
}

// truncatePostgres empties every table in the public schema of the database the
// given *sql.DB is connected to. TRUNCATE ... CASCADE transitively truncates any
// FK-dependent tables along with the listed ones, so circular FK graphs tear
// down in any order, and RESTART IDENTITY resets sequences so ids restart at 1
// each program — matching SQLite's fresh-client behavior. It discovers tables
// from pg_tables so it stays in sync with the schema automatically.
func truncatePostgres(ctx context.Context, db *sql.DB) error {
	tables, err := listPostgresTables(ctx, db)
	if err != nil {
		return err
	}
	if len(tables) == 0 {
		return nil
	}
	quoted := make([]string, len(tables))
	for i, t := range tables {
		quoted[i] = "public." + quoteIdent(t)
	}
	stmt := "TRUNCATE TABLE " + strings.Join(quoted, ", ") + " RESTART IDENTITY CASCADE"
	_, err = db.ExecContext(ctx, stmt)
	return err
}

// isSafeIdent reports whether name is a plain SQL identifier (letters, digits,
// underscores; not starting with a digit). Database / table names in this
// harness are derived from a controlled env DSN and the migration schema, so
// this is a defensive guard against injection through a malformed env value,
// not a general-purpose validator.
func isSafeIdent(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
		case r >= '0' && r <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// quoteIdent double-quotes a Postgres identifier, escaping embedded quotes.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// listPostgresTables returns the user tables in the public schema.
func listPostgresTables(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx,
		"SELECT tablename FROM pg_tables WHERE schemaname = 'public'")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		tables = append(tables, name)
	}
	return tables, rows.Err()
}
