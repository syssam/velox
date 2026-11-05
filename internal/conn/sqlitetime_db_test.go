package conn_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox/internal/conn"
)

// timeDB is a SQLite database with a table t of times, declared velox's way,
// datetime(9), and the old way, datetime; and its executor.
func timeDB(t *testing.T) (*sql.DB, conn.Executor) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE t (id integer PRIMARY KEY, at datetime(9) NOT NULL, maybe datetime(9), old datetime NOT NULL, label text NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	return db, conn.SQLiteTimes(conn.NewSQL(db))
}

// TestSQLiteDriverLeavesDatetime9AsText pins what velox's reading rests on:
// the driver parses a column declared exactly datetime into a time.Time, by a
// first parse that fails on velox's form, and hands over a datetime(9)
// column's text as it is.
func TestSQLiteDriverLeavesDatetime9AsText(t *testing.T) {
	db, ex := timeDB(t)
	at := time.Date(2026, 11, 1, 12, 0, 0, 5, time.UTC)
	if _, err := ex.Exec(context.Background(), `INSERT INTO t (at, old, label) VALUES (?, ?, 'x')`, []any{at, at}); err != nil {
		t.Fatal(err)
	}
	var a, o any
	if err := db.QueryRow(`SELECT at, old FROM t`).Scan(&a, &o); err != nil {
		t.Fatal(err)
	}
	if s, ok := a.(string); !ok || s != "2026-11-01 12:00:00.000000005+00:00" {
		t.Errorf("the driver hands over a datetime(9) as %T %v, want velox's text", a, a)
	}
	if v, ok := o.(time.Time); !ok || !v.Equal(at) {
		t.Errorf("the driver hands over a datetime as %T %v, want the time", o, o)
	}
}

type (
	// moment is a time GoType whose Scan, like kind.Moment's, takes only a
	// time.Time, as every driver hands a time column over.
	moment time.Time
	// boxed is a time GoType that does not convert to time.Time, whose Scan
	// takes only a time.Time.
	boxed struct{ T time.Time }
)

func (m *moment) Scan(src any) error {
	v, ok := src.(time.Time)
	if !ok {
		return fmt.Errorf("moment: cannot scan a %T", src)
	}
	*m = moment(v)
	return nil
}

func (b *boxed) Scan(src any) error {
	v, ok := src.(time.Time)
	if !ok {
		return fmt.Errorf("boxed: cannot scan a %T", src)
	}
	b.T = v
	return nil
}

// TestSQLiteRowsReadTimes: every destination of a time reads a datetime(9)
// column as the driver reads a datetime one, and the old datetime column
// reads as before.
func TestSQLiteRowsReadTimes(t *testing.T) {
	_, ex := timeDB(t)
	ctx := context.Background()
	at := time.Date(2026, 11, 1, 12, 0, 0, 5, time.FixedZone("HK", 8*3600))
	if _, err := ex.Exec(ctx, `INSERT INTO t (at, maybe, old, label) VALUES (?, NULL, ?, 'x'), (?, ?, ?, 'y')`, []any{at, at, at.Add(time.Hour), at, at}); err != nil {
		t.Fatal(err)
	}
	r, err := ex.Query(ctx, `SELECT at, maybe, old, at, at, maybe, at, old, at, label FROM t ORDER BY id`, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var (
		a, o   time.Time
		maybe  *time.Time
		m      moment
		b      boxed
		mm     *moment
		anyAt  any
		anyOld any
		text   string
		label  string
	)
	for i := 0; r.Next(); i++ {
		if err := r.Scan(&a, &maybe, &o, &m, &b, &mm, &anyAt, &anyOld, &text, &label); err != nil {
			t.Fatalf("row %d: %v", i, err)
		}
		want := at
		if i == 1 {
			want = at.Add(time.Hour)
		}
		if !a.Equal(want) || a.Location() != time.UTC {
			t.Errorf("row %d: *time.Time = %v, want %v in UTC", i, a, want)
		}
		if !o.Equal(at) {
			t.Errorf("row %d: the datetime column = %v, want %v", i, o, at)
		}
		if i == 0 && (maybe != nil || mm != nil) {
			t.Errorf("row 0: NULL into a pointer = %v, %v; want nil", maybe, mm)
		}
		if i == 1 && (maybe == nil || !maybe.Equal(at) || mm == nil || !time.Time(*mm).Equal(at)) {
			t.Errorf("row 1: the pointers = %v, %v; want %v", maybe, mm, at)
		}
		if !time.Time(m).Equal(want) || !b.T.Equal(want) {
			t.Errorf("row %d: the GoTypes = %v, %v; want %v", i, time.Time(m), b.T, want)
		}
		if v, ok := anyAt.(time.Time); !ok || !v.Equal(want) {
			t.Errorf("row %d: *any of datetime(9) = %T %v, want the time", i, anyAt, anyAt)
		}
		if v, ok := anyOld.(time.Time); !ok || !v.Equal(at) {
			t.Errorf("row %d: *any of datetime = %T %v, want the time", i, anyOld, anyOld)
		}
		if !strings.HasSuffix(text, "+00:00") {
			t.Errorf("row %d: *string = %q, want the column's text", i, text)
		}
	}
	if err := r.Err(); err != nil {
		t.Fatal(err)
	}
}

// TestSQLiteRowsReadComputedTimes: a column of no declared type, SQLite's
// own text and text in another zone read into a time; text that is no time
// fails as database/sql fails it.
func TestSQLiteRowsReadComputedTimes(t *testing.T) {
	_, ex := timeDB(t)
	ctx := context.Background()
	at := time.Date(2026, 11, 1, 12, 0, 0, 5, time.UTC)
	if _, err := ex.Exec(ctx, `INSERT INTO t (at, old, label) VALUES (?, ?, 'x')`, []any{at, at}); err != nil {
		t.Fatal(err)
	}
	read := func(query string) (time.Time, error) {
		r, err := ex.Query(ctx, query, nil)
		if err != nil {
			return time.Time{}, err
		}
		defer r.Close()
		var v time.Time
		if !r.Next() {
			return v, r.Err()
		}
		return v, r.Scan(&v)
	}
	for query, want := range map[string]time.Time{
		`SELECT MAX(at) FROM t`:                        at,
		`SELECT MIN(old) FROM t`:                       at,
		`SELECT '2026-11-01 20:00:00.000000005+08:00'`: at,
		`SELECT '2026-11-01 12:00:00'`:                 at.Truncate(time.Second),
	} {
		if got, err := read(query); err != nil || !got.Equal(want) {
			t.Errorf("%s = %v, %v; want %v", query, got, err, want)
		}
	}
	if got, err := read(`SELECT CURRENT_TIMESTAMP`); err != nil || time.Since(got) > time.Minute {
		t.Errorf("CURRENT_TIMESTAMP = %v, %v", got, err)
	}
	if _, err := read(`SELECT 'no time'`); err == nil || !strings.Contains(err.Error(), "string") {
		t.Errorf("text that is no time: %v", err)
	}
	if _, err := read(`SELECT NULL`); err == nil || !strings.Contains(err.Error(), "NULL") {
		t.Errorf("NULL into a time.Time: %v", err)
	}
}
