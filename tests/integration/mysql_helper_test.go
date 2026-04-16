package integration_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql" // mysql driver registration

	"github.com/syssam/velox/dialect"
	integration "github.com/syssam/velox/tests/integration"
)

// openMySQLOrSkip opens a connection to the MySQL instance described
// by VELOX_TEST_MYSQL (full DSN) or the conventional MYSQL_* env vars.
// Tests that need a real MySQL call this; if nothing is configured or
// the connection fails, the test is SKIPPED with a clear message —
// local `go test` keeps working without MySQL.
//
// CI sets VELOX_TEST_MYSQL for the docker mysql container. Local dev
// exports it (or MYSQL_HOST/MYSQL_PORT/...) when the docker container
// is running.
func openMySQLOrSkip(t testing.TB) (*integration.Client, func()) {
	t.Helper()

	dsn := os.Getenv("VELOX_TEST_MYSQL")
	if dsn == "" {
		host := os.Getenv("MYSQL_HOST")
		if host == "" {
			t.Skip("mysql env vars not set; skipping. " +
				"Export VELOX_TEST_MYSQL (DSN) or " +
				"MYSQL_HOST/MYSQL_PORT/MYSQL_USER/MYSQL_PASSWORD/MYSQL_DATABASE, " +
				"or run docker mysql to enable this test.")
		}
		dsn = fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&multiStatements=true",
			envOrDefault("MYSQL_USER", "root"),
			envOrDefault("MYSQL_PASSWORD", "root"),
			host,
			envOrDefault("MYSQL_PORT", "3306"),
			envOrDefault("MYSQL_DATABASE", "velox_test"),
		)
	}

	client, err := integration.Open(dialect.MySQL, dsn)
	if err != nil {
		t.Fatalf("mysql open: %v", err)
	}

	// Short timeout so a misconfigured DSN fails fast.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := pingClient(ctx, client); err != nil {
		_ = client.Close()
		t.Skipf("mysql ping failed: %v (skipping; docker mysql not running?)", err)
	}

	if err := client.Schema.Create(ctx); err != nil {
		_ = client.Close()
		t.Fatalf("mysql migrate: %v", err)
	}

	cleanup := func() {
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer dropCancel()
		if err := dropAllMySQLTables(dropCtx, client); err != nil {
			t.Logf("mysql cleanup: %v", err)
		}
		_ = client.Close()
	}
	return client, cleanup
}

// dropAllMySQLTables wipes all tables in the current database between
// test runs. We disable FK checks around the DROP so circular FK
// graphs can be torn down in any order, then re-enable.
//
// Like the Postgres helper, we deliberately do NOT drop the database
// itself — it may share config/users with other tooling. Dropping the
// tables is sufficient for isolation.
func dropAllMySQLTables(ctx context.Context, client *integration.Client) error {
	// List tables in the current schema. information_schema.tables
	// is the portable way; table_schema = DATABASE() scopes to the
	// connection's current db.
	rows, err := client.QueryContext(ctx,
		"SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE()")
	if err != nil {
		return err
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			return err
		}
		tables = append(tables, name)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(tables) == 0 {
		return nil
	}

	if _, err := client.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 0"); err != nil {
		return err
	}
	defer func() {
		_, _ = client.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 1")
	}()
	for _, name := range tables {
		if _, err := client.ExecContext(ctx, fmt.Sprintf("DROP TABLE IF EXISTS `%s`", name)); err != nil {
			return err
		}
	}
	return nil
}

// TestMySQLHelper_Smoke proves the helper is wired up. SKIPs cleanly
// when no mysql is reachable — correct behavior on a dev box without
// docker.
func TestMySQLHelper_Smoke(t *testing.T) {
	client, cleanup := openMySQLOrSkip(t)
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
