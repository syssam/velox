package integration_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq" // postgres driver registration

	"github.com/syssam/velox/dialect"
	integration "github.com/syssam/velox/tests/integration"
)

// openPostgresOrSkip opens a connection to the Postgres instance
// described by the standard PG* environment variables. Tests that
// need a real Postgres call this; if the env vars aren't set or the
// connection fails, the test is SKIPPED with a clear message —
// local `go test` keeps working without Postgres.
//
// CI sets PGHOST/PGPORT/PGUSER/PGPASSWORD/PGDATABASE for the docker
// postgres container. Local dev exports them when the docker
// container is running.
func openPostgresOrSkip(t testing.TB) (*integration.Client, func()) {
	t.Helper()

	// VELOX_TEST_POSTGRES (full DSN) takes precedence — matches the
	// convention used by dialect/sql/schema integration tests and CI.
	// PGHOST-style env vars remain supported as a local-dev fallback.
	dsn := os.Getenv("VELOX_TEST_POSTGRES")
	if dsn == "" {
		host := os.Getenv("PGHOST")
		if host == "" {
			t.Skip("postgres env vars not set; skipping. " +
				"Export VELOX_TEST_POSTGRES (DSN) or " +
				"PGHOST/PGPORT/PGUSER/PGPASSWORD/PGDATABASE, " +
				"or run docker postgres to enable this test.")
		}
		dsn = fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
			host,
			envOrDefault("PGPORT", "5432"),
			envOrDefault("PGUSER", "postgres"),
			envOrDefault("PGPASSWORD", "postgres"),
			envOrDefault("PGDATABASE", "velox_test"),
		)
	}

	client, err := integration.Open(dialect.Postgres, dsn)
	if err != nil {
		t.Fatalf("postgres open: %v", err)
	}

	// Verify the connection is reachable. Use a short timeout so a
	// misconfigured DSN fails fast instead of hanging.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := pingClient(ctx, client); err != nil {
		_ = client.Close()
		t.Skipf("postgres ping failed: %v (skipping; docker postgres not running?)", err)
	}

	// Run schema migration. Each test gets a fresh table set so
	// state from prior runs cannot leak in.
	if err := client.Schema.Create(ctx); err != nil {
		_ = client.Close()
		t.Fatalf("postgres migrate: %v", err)
	}

	cleanup := func() {
		// Drop the public schema and recreate so the next test
		// starts with an empty database. The owner is set to the
		// connection user so DROP/CREATE is permitted.
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer dropCancel()
		if err := dropAllTables(dropCtx, client); err != nil {
			t.Logf("postgres cleanup: %v", err)
		}
		_ = client.Close()
	}
	return client, cleanup
}

// envOrDefault returns the env var value if set and non-empty,
// otherwise the default. Lets local dev rely on conventional
// postgres defaults without exporting every variable.
func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// pingClient executes a trivial round-trip to confirm the connection
// is alive. Uses the velox client's underlying driver via a no-op
// SELECT so we don't have to import database/sql here.
func pingClient(ctx context.Context, client *integration.Client) error {
	// Schema.Create on a fresh db doubles as a connection check —
	// it issues at least one CREATE statement and surfaces network
	// errors immediately. We don't actually want to migrate yet
	// (the caller does that next), so just open and close a no-op
	// transaction instead.
	tx, err := client.Tx(ctx)
	if err != nil {
		return err
	}
	return tx.Rollback()
}

// dropAllTables wipes only the tables and sequences in the public
// schema between test runs. We deliberately DO NOT `DROP SCHEMA
// public CASCADE` — that also removes extensions, functions, and
// custom types, which the test DB may share with other tooling
// (e.g. pgcrypto/uuid-ossp enabled at provisioning time). Dropping
// tables with CASCADE transitively removes their foreign keys and
// owned sequences, which is all we need.
//
// The DO block iterates pg_tables so callers don't have to keep the
// list in sync with the schema — any table velox creates in public
// is wiped, nothing else is touched.
func dropAllTables(ctx context.Context, client *integration.Client) error {
	const dropTables = `
DO $$ DECLARE
	r RECORD;
BEGIN
	FOR r IN (SELECT tablename FROM pg_tables WHERE schemaname = 'public') LOOP
		EXECUTE 'DROP TABLE IF EXISTS public.' || quote_ident(r.tablename) || ' CASCADE';
	END LOOP;
END $$;`
	const dropSequences = `
DO $$ DECLARE
	r RECORD;
BEGIN
	FOR r IN (SELECT sequence_name FROM information_schema.sequences WHERE sequence_schema = 'public') LOOP
		EXECUTE 'DROP SEQUENCE IF EXISTS public.' || quote_ident(r.sequence_name) || ' CASCADE';
	END LOOP;
END $$;`
	if _, err := client.ExecContext(ctx, dropTables); err != nil {
		return err
	}
	if _, err := client.ExecContext(ctx, dropSequences); err != nil {
		return err
	}
	return nil
}

// TestPostgresHelper_Smoke is a tiny exercise that proves the helper
// is wired up. It SKIPs cleanly when no postgres is reachable, which
// is the correct behavior in a dev box without docker.
func TestPostgresHelper_Smoke(t *testing.T) {
	client, cleanup := openPostgresOrSkip(t)
	defer cleanup()

	ctx := context.Background()
	u, err := client.User.Create().SetName("smoke").SetEmail("smoke@x").SetAge(1).Save(ctx)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if u.Name != "smoke" {
		t.Fatalf("unexpected name: %q", u.Name)
	}
}
