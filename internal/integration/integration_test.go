// Package integration runs velox against real databases: SQLite always, PostgreSQL
// and MySQL when VELOX_TEST_POSTGRES and VELOX_TEST_MYSQL name them
// (scripts/ci-local.sh starts both and sets the variables). The routing tests
// also make databases beside the CI one, and MySQL needs a root DSN for that:
// VELOX_TEST_MYSQL_ADMIN. With VELOX_ITEST_REQUIRE=1, a missing variable fails the run
// instead of skipping its tests.
package integration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
	"github.com/syssam/velox/veloxpgx"
)

// TestMain refuses to run the itests, when VELOX_ITEST_REQUIRE=1, as CI and
// scripts/ci-local.sh set it, without every database: a missing variable would
// skip the PostgreSQL and MySQL tests without a failure.
func TestMain(m *testing.M) {
	if os.Getenv("VELOX_ITEST_REQUIRE") == "1" {
		var missing []string
		for _, v := range []string{"VELOX_TEST_POSTGRES", "VELOX_TEST_MYSQL", "VELOX_TEST_MYSQL_ADMIN"} {
			if os.Getenv(v) == "" {
				missing = append(missing, v)
			}
		}
		if len(missing) > 0 {
			fmt.Fprintf(os.Stderr, "integration: VELOX_ITEST_REQUIRE=1 but %s unset: the PostgreSQL and MySQL tests would be skipped\n", strings.Join(missing, ", "))
			os.Exit(1)
		}
	}
	if p := dsnDatabaseProblem(os.Getenv("VELOX_TEST_POSTGRES")); p != "" {
		fmt.Fprintln(os.Stderr, p)
		os.Exit(1)
	}
	// The MySQL driver logs "closing bad idle connection" when a test kills a
	// session; under -v that is noise, not a failure.
	_ = mysql.SetLogger(log.New(io.Discard, "", 0))
	os.Exit(m.Run())
}

// dsnDatabaseProblem returns what is wrong with dsn as VELOX_TEST_POSTGRES,
// or "" when it is fine: an unset variable is, and so is a DSN that names a
// database. The routing tests drop and create tables in it, and a name left
// out would silently use the user's default database.
// pgx reads the environment as the tests' connections do, so a DSN without a
// name is fine when PGDATABASE names one.
func dsnDatabaseProblem(dsn string) string {
	if dsn == "" {
		return ""
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return "integration: VELOX_TEST_POSTGRES does not parse as a PostgreSQL DSN"
	}
	if cfg.Database == "" {
		return "integration: VELOX_TEST_POSTGRES names no database: add one, as in postgres://user:pass@host:port/velox"
	}
	return ""
}

func TestITestDSNNamesADatabase(t *testing.T) {
	// pgx fills an empty database name from PGDATABASE; with it set, the
	// refused DSNs below would pass.
	t.Setenv("PGDATABASE", "")
	for _, c := range []struct {
		dsn  string
		fine bool
	}{
		{"", true},
		{"postgres://u:p@h:5432/velox?sslmode=disable", true},
		{"postgres://u:p@h:5432/?sslmode=disable", false},
		{"postgres://u:p@h:5432", false},
	} {
		if got := dsnDatabaseProblem(c.dsn); (got == "") != c.fine {
			t.Errorf("dsnDatabaseProblem(%q) = %q, fine %v", c.dsn, got, c.fine)
		}
	}
	t.Setenv("PGDATABASE", "velox")
	if got := dsnDatabaseProblem("postgres://u:p@h:5432"); got != "" {
		t.Errorf("with PGDATABASE set: %q", got)
	}
}

var (
	ctx = velox.WithAllTenants(context.Background(), "velox tests")
	t0  = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	I   = testmodel.Invoices
)

// target is one database the tests run against.
type target struct {
	name    string
	d       velox.Dialect
	db      *velox.DB
	exec    func(t testing.TB, query string, args ...any) // setup SQL, written with ? placeholders
	ddl     string                                        // CREATE TABLE invoices
	items   string                                        // CREATE TABLE items
	tickets string                                        // CREATE TABLE tickets
	raw     *sql.DB                                       // a second way in, for holding locks; nil for SQLite
	direct  *sql.DB                                       // the database itself, for reading rows back without velox
}

