package conn

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestSQLiteTimeRefusesOtherTypes: a value that is no time is an error naming it.
func TestSQLiteTimeRefusesOtherTypes(t *testing.T) {
	var dst time.Time
	err := (&sqliteTime{dst: &dst}).Scan(42)
	if err == nil || !strings.Contains(err.Error(), "int") {
		t.Errorf("Scan(42) = %v", err)
	}
}

// TestSQLiteRowsWithoutColumns: rows that cannot name their columns say so.
func TestSQLiteRowsWithoutColumns(t *testing.T) {
	type bare struct{ Rows }
	_, err := (&sqliteRows{Rows: bare{}}).Columns()
	if err == nil || !strings.Contains(err.Error(), "do not name their columns") {
		t.Errorf("Columns() = %v", err)
	}
}

// TestSQLiteTimeTakesATime: a driver that parsed the column itself gives a time.
func TestSQLiteTimeTakesATime(t *testing.T) {
	want := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	var dst time.Time
	if err := (&sqliteTime{dst: &dst}).Scan(want); err != nil || !dst.Equal(want) {
		t.Errorf("Scan = %v, %v; want %v", dst, err, want)
	}
}

// TestParseVeloxTime: velox's own form reads into UTC; any other text, an
// impossible date in velox's form included, is left to parseSQLiteTime.
func TestParseVeloxTime(t *testing.T) {
	for _, c := range []struct {
		in   string
		want time.Time
		ok   bool
	}{
		{"2026-11-01 12:34:56.000000007+00:00", time.Date(2026, 11, 1, 12, 34, 56, 7, time.UTC), true},
		{"0001-01-01 00:00:00.000000000+00:00", time.Time{}, true},
		{"2024-02-29 23:59:59.999999999+00:00", time.Date(2024, 2, 29, 23, 59, 59, 999999999, time.UTC), true},
		{"2026-02-29 00:00:00.000000000+00:00", time.Time{}, false},
		{"2026-13-01 00:00:00.000000000+00:00", time.Time{}, false},
		{"2026-11-00 00:00:00.000000000+00:00", time.Time{}, false},
		{"2026-11-01 24:00:00.000000000+00:00", time.Time{}, false},
		{"2026-11-01 12:60:00.000000000+00:00", time.Time{}, false},
		{"2026-11-01 12:00:60.000000000+00:00", time.Time{}, false},
		{"2026-11-01 12:00:00.00000000x+00:00", time.Time{}, false},
		{"2026-11-01T12:00:00.000000000+00:00", time.Time{}, false},
		{"2026-11-01 12:00:00.000000000+08:00", time.Time{}, false},
		{"2026-11-01 12:00:00.000000000Z00:00", time.Time{}, false},
		{"2026-11-01 12:00:00", time.Time{}, false},
		{"", time.Time{}, false},
	} {
		got, ok := parseVeloxTime(c.in)
		if ok != c.ok || !got.Equal(c.want) || ok && got.Location() != time.UTC {
			t.Errorf("parseVeloxTime(%q) = %v, %v; want %v, %v", c.in, got, ok, c.want, c.ok)
		}
		if gotB, okB := parseVeloxTime([]byte(c.in)); okB != ok || !gotB.Equal(got) {
			t.Errorf("parseVeloxTime([]byte(%q)) = %v, %v; the string gives %v, %v", c.in, gotB, okB, got, ok)
		}
		if !ok {
			continue
		}
		if want, err := time.Parse(sqliteTimeLayout, c.in); err != nil || !want.Equal(got) {
			t.Errorf("time.Parse(%q) = %v, %v; parseVeloxTime gives %v", c.in, want, err, got)
		}
	}
}

// TestSQLiteTextTimeFallsBack: text in another of SQLite's forms reads through
// parseSQLiteTime, and text that is no time does not read.
func TestSQLiteTextTimeFallsBack(t *testing.T) {
	want := time.Date(2026, 11, 1, 4, 0, 0, 0, time.UTC)
	for _, in := range []string{"2026-11-01 12:00:00.000000000+08:00", "2026-11-01 04:00:00", "2026-11-01T04:00Z"} {
		if got, ok := sqliteTextTime(in); !ok || !got.Equal(want) {
			t.Errorf("sqliteTextTime(%q) = %v, %v; want %v", in, got, ok, want)
		}
	}
	if got, ok := sqliteTextTime([]byte("no time")); ok {
		t.Errorf("sqliteTextTime(no time) = %v, true", got)
	}
}

// TestSQLiteTextTimeType: a time with a precision is the driver's text; a
// bare DATETIME the driver parses itself, and other types are no time.
func TestSQLiteTextTimeType(t *testing.T) {
	for typ, want := range map[string]bool{
		"DATETIME(9)": true, "datetime(9)": true, "TIMESTAMP(6)": true, "DATE (3)": true,
		"DATETIME": false, "TIMESTAMP": false, "TEXT": false, "": false, "TIME(9)": false,
		"DATETIME(9": false, "VARCHAR(9)": false,
	} {
		if got := sqliteTextTimeType(typ); got != want {
			t.Errorf("sqliteTextTimeType(%q) = %v, want %v", typ, got, want)
		}
	}
}

