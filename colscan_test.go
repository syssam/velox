package velox_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/bindmodel/bindtype"
	"github.com/syssam/velox2/internal/testmodel"
	"github.com/syssam/velox2/internal/testmodel/kind"
	_ "modernc.org/sqlite"
)

// unreachablePG is a PostgreSQL DSN that nothing listens on: the tests that
// use it never connect.
const unreachablePG = "postgres://u@127.0.0.1:1/x"

// wrapConnector is a connector that wraps pgx's, as a tracing driver does.
// It is its own driver.
type wrapConnector struct{ driver.Connector }

func (w wrapConnector) Driver() driver.Driver { return w }

func (wrapConnector) Open(string) (driver.Conn, error) {
	return nil, errors.New("wrapConnector: open through the connector")
}

func openSQL(t *testing.T, name, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open(name, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestPgxStdlibDetected(t *testing.T) {
	cfg, err := pgx.ParseConfig(unreachablePG)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(context.Background(), unreachablePG)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	for _, c := range []struct {
		name string
		db   *sql.DB
		want bool
	}{
		{"sql.Open pgx", openSQL(t, "pgx", unreachablePG), true},
		{"stdlib.OpenDB", stdlib.OpenDB(*cfg), true},
		{"sql.OpenDB GetConnector", sql.OpenDB(stdlib.GetConnector(*cfg)), true},
		{"stdlib.OpenDBFromPool", stdlib.OpenDBFromPool(pool), true},
		{"sqlite", openSQL(t, "sqlite", ":memory:"), false},
		{"wrapped connector", sql.OpenDB(wrapConnector{stdlib.GetConnector(*cfg)}), false},
	} {
		t.Cleanup(func() { _ = c.db.Close() })
		if got := velox.PgxStdlib(c.db); got != c.want {
			t.Errorf("%s: PgxStdlib = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestSQLConnDetects(t *testing.T) {
	if !velox.ConnColumnScanning(velox.SQLConn(openSQL(t, "pgx", unreachablePG))) {
		t.Error("SQLConn of a pgx stdlib DB does not scan through colRows")
	}
	if velox.ConnColumnScanning(velox.SQLConn(openSQL(t, "sqlite", ":memory:"))) {
		t.Error("SQLConn of a SQLite DB scans through colRows")
	}
}

func TestSQLiteAndMySQLKeepPlainRows(t *testing.T) {
	if velox.RowsAreColumnScanning(velox.Open(velox.SQLite, openSQL(t, "sqlite", ":memory:"))) {
		t.Error("SQLite's rows are colRows")
	}
	dsn := os.Getenv("VELOX_TEST_MYSQL")
	if dsn == "" {
		return
	}
	if velox.RowsAreColumnScanning(velox.Open(velox.MySQL, openSQL(t, "mysql", dsn))) {
		t.Error("MySQL's rows are colRows")
	}
}

// scanOne runs sql on db and scans its first row into dst.
func scanOne(db *velox.DB, sql string, dst ...any) (err error) {
	r, err := velox.RawRows(db, sql, nil)
	if err != nil {
		return err
	}
	defer r.Close()
	if !r.Next() {
		return errors.New("no row")
	}
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v", p)
		}
	}()
	return r.Scan(dst...)
}

func TestColumnScanRowsKeepCallerSlice(t *testing.T) {
	raw := openSQL(t, "sqlite", "file::memory:")
	raw.SetMaxOpenConns(1)
	wrapped, plain := velox.OpenColumnScan(velox.SQLite, raw, true), velox.OpenColumnScan(velox.SQLite, raw, false)

	r, err := velox.RawRows(wrapped, "SELECT 'a' UNION ALL SELECT 'b' UNION ALL SELECT 'c' UNION ALL SELECT 'd'", nil)
	if err != nil {
		t.Fatal(err)
	}
	var s string
	dst := []any{&s}
	if !r.Next() {
		t.Fatal("no first row")
	}
	if err := r.Scan(dst...); err != nil {
		t.Fatal(err)
	}
	if dst[0] != any(&s) || s != "a" {
		t.Errorf("row 1: dst[0] = %v, s = %q; want &s and \"a\"", dst[0], s)
	}
	var st kind.Status
	if !r.Next() {
		t.Fatal("no second row")
	}
	if err := r.Scan([]any{&st}...); err != nil {
		t.Fatal(err)
	}
	if st != "b" {
		t.Errorf("row 2: st = %q, want \"b\"", st)
	}
	// Row 3's destination is not wrapped, and row 4's is again: the column's
	// wrap flag turns off and back on.
	var ns sql.NullString
	if !r.Next() {
		t.Fatal("no third row")
	}
	if err := r.Scan([]any{&ns}...); err != nil {
		t.Fatal(err)
	}
	if ns != (sql.NullString{String: "c", Valid: true}) {
		t.Errorf("row 3: ns = %#v, want a valid \"c\"", ns)
	}
	var s4 string
	if !r.Next() {
		t.Fatal("no fourth row")
	}
	if err := r.Scan([]any{&s4}...); err != nil {
		t.Fatal(err)
	}
	if s4 != "d" {
		t.Errorf("row 4: s4 = %q, want \"d\"", s4)
	}
	r.Close()

	for _, d := range []any{nil, (*time.Time)(nil)} {
		werr := scanOne(wrapped, "SELECT 'a'", d)
		perr := scanOne(plain, "SELECT 'a'", d)
		if werr == nil || perr == nil || werr.Error() != perr.Error() {
			t.Errorf("%T: wrapped error %v, plain error %v; want the same error", d, werr, perr)
		}
	}

	dsn := os.Getenv("VELOX_TEST_POSTGRES")
	if dsn == "" {
		return
	}
	pg := openSQL(t, "pgx", dsn)
	for _, wrap := range []bool{true, false} {
		err := scanOne(velox.OpenColumnScan(velox.Postgres, pg, wrap), "SELECT 'a'", (*time.Time)(nil))
		if err == nil || !strings.Contains(err.Error(), "destination pointer is nil") {
			t.Errorf("PostgreSQL, wrap %v: error %v, want destination pointer is nil", wrap, err)
		}
	}
}

func TestColumnScanAllocsPerQuery(t *testing.T) {
	raw := openSQL(t, "sqlite", "file::memory:")
	raw.SetMaxOpenConns(1)
	if _, err := raw.Exec(`CREATE TABLE invoices (created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL, id INTEGER PRIMARY KEY,
			org_id INTEGER NOT NULL, number TEXT NOT NULL, total REAL NOT NULL, paid BOOLEAN NOT NULL,
			paid_at DATETIME, note TEXT NOT NULL, data BLOB NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for id := int64(1); id <= 100; id++ {
		if _, err := raw.Exec(`INSERT INTO invoices (created_at, updated_at, id, org_id, number, total, paid, paid_at, note, data)
			VALUES (?, ?, ?, 1, ?, 0, false, ?, '', ?)`, t0, t0, id, fmt.Sprintf("N%d", id), t0, []byte("d")); err != nil {
			t.Fatal(err)
		}
	}
	ctx := velox.WithAllTenants(context.Background(), "test")
	allocs := func(wrap bool, limit int) float64 {
		db := velox.OpenColumnScan(velox.SQLite, raw, wrap)
		return testing.AllocsPerRun(50, func() {
			invs, err := db.Query[testmodel.Invoice]().Limit(limit).All(ctx)
			if err != nil || len(invs) != limit {
				t.Fatalf("limit %d: %d invoices, %v", limit, len(invs), err)
			}
		})
	}
	w1, p1 := allocs(true, 1), allocs(false, 1)
	w100, p100 := allocs(true, 100), allocs(false, 100)
	t.Logf("1 row: wrapped %v, plain %v; 100 rows: wrapped %v, plain %v", w1, p1, w100, p100)
	if w1-p1 > 3 {
		t.Errorf("1 row: wrapped %v allocations, plain %v; want at most 3 more", w1, p1)
	}
	d1, d100 := w1-p1, w100-p100
	if raceEnabled {
		if d100 < d1-1 || d100 > d1+1 {
			t.Errorf("wrapped minus plain: %v for 1 row, %v for 100; want within 1", d1, d100)
		}
	} else if d100 != d1 {
		t.Errorf("wrapped minus plain: %v for 1 row, %v for 100; want the same", d1, d100)
	}
}

// matchMoment is a struct type that converts to time.Time, with no methods.
type matchMoment time.Time

// matchDests are the destination types TestColumnScanMatchesPlain scans
// into: the wrapped types that tests outside the package can name, and
// destinations that are left unwrapped.
func matchDests() []reflect.Type {
	return []reflect.Type{
		reflect.TypeFor[*int](),
		reflect.TypeFor[*int8](),
		reflect.TypeFor[*int16](),
		reflect.TypeFor[*int32](),
		reflect.TypeFor[*int64](),
		reflect.TypeFor[*uint](),
		reflect.TypeFor[*uint8](),
		reflect.TypeFor[*uint16](),
		reflect.TypeFor[*uint32](),
		reflect.TypeFor[*uint64](),
		reflect.TypeFor[*float32](),
		reflect.TypeFor[*float64](),
		reflect.TypeFor[*bool](),
		reflect.TypeFor[*string](),
		reflect.TypeFor[*[]byte](),
		reflect.TypeFor[*time.Time](),
		reflect.TypeFor[**string](),
		reflect.TypeFor[**int64](),
		reflect.TypeFor[**time.Time](),
		reflect.TypeFor[**float64](),
		reflect.TypeFor[**bool](),
		reflect.TypeFor[*json.RawMessage](),
		reflect.TypeFor[*kind.Status](),
		reflect.TypeFor[**kind.Status](),
		reflect.TypeFor[*kind.Cents](),
		reflect.TypeFor[*matchMoment](),
		reflect.TypeFor[**matchMoment](),
		reflect.TypeFor[*kind.Moment](),
		reflect.TypeFor[**kind.Moment](),
		reflect.TypeFor[*bindtype.UUID](),
		reflect.TypeFor[*bindtype.Level](),
		reflect.TypeFor[*sql.NullInt64](),
		reflect.TypeFor[*any](),
	}
}

// sameTimeZone reports whether a and b, scanned destinations that agree,
// hold times of the same location, when they hold times.
func sameTimeZone(a, b reflect.Value) bool {
	for a.Kind() == reflect.Pointer {
		if a.IsNil() || b.IsNil() {
			return true
		}
		a, b = a.Elem(), b.Elem()
	}
	if a.Kind() == reflect.Interface {
		if a.IsNil() || b.IsNil() {
			return true
		}
		a, b = a.Elem(), b.Elem()
	}
	timeType := reflect.TypeFor[time.Time]()
	if a.Kind() != reflect.Struct || !a.Type().ConvertibleTo(timeType) {
		return true
	}
	ta, tb := a.Convert(timeType).Interface().(time.Time), b.Convert(timeType).Interface().(time.Time)
	return ta.Location().String() == tb.Location().String()
}

func TestColumnScanMatchesPlain(t *testing.T) {
	common := []string{
		"127", "128", "255", "256", "-1", "32768", "2147483648", "9223372036854775807",
		"1.5", "1e300", "true", "false", "'abc'", "'42'", "''", "NULL",
	}
	type target struct {
		name  string
		d     velox.Dialect
		db    *sql.DB
		exprs []string
	}
	sqlite := openSQL(t, "sqlite", "file::memory:")
	sqlite.SetMaxOpenConns(1)
	targets := []target{{"sqlite", velox.SQLite, sqlite,
		append(append([]string{}, common...), "x'00ff'", "'2026-10-29 01:02:03.456789+08'")}}
	if dsn := os.Getenv("VELOX_TEST_POSTGRES"); dsn != "" {
		targets = append(targets, target{"postgres", velox.Postgres, openSQL(t, "pgx", dsn),
			append(append([]string{}, common...), `'\x00ff'::bytea`, "'2026-10-29 01:02:03.456789+08'::timestamptz")})
	} else if os.Getenv("VELOX_ITEST_REQUIRE") == "1" {
		t.Fatal("VELOX_ITEST_REQUIRE=1 and VELOX_TEST_POSTGRES is unset")
	}
	for _, tg := range targets {
		wrapped, plain := velox.OpenColumnScan(tg.d, tg.db, true), velox.OpenColumnScan(tg.d, tg.db, false)
		cells := 0
		for _, expr := range tg.exprs {
			for _, typ := range matchDests() {
				q := "SELECT " + expr
				w, p := reflect.New(typ.Elem()), reflect.New(typ.Elem())
				werr := scanOne(wrapped, q, w.Interface())
				perr := scanOne(plain, q, p.Interface())
				cells++
				switch {
				case werr != nil || perr != nil:
					if werr == nil || perr == nil || werr.Error() != perr.Error() {
						t.Errorf("%s: %s into %s: wrapped error %v, plain error %v", tg.name, q, typ, werr, perr)
					}
				case !reflect.DeepEqual(w.Elem().Interface(), p.Elem().Interface()):
					t.Errorf("%s: %s into %s: wrapped %#v, plain %#v", tg.name, q, typ, w.Elem().Interface(), p.Elem().Interface())
				case !sameTimeZone(w.Elem(), p.Elem()):
					t.Errorf("%s: %s into %s: the times' locations differ", tg.name, q, typ)
				}
			}
		}
		t.Logf("%s: %d cells compared", tg.name, cells)
		if tg.d == velox.Postgres && cells < 400 {
			t.Errorf("%s: %d cells compared, want at least 400", tg.name, cells)
		}
	}
}