// targets returns SQLite always, and PostgreSQL and MySQL when their
// environment variable is set. An unset variable omits the database; it never
// skips a test, so the SQLite subtests always run. Each target's DB is opened
// with opts.
func targets(t testing.TB, opts []velox.Option) []target {
	t.Helper()
	ts := []target{sqliteTarget(t, opts)}
	if dsn := os.Getenv("VELOX_TEST_POSTGRES"); dsn != "" {
		ts = append(ts, postgresTargets(t, dsn, opts)...)
	}
	if dsn := os.Getenv("VELOX_TEST_MYSQL"); dsn != "" {
		ts = append(ts, mysqlTarget(t, dsn, opts))
	}
	return ts
}

// The integration tests' tables stay hand-written DDL, unlike the examples', which come
// from committed migrations: identities start at 100, the tests assert the
// constraints' names, and there are no foreign keys, none of which a
// migration planned from the schema gives.
const pgDDL = `CREATE TABLE invoices (created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL,
	id bigint GENERATED BY DEFAULT AS IDENTITY (START WITH 100) PRIMARY KEY, org_id bigint NOT NULL, number text NOT NULL,
	total double precision NOT NULL, paid boolean NOT NULL, paid_at timestamptz, note text NOT NULL, data bytea NOT NULL,
	CONSTRAINT invoices_org_number_uq UNIQUE (org_id, number))`

// postgresTargets returns PostgreSQL twice: through native pgx (veloxpgx.Open) and
// through database/sql. Both are opened per test. The two targets share one
// sqlDB for setup, so within a test its INSERT statement stays cached across
// the second target's DROP and CREATE; that is harmless, because the schema is
// the same and PostgreSQL re-plans a statement whose table was replaced.
func postgresTargets(t testing.TB, dsn string, opts []velox.Option) []target {
	t.Helper()
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	exec := sqlExec(sqlDB, velox.Postgres)
	return []target{
		{name: "postgres-pgx", d: velox.Postgres, db: veloxpgx.Open(pool, opts...), exec: exec, ddl: pgDDL, items: pgItems, tickets: pgTickets, raw: sqlDB, direct: sqlDB},
		{name: "postgres-sql", d: velox.Postgres, db: velox.Open(velox.Postgres, sqlDB, opts...), exec: exec, ddl: pgDDL, items: pgItems, tickets: pgTickets, raw: sqlDB, direct: sqlDB},
	}
}

const pgItems = `CREATE TABLE items (id bigint PRIMARY KEY, invoice_id bigint NOT NULL, sku text NOT NULL, qty bigint NOT NULL)`

const pgTickets = `CREATE TABLE tickets (created_by text NOT NULL,
	id bigint GENERATED BY DEFAULT AS IDENTITY (START WITH 100) PRIMARY KEY, org_id bigint NOT NULL, owner text NOT NULL,
	title text NOT NULL, done boolean NOT NULL, invoice_id bigint NOT NULL, parent_id bigint,
	CONSTRAINT tickets_org_title_uq UNIQUE (org_id, title))`

const mysqlTickets = "CREATE TABLE tickets (created_by VARCHAR(64) NOT NULL, id BIGINT AUTO_INCREMENT PRIMARY KEY, " +
	"org_id BIGINT NOT NULL, owner VARCHAR(64) NOT NULL, title VARCHAR(64) NOT NULL, done BOOLEAN NOT NULL, " +
	"invoice_id BIGINT NOT NULL, parent_id BIGINT NULL, CONSTRAINT tickets_org_title_uq UNIQUE (org_id, title))"

const sqliteTickets = `CREATE TABLE tickets (created_by TEXT NOT NULL, id INTEGER PRIMARY KEY, org_id INTEGER NOT NULL,
	owner TEXT NOT NULL, title TEXT NOT NULL, done BOOLEAN NOT NULL, invoice_id INTEGER NOT NULL, parent_id INTEGER,
	CONSTRAINT tickets_org_title_uq UNIQUE (org_id, title))`