type (
	convTime    time.Time
	wrappedTime struct{ time.Time }
	textScanner struct{ s string }
)

func (w *wrappedTime) Scan(src any) error { w.Time, _ = src.(time.Time); return nil }
func (s *textScanner) Scan(src any) error { s.s, _ = src.(string); return nil }

// TestSQLiteScanHow: a destination that holds a time is read as one; one that
// may take a time is read by its column; the rest as it is.
func TestSQLiteScanHow(t *testing.T) {
	for _, c := range []struct {
		dst  any
		want scanHow
	}{
		{new(time.Time), scanTime},
		{new(*time.Time), scanTime},
		{new(convTime), scanTime},
		{new(*convTime), scanTime},
		{new(wrappedTime), scanByDecl},
		{new(*wrappedTime), scanByDecl},
		{new(textScanner), scanByDecl},
		{new(any), scanByDecl},
		{new(string), scanAsIs},
		{new(*int64), scanAsIs},
		{new([]byte), scanAsIs},
		{time.Time{}, scanAsIs},
		{nil, scanAsIs},
	} {
		if got := sqliteScanHow(reflect.TypeOf(c.dst)); got != c.want {
			t.Errorf("sqliteScanHow(%T) = %d, want %d", c.dst, got, c.want)
		}
	}
}

// textRows hands each destination, a sql.Scanner, the column's value, as
// database/sql does with what a driver hands over.
type textRows struct {
	Rows
	vals []any
}

func (r textRows) Scan(dst ...any) error {
	for i, d := range dst {
		if s, ok := d.(sql.Scanner); ok {
			if err := s.Scan(r.vals[i]); err != nil {
				return err
			}
		}
	}
	return nil
}

// TestSQLiteTimeRowAllocatesNothing: past the first row, a row of times in
// velox's form, and of NULL into a **time.Time, allocates nothing.
func TestSQLiteTimeRowAllocatesNothing(t *testing.T) {
	r := &sqliteRows{Rows: textRows{vals: []any{"2026-11-01 12:34:56.000000007+00:00", "2026-11-02 00:00:00.000000000+00:00", nil}}}
	var (
		a, b time.Time
		c    = new(time.Time)
	)
	dst := []any{&a, &b, &c} // made once, as the engine makes a row's
	if err := r.Scan(dst...); err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 11, 1, 12, 34, 56, 7, time.UTC); a != want || c != nil {
		t.Fatalf("scanned %v, %v; want %v, nil", a, c, want)
	}
	if n := testing.AllocsPerRun(100, func() { _ = r.Scan(dst...) }); n != 0 {
		t.Errorf("a row allocates %v times, want 0", n)
	}
	s := sqliteTime{dst: &a}
	for _, src := range []any{"2026-11-01 12:34:56.000000007+00:00", []byte("2026-11-01 12:34:56.000000007+00:00")} {
		if n := testing.AllocsPerRun(100, func() { _ = s.Scan(src) }); n != 0 {
			t.Errorf("Scan(%T) allocates %v times, want 0", src, n)
		}
	}
}

// BenchmarkSQLiteTimeScan scans one time in velox's form per op.
func BenchmarkSQLiteTimeScan(b *testing.B) {
	var dst time.Time
	s := sqliteTime{dst: &dst}
	var src any = "2026-11-01 12:34:56.000000007+00:00"
	b.ReportAllocs()
	for b.Loop() {
		if err := s.Scan(src); err != nil {
			b.Fatal(err)
		}
	}
}

// oneRows is an Executor whose every query returns rows.
type oneRows struct {
	Executor
	rows Rows
}

func (e oneRows) Query(context.Context, string, []any) (Rows, error) { return e.rows, nil }

type closingRows struct {
	textRows
	closed int
}

func (r *closingRows) Close() { r.closed++ }

// TestSQLiteQueryReusesItsSlots: a query of times, past the first, allocates
// only its sqliteRows: Close hands its slots to the next query. A second
// Close changes nothing.
func TestSQLiteQueryReusesItsSlots(t *testing.T) {
	rows := &closingRows{}
	rows.vals = []any{"2026-11-01 12:34:56.000000007+00:00", nil}
	ex := SQLiteTimes(oneRows{rows: rows})
	var (
		a time.Time
		b = new(time.Time)
	)
	dst := []any{&a, &b}
	query := func() {
		r, err := ex.Query(context.Background(), "", nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.Scan(dst...); err != nil {
			t.Fatal(err)
		}
		r.Close()
	}
	query()
	if n := testing.AllocsPerRun(100, query); n > 1 {
		t.Errorf("a query allocates %v times, want 1", n)
	}
	r, _ := ex.Query(context.Background(), "", nil)
	_ = r.Scan(dst...)
	before := rows.closed
	r.Close()
	r.Close()
	if rows.closed != before+2 {
		t.Errorf("Close reached the driver's rows %d times, want 2", rows.closed-before)
	}
	if want := time.Date(2026, 11, 1, 12, 34, 56, 7, time.UTC); a != want || b != nil {
		t.Errorf("scanned %v, %v; want %v, nil", a, b, want)
	}
}
