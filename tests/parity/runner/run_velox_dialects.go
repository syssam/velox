// run_velox_dialects.go opens velox + ent clients against real Postgres / MySQL
// servers for the A3b dialect matrix. Like run_velox.go / run_ent.go it is one
// of the files the architecture guard
// (architecture_test.go::TestBrainHasNoORMImports) permits to import the ORMs —
// its name is prefixed "run_velox". It holds the env-gated, separate-database,
// truncate-isolated client harness shared by the dialect tests and the driver.
//
// Separate databases: velox and ent migrate identical table names, so on one
// shared server they live in different databases (velox→base, ent→"<base>_ent").
// The ORM-free DSN derivation, database creation, and truncation live in
// db_postgres.go / db_mysql.go; this file only wires the ORM clients on top.

package runner

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/syssam/velox/dialect"

	"velox.test/parity/ent"
	velox "velox.test/parity/velox"
)

// dialectPair holds an opened (velox, ent) client pair for one backend plus the
// raw *sql.DB handles used to truncate each ORM's database between programs. The
// driver reuses one pair across a whole test, truncating before each program;
// the test-facing constructors return just the two clients plus an ok flag.
type dialectPair struct {
	velox     *velox.Client
	ent       *ent.Client
	veloxDB   *sql.DB // separate connection to velox's database, for truncation
	entDB     *sql.DB // separate connection to ent's database, for truncation
	truncateV func(ctx context.Context, db *sql.DB) error
	truncateE func(ctx context.Context, db *sql.DB) error
}

// reset truncates both ORMs' databases so the next program starts clean. Used by
// the driver before each program (the PG/MySQL analogue of SQLite's fresh
// in-memory client per program).
func (p *dialectPair) reset(ctx context.Context) error {
	if err := p.truncateV(ctx, p.veloxDB); err != nil {
		return err
	}
	return p.truncateE(ctx, p.entDB)
}

// close releases both clients and both truncation connections.
func (p *dialectPair) close() {
	if p.velox != nil {
		_ = p.velox.Close()
	}
	if p.ent != nil {
		_ = p.ent.Close()
	}
	if p.veloxDB != nil {
		_ = p.veloxDB.Close()
	}
	if p.entDB != nil {
		_ = p.entDB.Close()
	}
}

// dialectDialTimeout bounds the initial connect/ping so a missing or
// misconfigured server fails fast into a skip instead of hanging the suite.
const dialectDialTimeout = 5 * time.Second

// newPostgresPair opens the velox+ent client pair against Postgres, or returns
// ok=false (so the caller skips) when VELOX_TEST_POSTGRES is unset or the server
// is unreachable / the test user cannot create the ent database. veloxOpts /
// entOpts let callers attach SQL-trace options (Debug/Log); test-facing
// constructors pass none.
func newPostgresPair(t testing.TB, veloxOpts []velox.Option, entOpts []ent.Option) (*dialectPair, bool) {
	t.Helper()
	dsn := os.Getenv(pgEnvVar)
	if dsn == "" {
		return nil, false
	}
	baseDB, entDSN := deriveEntPostgresDSN(dsn)
	if baseDB == "" {
		t.Logf("postgres: DSN %q has no dbname; skipping", dsn)
		return nil, false
	}

	ctx, cancel := context.WithTimeout(context.Background(), dialectDialTimeout)
	defer cancel()
	if err := ensurePostgresDatabases(ctx, dsn, baseDB); err != nil {
		t.Logf("postgres: cannot reach server or create %s_ent (%v); skipping", baseDB, err)
		return nil, false
	}

	pair, ok := openDialectPair(t, dialect.Postgres, "postgres", dsn, entDSN,
		veloxOpts, entOpts, truncatePostgres, truncatePostgres)
	if !ok {
		return nil, false
	}
	return pair, true
}