const mysqlItems = "CREATE TABLE items (id BIGINT PRIMARY KEY, invoice_id BIGINT NOT NULL, sku VARCHAR(64) NOT NULL, qty BIGINT NOT NULL)"

const sqliteItems = "CREATE TABLE items (id INTEGER PRIMARY KEY, invoice_id INTEGER NOT NULL, sku TEXT NOT NULL, qty INTEGER NOT NULL)"

const mysqlDDL = "CREATE TABLE invoices (created_at DATETIME(6) NOT NULL, updated_at DATETIME(6) NOT NULL, " +
	"id BIGINT AUTO_INCREMENT PRIMARY KEY, org_id BIGINT NOT NULL, number VARCHAR(64) NOT NULL, total DOUBLE NOT NULL, " +
	"paid BOOLEAN NOT NULL, paid_at DATETIME(6) NULL, note TEXT NOT NULL, data BLOB NOT NULL, " +
	"CONSTRAINT invoices_org_number_uq UNIQUE (org_id, number))"

func mysqlTarget(t testing.TB, dsn string, opts []velox.Option) target {
	t.Helper()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return target{name: "mysql", d: velox.MySQL, db: velox.Open(velox.MySQL, db, opts...), exec: sqlExec(db, velox.MySQL), ddl: mysqlDDL, items: mysqlItems,
		tickets: mysqlTickets, raw: db, direct: db}
}

func sqliteTarget(t testing.TB, opts []velox.Option) target {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1) // one connection keeps the in-memory database alive
	t.Cleanup(func() { _ = db.Close() })
	return target{
		name: "sqlite", d: velox.SQLite, db: velox.Open(velox.SQLite, db, opts...), exec: sqlExec(db, velox.SQLite),
		items: sqliteItems, tickets: sqliteTickets, direct: db,
		ddl: `CREATE TABLE invoices (created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL, id INTEGER PRIMARY KEY,
			org_id INTEGER NOT NULL, number TEXT NOT NULL, total REAL NOT NULL, paid BOOLEAN NOT NULL,
			paid_at DATETIME, note TEXT NOT NULL, data BLOB NOT NULL, CONSTRAINT invoices_org_number_uq UNIQUE (org_id, number))`,
	}
}

