package integration_test

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
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
	// Schema migration on a loaded CI runner (or under -race) can take well
	// over 5s; a short deadline turns machine load into a spurious failure.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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

// supportsForShare reports whether the server behind client accepts the
// standard `SELECT ... FOR SHARE` clause.
//
// MySQL only learned that spelling in 8.0.1; 5.x expresses the same shared
// row lock as `LOCK IN SHARE MODE` and answers `FOR SHARE` with a 1064 syntax
// error. velox — like ent — renders the clause from the dialect name alone
// (the static dialect.GetCapabilities set, not the probed server version), so a 5.x caller opts into the
// old spelling explicitly:
//
//	q.ForShare(sql.WithLockClause("LOCK IN SHARE MODE"))
//
// ent guards its own lock coverage the same way, with skip(t, "MySQL/5") in
// entc/integration/integration_test.go::Lock — so this is parity, not a gap.
// Every non-MySQL dialect is reported as supporting it: Postgres has FOR
// SHARE, and SQLite drops row locks in Selector.For, so the clause never
// reaches the engine.
func supportsForShare(t *testing.T, client *integration.Client) bool {
	t.Helper()
	if client.RuntimeConfig().Driver.Dialect() != dialect.MySQL {
		return true
	}
	version := mysqlServerVersion(t, client)
	// MariaDB reports e.g. "10.11.6-MariaDB" and has no FOR SHARE at all.
	if strings.Contains(strings.ToLower(version), "mariadb") {
		return false
	}
	return compareMySQLVersion(version, "8.0.1") >= 0
}

// supportsWindowFunctions reports whether the server behind client has
// window functions (ROW_NUMBER() OVER), which per-parent eager-load limits
// render when they can: MySQL 8.0+, MariaDB 10.2+, every Postgres and the
// bundled SQLite. It is derived from the version here, independently of
// dialect.VersionCapabilities, so a wrong rule there shows up as a test
// failure instead of agreeing with itself.
func supportsWindowFunctions(t *testing.T, client *integration.Client) bool {
	t.Helper()
	if client.RuntimeConfig().Driver.Dialect() != dialect.MySQL {
		return true
	}
	version := mysqlServerVersion(t, client)
	if strings.Contains(strings.ToLower(version), "mariadb") {
		return compareMySQLVersion(strings.TrimPrefix(version, "5.5.5-"), "10.2.0") >= 0
	}
	return compareMySQLVersion(version, "8.0.0") >= 0
}

// mysqlServerVersion returns SELECT VERSION() of the server behind client.
func mysqlServerVersion(t *testing.T, client *integration.Client) string {
	t.Helper()
	var version string
	rows, err := client.QueryContext(context.Background(), "SELECT VERSION()")
	if err != nil {
		t.Fatalf("mysql version: %v", err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		t.Fatal("mysql version: no rows")
	}
	if err := rows.Scan(&version); err != nil {
		t.Fatalf("mysql version scan: %v", err)
	}
	return version
}

// compareMySQLVersion compares two dotted version strings numerically,
// ignoring any suffix after the patch component ("8.0.36-log" == "8.0.36").
// Returns -1, 0 or 1 as a is less than, equal to, or greater than b.
func compareMySQLVersion(a, b string) int {
	split := func(v string) []int {
		if i := strings.IndexAny(v, "-+ "); i >= 0 {
			v = v[:i]
		}
		parts := strings.Split(v, ".")
		out := make([]int, 3)
		for i := 0; i < len(parts) && i < 3; i++ {
			n, err := strconv.Atoi(parts[i])
			if err != nil {
				return out
			}
			out[i] = n
		}
		return out
	}
	av, bv := split(a), split(b)
	for i := range av {
		switch {
		case av[i] < bv[i]:
			return -1
		case av[i] > bv[i]:
			return 1
		}
	}
	return 0
}

// TestCompareMySQLVersion pins the boundary that decides whether the FOR
// SHARE leg of TestMultiDialect_LockWithDistinct runs: 8.0.1 is the first
// MySQL that parses the clause. A comparison that wrongly reports "too old"
// silently drops that coverage on a modern server, which is how the clause
// shipped untested in the first place.
func TestCompareMySQLVersion(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"5.7.44", "8.0.1", -1},
		{"8.0.0", "8.0.1", -1},
		{"8.0.1", "8.0.1", 0},
		{"8.0.36", "8.0.1", 1},
		{"8.4.0", "8.0.1", 1},
		{"9.1.0", "8.0.1", 1},
		// Suffixes are common in the wild and must not defeat the compare.
		{"8.0.36-log", "8.0.1", 1},
		{"5.7.44-log", "8.0.1", -1},
		{"8.0.1-0ubuntu0.20.04.1", "8.0.1", 0},
		// Short forms pad with zeros rather than mis-ranking.
		{"8", "8.0.1", -1},
		{"8.1", "8.0.1", 1},
	} {
		if got := compareMySQLVersion(tc.a, tc.b); got != tc.want {
			t.Errorf("compareMySQLVersion(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
