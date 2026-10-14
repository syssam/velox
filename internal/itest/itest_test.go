// Package itest runs velox against real databases: SQLite always, PostgreSQL
// and MySQL when VELOX_TEST_POSTGRES and VELOX_TEST_MYSQL name them
// (scripts/ci-local.sh starts both and sets the variables).
package itest

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

var (
	ctx = context.Background()
	t0  = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	I   = testmodel.Invoices
)

// target is one database the tests run against.
type target struct {
	name string
	d    velox.Dialect
	db   *velox.DB
	exec func(t *testing.T, query string, args ...any) // setup SQL, written with ? placeholders
	ddl  string
}

// targets returns SQLite always, and PostgreSQL and MySQL when their
// environment variable is set. An unset variable omits the database; it never
// skips a test, so the SQLite subtests always run.
func targets(t *testing.T) []target {
	t.Helper()
	ts := []target{sqliteTarget(t)}
	if dsn := os.Getenv("VELOX_TEST_POSTGRES"); dsn != "" {
		ts = append(ts, postgresTargets(t, dsn)...)
	}
	if dsn := os.Getenv("VELOX_TEST_MYSQL"); dsn != "" {
		ts = append(ts, mysqlTarget(t, dsn))
	}
	return ts
}

const pgDDL = `CREATE TABLE invoices (created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL,
	id bigint PRIMARY KEY, org_id bigint NOT NULL, number text NOT NULL, total double precision NOT NULL,
	paid boolean NOT NULL, paid_at timestamptz, note text, data bytea NOT NULL)`

// postgresTargets returns PostgreSQL twice: through native pgx (OpenPgx) and
// through database/sql. Both are opened per test, so no connection keeps a
// statement cached across the DROP and CREATE of setup.
func postgresTargets(t *testing.T, dsn string) []target {
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
		{name: "postgres-pgx", d: velox.Postgres, db: velox.OpenPgx(pool), exec: exec, ddl: pgDDL},
		{name: "postgres-sql", d: velox.Postgres, db: velox.Open(velox.Postgres, sqlDB), exec: exec, ddl: pgDDL},
	}
}

const mysqlDDL = "CREATE TABLE invoices (created_at DATETIME(6) NOT NULL, updated_at DATETIME(6) NOT NULL, " +
	"id BIGINT PRIMARY KEY, org_id BIGINT NOT NULL, number VARCHAR(64) NOT NULL, total DOUBLE NOT NULL, " +
	"paid BOOLEAN NOT NULL, paid_at DATETIME(6) NULL, note TEXT NULL, data BLOB NOT NULL)"

func mysqlTarget(t *testing.T, dsn string) target {
	t.Helper()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return target{name: "mysql", d: velox.MySQL, db: velox.Open(velox.MySQL, db), exec: sqlExec(db, velox.MySQL), ddl: mysqlDDL}
}

func sqliteTarget(t *testing.T) target {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1) // one connection keeps the in-memory database alive
	t.Cleanup(func() { _ = db.Close() })
	return target{
		name: "sqlite", d: velox.SQLite, db: velox.Open(velox.SQLite, db), exec: sqlExec(db, velox.SQLite),
		ddl: `CREATE TABLE invoices (created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL, id INTEGER PRIMARY KEY,
			org_id INTEGER NOT NULL, number TEXT NOT NULL, total REAL NOT NULL, paid BOOLEAN NOT NULL,
			paid_at DATETIME, note TEXT, data BLOB NOT NULL)`,
	}
}

func sqlExec(db *sql.DB, d velox.Dialect) func(*testing.T, string, ...any) {
	return func(t *testing.T, q string, args ...any) {
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

// setup creates the invoices table with five rows. Org 1 owns ids 1–4. The
// numbers hold LIKE wildcards and a backslash; even ids have a note; paid
// invoices have paid_at.
func setup(t *testing.T, tg target) {
	t.Helper()
	tg.exec(t, "DROP TABLE IF EXISTS invoices")
	tg.exec(t, tg.ddl)
	note := "a note"
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
		var n *string
		if r.id%2 == 0 {
			n = &note
		}
		tg.exec(t, "INSERT INTO invoices (created_at, updated_at, id, org_id, number, total, paid, paid_at, note, data) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			t0, t0, r.id, r.org, r.number, r.total, r.paid, paidAt, n, []byte("d"))
	}
}

func each(t *testing.T, f func(t *testing.T, tg target)) {
	for _, tg := range targets(t) {
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
	got, err := tg.db.Query[testmodel.Invoice]().Where(ps...).OrderBy(I.ID.Asc()).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return ids(got)
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
		if two.Number != "INV-2" || two.Total != 20 || two.Paid || two.PaidAt != nil || two.Note == nil ||
			*two.Note != "a note" || string(two.Data) != "d" || !two.CreatedAt.Equal(t0) || two.OrgID != 1 {
			t.Errorf("%+v", two)
		}
		three, err := tg.db.Query[testmodel.Invoice]().Where(I.ID.EQ(3)).One(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !three.Paid || three.PaidAt == nil || !three.PaidAt.Equal(t0) || three.Note != nil {
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

// TestLikeTakesTextLiterally is Review Focus 1.
func TestLikeTakesTextLiterally(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		wantIDs(t, query(t, tg, I.Number.HasPrefix("50%")), []int64{3})
		wantIDs(t, query(t, tg, I.Number.HasSuffix("_OFF")), []int64{3})
		wantIDs(t, query(t, tg, I.Number.HasSuffix("_1")), nil) // unescaped, "%_1" would match INV-1
		wantIDs(t, query(t, tg, I.Number.Contains("_")), []int64{3})
		wantIDs(t, query(t, tg, I.Number.Contains(`\`)), []int64{5})
		wantIDs(t, query(t, tg, I.Number.Like("INV-_")), []int64{1, 2})
	})
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
		wantIDs(t, query(t, tg, I.Note.IsNull()), []int64{1, 3, 5})
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
		got, err := tg.db.Query[testmodel.Invoice]().OrderBy(I.ID.Asc()).Offset(3).All(ctx)
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