func sqlExec(db *sql.DB, d velox.Dialect) func(testing.TB, string, ...any) {
	return func(t testing.TB, q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, rebind(d, q), args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

// rebind turns ? placeholders into $n for PostgreSQL.
func rebind(d velox.Dialect, q string) string {
	if d != velox.Postgres {
		return q
	}
	var b strings.Builder
	n := 0
	for i := range len(q) {
		if q[i] == '?' {
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
			continue
		}
		b.WriteByte(q[i])
	}
	return b.String()
}

func TestRebind(t *testing.T) {
	const q = "a = ? AND b IN (?, ?)"
	for _, c := range []struct {
		d    velox.Dialect
		want string
	}{
		{velox.Postgres, "a = $1 AND b IN ($2, $3)"},
		{velox.MySQL, q},
		{velox.SQLite, q},
	} {
		if got := rebind(c.d, q); got != c.want {
			t.Errorf("rebind(%v) = %q, want %q", c.d, got, c.want)
		}
	}
	if got := rebind(velox.Postgres, "no placeholders"); got != "no placeholders" {
		t.Errorf("rebind without placeholders = %q", got)
	}
}

// setup creates the invoices table with five rows, the items table with
// three and the tickets table with eight. Org 1 owns invoice ids 1–4. The
// numbers hold LIKE wildcards and a backslash; even ids have a note, and odd
// ones none (note is Optional, a NOT NULL column: "" means unset); paid
// invoices have paid_at. The items are (id, invoice, sku, qty): (1, 1, A, 2),
// (2, 1, B, 0), (3, 2, C, 5). The tickets are in seedTickets.
func setup(t testing.TB, tg target) {
	t.Helper()
	tg.exec(t, "DROP TABLE IF EXISTS tickets")
	tg.exec(t, "DROP TABLE IF EXISTS items")
	tg.exec(t, "DROP TABLE IF EXISTS invoices")
	tg.exec(t, tg.ddl)
	tg.exec(t, tg.items)
	tg.exec(t, tg.tickets)
	for _, r := range []struct {
		id, org int64
		number  string
		total   float64
		paid    bool
	}{
		{1, 1, "INV-1", 10, true}, {2, 1, "INV-2", 20, false}, {3, 1, "50%_OFF", 30, true},
		{4, 1, "500X", 40, false}, {5, 2, `back\slash`, 50, true},
	} {
		var paidAt *time.Time
		if r.paid {
			paidAt = &t0
		}
		n := ""
		if r.id%2 == 0 {
			n = "a note"
		}
		tg.exec(t, "INSERT INTO invoices (created_at, updated_at, id, org_id, number, total, paid, paid_at, note, data) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			t0, t0, r.id, r.org, r.number, r.total, r.paid, paidAt, n, []byte("d"))
	}
	for _, r := range []struct {
		id, invoice int64
		sku         string
		qty         int64
	}{{1, 1, "A", 2}, {2, 1, "B", 0}, {3, 2, "C", 5}} {
		tg.exec(t, "INSERT INTO items (id, invoice_id, sku, qty) VALUES (?, ?, ?, ?)", r.id, r.invoice, r.sku, r.qty)
	}
	for _, r := range seedTickets {
		tg.exec(t, "INSERT INTO tickets (created_by, id, org_id, owner, title, done, invoice_id, parent_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
			"seed", r.id, r.org, r.owner, r.title, r.done, r.invoice, r.parent)
	}
}

// each runs f on every target, opened with opts.
func each(t *testing.T, f func(t *testing.T, tg target), opts ...velox.Option) {
	for _, tg := range targets(t, opts) {
		t.Run(tg.name, func(t *testing.T) {
			setup(t, tg)
			f(t, tg)
		})
	}
}

func ids(xs []testmodel.Invoice) []int64 {
	out := make([]int64, len(xs))
	for i := range xs {
		out[i] = xs[i].ID
	}
	return out
}

func query(t *testing.T, tg target, ps ...velox.Pred[testmodel.Invoice]) []int64 {
	t.Helper()
	got, err := tg.db.Query[testmodel.Invoice]().Where(ps...).OrderBy(I.ID.Asc()).Unbounded().All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return ids(got)
}

func wantStrings(t *testing.T, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func wantIDs(t *testing.T, got, want []int64) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("ids %v, want %v", got, want)
	}
}

func TestFilterOrderLimit(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		got, err := tg.db.Query[testmodel.Invoice]().Where(I.OrgID.EQ(1)).OrderBy(I.Total.Desc()).Limit(2).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		wantIDs(t, ids(got), []int64{4, 3})
	})
}

func TestOneScansEveryType(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		two, err := tg.db.Query[testmodel.Invoice]().Where(I.ID.EQ(2)).One(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if two.Number != "INV-2" || two.Total != 20 || two.Paid || two.PaidAt != nil ||
			two.Note != "a note" || string(two.Data) != "d" || !two.CreatedAt.Equal(t0) || two.OrgID != 1 {
			t.Errorf("%+v", two)
		}
		three, err := tg.db.Query[testmodel.Invoice]().Where(I.ID.EQ(3)).One(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !three.Paid || three.PaidAt == nil || !three.PaidAt.Equal(t0) || three.Note != "" {
			t.Errorf("%+v", three)
		}
	})
}

func TestOneErrors(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		if _, err := tg.db.Query[testmodel.Invoice]().Where(I.ID.EQ(99)).One(ctx); !errors.Is(err, velox.ErrNotFound) {
			t.Errorf("no row: %v", err)
		}
		if _, err := tg.db.Query[testmodel.Invoice]().Where(I.OrgID.EQ(1)).One(ctx); !errors.Is(err, velox.ErrNotSingular) {
			t.Errorf("four rows: %v", err)
		}
	})
}