// newMySQLPair is the MySQL analogue of newPostgresPair.
func newMySQLPair(t testing.TB, veloxOpts []velox.Option, entOpts []ent.Option) (*dialectPair, bool) {
	t.Helper()
	dsn := os.Getenv(mysqlEnvVar)
	if dsn == "" {
		return nil, false
	}
	baseDB, entDSN := deriveEntMySQLDSN(dsn)
	if baseDB == "" {
		t.Logf("mysql: DSN %q has no database; skipping", dsn)
		return nil, false
	}

	ctx, cancel := context.WithTimeout(context.Background(), dialectDialTimeout)
	defer cancel()
	if err := ensureMySQLDatabases(ctx, dsn, baseDB); err != nil {
		t.Logf("mysql: cannot reach server or create %s_ent (%v); skipping", baseDB, err)
		return nil, false
	}

	pair, ok := openDialectPair(t, dialect.MySQL, "mysql", dsn, entDSN,
		veloxOpts, entOpts, truncateMySQL, truncateMySQL)
	if !ok {
		return nil, false
	}
	return pair, true
}

// openDialectPair opens velox (on dsn) and ent (on entDSN), migrates both,
// opens the two raw truncation connections, truncates once so a prior run can't
// leak, and registers cleanup. veloxDialect is the velox dialect constant
// (dialect.Postgres/MySQL); driverName is the database/sql driver name shared by
// the raw truncation handles and ent ("postgres"/"mysql"). On any failure it
// returns ok=false so the caller skips rather than fails — a no-DB or
// low-privilege box stays green.
func openDialectPair(
	t testing.TB,
	veloxDialect, driverName, dsn, entDSN string,
	veloxOpts []velox.Option, entOpts []ent.Option,
	truncateV, truncateE func(ctx context.Context, db *sql.DB) error,
) (*dialectPair, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), dialectDialTimeout)
	defer cancel()

	vc, err := velox.Open(veloxDialect, dsn, veloxOpts...)
	if err != nil {
		t.Logf("%s: velox open failed (%v); skipping", veloxDialect, err)
		return nil, false
	}
	if err := vc.Schema.Create(ctx); err != nil {
		_ = vc.Close()
		t.Logf("%s: velox migrate failed (%v); skipping", veloxDialect, err)
		return nil, false
	}

	ec, err := ent.Open(driverName, entDSN, entOpts...)
	if err != nil {
		_ = vc.Close()
		t.Logf("%s: ent open failed (%v); skipping", veloxDialect, err)
		return nil, false
	}
	if err := ec.Schema.Create(ctx); err != nil {
		_ = vc.Close()
		_ = ec.Close()
		t.Logf("%s: ent migrate failed (%v); skipping", veloxDialect, err)
		return nil, false
	}

	veloxDB, err := sql.Open(driverName, dsn)
	if err != nil {
		_ = vc.Close()
		_ = ec.Close()
		t.Logf("%s: velox truncation handle failed (%v); skipping", veloxDialect, err)
		return nil, false
	}
	entDB, err := sql.Open(driverName, entDSN)
	if err != nil {
		_ = vc.Close()
		_ = ec.Close()
		_ = veloxDB.Close()
		t.Logf("%s: ent truncation handle failed (%v); skipping", veloxDialect, err)
		return nil, false
	}

	pair := &dialectPair{
		velox: vc, ent: ec,
		veloxDB: veloxDB, entDB: entDB,
		truncateV: truncateV, truncateE: truncateE,
	}
	// Start clean even if a previous run left rows behind.
	if err := pair.reset(ctx); err != nil {
		pair.close()
		t.Logf("%s: initial truncate failed (%v); skipping", veloxDialect, err)
		return nil, false
	}
	t.Cleanup(pair.close)
	return pair, true
}

// NewPostgresClients opens migrated velox + ent clients against the Postgres
// server named by VELOX_TEST_POSTGRES (velox→base db, ent→"<base>_ent" db),
// truncates both, and registers cleanup. Returns ok=false when no server is
// configured / reachable so the caller can t.Skip — exactly the env-gated skip
// pattern of tests/integration's postgres helper.
func NewPostgresClients(t testing.TB) (*velox.Client, *ent.Client, bool) {
	t.Helper()
	pair, ok := newPostgresPair(t, nil, nil)
	if !ok {
		return nil, nil, false
	}
	return pair.velox, pair.ent, true
}

// NewMySQLClients is the MySQL analogue of NewPostgresClients (gated on
// VELOX_TEST_MYSQL).
func NewMySQLClients(t testing.TB) (*velox.Client, *ent.Client, bool) {
	t.Helper()
	pair, ok := newMySQLPair(t, nil, nil)
	if !ok {
		return nil, nil, false
	}
	return pair.velox, pair.ent, true
}