func TestFirstAndExistsOnRealDatabases(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		q := tg.db.Query[testmodel.Invoice]()
		last, err := q.OrderBy(I.ID.Desc()).First(ctx)
		if err != nil || last.ID != 5 {
			t.Fatalf("First: %+v, %v", last, err)
		}
		if _, err := q.Where(I.ID.EQ(99)).First(ctx); !errors.Is(err, velox.ErrNotFound) {
			t.Errorf("First, no row: %v", err)
		}
		if ok, err := q.Where(I.Paid.EQ(true)).Exists(ctx); err != nil || !ok {
			t.Errorf("Exists, a row: %v, %v", ok, err)
		}
		if ok, err := q.Where(I.ID.EQ(99)).Exists(ctx); err != nil || ok {
			t.Errorf("Exists, no row: %v, %v", ok, err)
		}
	})
}

// TestLikeTakesTextLiterally: HasPrefix, HasSuffix and Contains match %, _,
// ! and \ as themselves, and Like takes % and _ as wildcards and ! as its
// escape, while \ is an ordinary character.
func TestLikeTakesTextLiterally(t *testing.T) {
	each(t, likeChecks)
}

// TestLikeIgnoresServerSettings runs the LIKE checks on sessions whose
// settings change what a backslash means in a string literal: MySQL with
// NO_BACKSLASH_ESCAPES in sql_mode, and PostgreSQL with
// standard_conforming_strings off. The ESCAPE character is not a backslash,
// so neither changes a match.
func TestLikeIgnoresServerSettings(t *testing.T) {
	var ts []target
	if dsn := os.Getenv("VELOX_TEST_POSTGRES"); dsn != "" {
		ts = append(ts, nonStandardPostgres(t, dsn)...)
	}
	if dsn := os.Getenv("VELOX_TEST_MYSQL"); dsn != "" {
		ts = append(ts, noBackslashMySQL(t, dsn))
	}
	if len(ts) == 0 {
		t.Skip("needs VELOX_TEST_POSTGRES or VELOX_TEST_MYSQL")
	}
	for _, tg := range ts {
		t.Run(tg.name, func(t *testing.T) {
			setup(t, tg)
			likeChecks(t, tg)
		})
	}
}

// likeChecks adds invoices 6 (x!y) and 7 (x!%y) to setup's rows, whose
// numbers already hold 50%_OFF and back\slash, and matches them.
func likeChecks(t *testing.T, tg target) {
	for _, r := range []struct {
		id     int64
		number string
	}{{6, "x!y"}, {7, "x!%y"}} {
		tg.exec(t, "INSERT INTO invoices (created_at, updated_at, id, org_id, number, total, paid, paid_at, note, data) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			t0, t0, r.id, 2, r.number, 1.0, false, nil, "", []byte("d"))
	}
	wantIDs(t, query(t, tg, I.Number.HasPrefix("50%")), []int64{3})
	wantIDs(t, query(t, tg, I.Number.HasSuffix("_OFF")), []int64{3})
	wantIDs(t, query(t, tg, I.Number.HasSuffix("_1")), nil) // unescaped, "%_1" would match INV-1
	wantIDs(t, query(t, tg, I.Number.Contains("_")), []int64{3})
	wantIDs(t, query(t, tg, I.Number.Contains(`\`)), []int64{5})
	wantIDs(t, query(t, tg, I.Number.HasPrefix(`back\`)), []int64{5})
	wantIDs(t, query(t, tg, I.Number.HasSuffix(`\slash`)), []int64{5})
	wantIDs(t, query(t, tg, I.Number.Contains("!")), []int64{6, 7})
	wantIDs(t, query(t, tg, I.Number.HasPrefix("x!%")), []int64{7})
	wantIDs(t, query(t, tg, I.Number.HasSuffix("!y")), []int64{6})
	wantIDs(t, query(t, tg, I.Number.Contains("!%")), []int64{7})
	wantIDs(t, query(t, tg, I.Number.Like("INV-_")), []int64{1, 2})
	wantIDs(t, query(t, tg, I.Number.Like("50!%!_OFF")), []int64{3})
	wantIDs(t, query(t, tg, I.Number.Like("x!!%")), []int64{6, 7})
	wantIDs(t, query(t, tg, I.Number.Like("x!!!%y")), []int64{7})
	wantIDs(t, query(t, tg, I.Number.Like(`back\slash`)), []int64{5})
	wantIDs(t, query(t, tg, I.Number.Like(`back\_lash`)), []int64{5})
}

// nonStandardPostgres returns PostgreSQL through pgx and database/sql, each
// session opened with standard_conforming_strings off, which makes a
// backslash an escape in an ordinary string literal.
func nonStandardPostgres(t *testing.T, dsn string) []target {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["standard_conforming_strings"] = "off"
	sqlDB := stdlib.OpenDB(*cfg.ConnConfig.Copy())
	t.Cleanup(func() { _ = sqlDB.Close() })
	var scs string
	if err := sqlDB.QueryRowContext(ctx, "SHOW standard_conforming_strings").Scan(&scs); err != nil {
		t.Fatal(err)
	}
	if scs != "off" {
		t.Fatalf("standard_conforming_strings is %q, want off", scs)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	exec := sqlExec(sqlDB, velox.Postgres)
	return []target{
		{name: "postgres-pgx-nonstandard", d: velox.Postgres, db: veloxpgx.Open(pool), exec: exec, ddl: pgDDL, items: pgItems, tickets: pgTickets, raw: sqlDB, direct: sqlDB},
		{name: "postgres-sql-nonstandard", d: velox.Postgres, db: velox.Open(velox.Postgres, sqlDB), exec: exec, ddl: pgDDL, items: pgItems, tickets: pgTickets, raw: sqlDB, direct: sqlDB},
	}
}

// noBackslashMySQL returns MySQL with NO_BACKSLASH_ESCAPES added to the
// session's sql_mode, which makes a backslash an ordinary character in a
// string literal.
func noBackslashMySQL(t *testing.T, dsn string) target {
	t.Helper()
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Params == nil {
		cfg.Params = map[string]string{}
	}
	cfg.Params["sql_mode"] = "CONCAT(@@sql_mode, ',NO_BACKSLASH_ESCAPES')"
	conn, err := mysql.NewConnector(cfg)
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(conn)
	t.Cleanup(func() { _ = db.Close() })
	var mode string
	if err := db.QueryRowContext(ctx, "SELECT @@SESSION.sql_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mode, "NO_BACKSLASH_ESCAPES") {
		t.Fatalf("sql_mode is %q, want NO_BACKSLASH_ESCAPES in it", mode)
	}
	return target{name: "mysql-no-backslash-escapes", d: velox.MySQL, db: velox.Open(velox.MySQL, db), exec: sqlExec(db, velox.MySQL), ddl: mysqlDDL, items: mysqlItems,
		tickets: mysqlTickets, raw: db, direct: db}
}

// TestTextMatchingFollowsTheDatabase is Review Focus 2: whether LIKE ignores
// case is the database's choice, and the handles' documentation says so.
func TestTextMatchingFollowsTheDatabase(t *testing.T) {
	want := map[velox.Dialect][]int64{velox.Postgres: nil, velox.MySQL: {1, 2}, velox.SQLite: {1, 2}}
	each(t, func(t *testing.T, tg target) {
		w, ok := want[tg.d]
		if !ok {
			t.Fatalf("no expectation for dialect %v", tg.d)
		}
		wantIDs(t, query(t, tg, I.Number.HasPrefix("inv")), w)
		wantIDs(t, query(t, tg, I.Number.EqualFold("inv-1")), []int64{1})
	})
}

func TestEmptyLists(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		wantIDs(t, query(t, tg, I.ID.In()), nil)
		wantIDs(t, query(t, tg, I.OrgID.EQ(1), I.ID.NotIn()), []int64{1, 2, 3, 4})
	})
}

func TestLogicAndNull(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		wantIDs(t, query(t, tg, velox.Or(I.ID.EQ(1), I.ID.EQ(5)), velox.Not(I.Paid.EQ(false))), []int64{1, 5})
		wantIDs(t, query(t, tg, I.Note.EQ("")), []int64{1, 3, 5}) // an Optional value: "" is unset
		wantIDs(t, query(t, tg, I.PaidAt.NotNull()), []int64{1, 3, 5})
		wantIDs(t, query(t, tg, I.Total.Between(20, 40)), []int64{2, 3, 4})
	})
}

func TestCountOffsetColumnsIter(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		n, err := tg.db.Query[testmodel.Invoice]().Where(I.OrgID.EQ(1)).Count(ctx)
		if err != nil || n != 4 {
			t.Errorf("count %d, %v", n, err)
		}
		got, err := tg.db.Query[testmodel.Invoice]().OrderBy(I.ID.Asc()).Offset(3).Unbounded().All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		wantIDs(t, ids(got), []int64{4, 5})
		some, err := tg.db.Query[testmodel.Invoice]().Columns(I.ID, I.Number).Where(I.ID.EQ(3)).One(ctx)
		if err != nil || some.Number != "50%_OFF" || some.Total != 0 || some.Data != nil {
			t.Errorf("%+v, %v", some, err)
		}
		var seen int
		for _, err := range tg.db.Query[testmodel.Invoice]().Unbounded().Iter(ctx) {
			if err != nil {
				t.Fatal(err)
			}
			if seen++; seen == 2 {
				break
			}
		}
		if seen != 2 {
			t.Errorf("Iter yielded %d rows before the break, want 2", seen)
		}
		// The early break must release the connection. SQLite has only one, so a
		// leak makes this query wait out the timeout and fail rather than hang.
		tctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if n, err := tg.db.Query[testmodel.Invoice]().Count(tctx); err != nil || n != 5 {
			t.Errorf("query after an early break: %d, %v", n, err)
		}
	})
}

// TestAroundDeadlineCoversReadingTheRows: an around hook that gives the
// statement a deadline, cancelled when next returns, must not cut off reading
// its rows: next reads them all before it returns.
func TestAroundDeadlineCoversReadingTheRows(t *testing.T) {
	deadline := velox.WithAround(func(ctx context.Context, _ *velox.Statement, next func(context.Context) error) error {
		ctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		return next(ctx)
	})
	each(t, func(t *testing.T, tg target) {
		for i := range 300 {
			got, err := tg.db.Query[testmodel.Invoice]().OrderBy(I.ID.Asc()).Unbounded().All(ctx)
			if err != nil || len(got) != 5 {
				t.Fatalf("query %d, All: %d rows, %v", i, len(got), err)
			}
			if n, err := tg.db.Query[testmodel.Invoice]().Count(ctx); err != nil || n != 5 {
				t.Fatalf("query %d, Count: %d, %v", i, n, err)
			}
			seen := 0
			for _, err := range tg.db.Query[testmodel.Invoice]().Unbounded().Iter(ctx) {
				if err != nil {
					t.Fatalf("query %d, Iter: %v", i, err)
				}
				seen++
			}
			if seen != 5 {
				t.Fatalf("query %d, Iter: %d rows", i, seen)
			}
		}
	}, deadline)
}

// TestConnect opens each server by its driver's name, as an application does.
func TestConnect(t *testing.T) {
	for _, c := range []struct{ driver, env string }{
		{"pgx", "VELOX_TEST_POSTGRES"},
		{"mysql", "VELOX_TEST_MYSQL"},
	} {
		t.Run(c.driver, func(t *testing.T) {
			dsn := os.Getenv(c.env)
			if dsn == "" {
				t.Skip(c.env + " is not set")
			}
			db, err := velox.Connect(c.driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			pool := velox.Pool(db)
			if err := pool.PingContext(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			if err := pool.PingContext(context.Background()); err == nil {
				t.Error("the pool is open after Close")
			}
		})
	}
}
