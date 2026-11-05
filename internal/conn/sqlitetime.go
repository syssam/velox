package conn

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"
)

// sqliteTimeLayout writes every time as the same width of UTC. SQLite has no time
// type and compares the text it holds, so that text sorts by the instant only when
// it has one zone and one precision. SQLite's drivers read this form back.
const sqliteTimeLayout = "2006-01-02 15:04:05.000000000-07:00"

// SQLiteTimes returns ex with each time argument written in sqliteTimeLayout, so
// that a statement stores and compares times by the instant whatever their zone.
func SQLiteTimes(ex Executor) Executor { return sqliteTimes{ex} }

type sqliteTimes struct{ Executor }

// Query runs a statement that returns rows.
func (e sqliteTimes) Query(ctx context.Context, s string, args []any) (Rows, error) {
	r, err := e.Executor.Query(ctx, s, sqliteTimeArgs(args))
	if err != nil {
		return nil, err
	}
	return &sqliteRows{Rows: r}, nil
}

// sqliteRows read times from SQLite's text. velox declares a time column
// datetime(9), which the driver hands over as the text the column holds, and
// SQLite gives a computed column, such as MAX(created_at), no declared type at
// all. Scan parses that text for each destination that holds a time. Its slots
// and args are made at the first row that needs them and reused after, so a
// row allocates nothing of its own.
type sqliteRows struct {
	Rows
	slots []sqliteSlot
	args  []any
	// declTimes says, by column, whether its declared type is a time that the
	// driver hands over as text. It is read once, when a slot first needs it.
	declTimes []bool
	declRead  bool
}

// sqliteSlot is one column of a sqliteRows: the destination type its last
// scan was given, how Scan reads into it, and the scanner it hands the driver.
type sqliteSlot struct {
	typ reflect.Type
	how scanHow
	ts  sqliteTime
}

// scanHow is how sqliteRows reads a column into a destination.
type scanHow uint8

const (
	// scanAsIs hands the destination to the driver.
	scanAsIs scanHow = iota
	// scanTime reads through sqliteTime, whatever the column: the
	// destination holds a time.
	scanTime
	// scanByDecl reads through sqliteTime when the column's declared type is
	// a time, and as it is otherwise: the destination may take a time, as a
	// sql.Scanner or an *any does, but its type does not say.
	scanByDecl
)

// Columns returns the names of the columns, as Raw reads them.
func (r *sqliteRows) Columns() ([]string, error) {
	c, ok := r.Rows.(interface{ Columns() ([]string, error) })
	if !ok {
		return nil, fmt.Errorf("the driver's rows (%T) do not name their columns", r.Rows)
	}
	return c.Columns()
}

// Scan scans the row into dst as the driver does, but for a destination that
// takes a time, whose column's text it parses. A row of no such destination
// goes to the driver as it is. It never writes to dst itself: a nil pointer
// goes to the driver, which rejects it with its own error.
func (r *sqliteRows) Scan(dst ...any) error {
	if r.slots == nil && !takesTime(dst) {
		return r.Rows.Scan(dst...)
	}
	if len(r.slots) != len(dst) {
		r.slots = make([]sqliteSlot, len(dst))
		r.args = make([]any, len(dst))
	}
	for i, d := range dst {
		s := &r.slots[i]
		if t := reflect.TypeOf(d); t != s.typ {
			s.typ, s.how = t, sqliteScanHow(t)
		}
		how := s.how
		if how == scanByDecl && !r.declTime(i) {
			how = scanAsIs
		}
		if how == scanAsIs || reflect.ValueOf(d).IsNil() {
			r.args[i] = d
			continue
		}
		s.ts.dst = d
		r.args[i] = &s.ts
	}
	return r.Rows.Scan(r.args...)
}

// takesTime reports whether a destination of dst may take a time.
func takesTime(dst []any) bool {
	for _, d := range dst {
		switch d.(type) {
		case *time.Time, **time.Time:
			return true
		case *int64, *int, *string, *bool, *float64, *[]byte, **int64, **string:
		default:
			if sqliteScanHow(reflect.TypeOf(d)) != scanAsIs {
				return true
			}
		}
	}
	return false
}

// declTime reports whether column i's declared type is a time that the driver
// hands over as text. Rows that cannot give their column types have none.
func (r *sqliteRows) declTime(i int) bool {
	if !r.declRead {
		r.declRead = true
		if c, ok := r.Rows.(interface {
			ColumnTypes() ([]*sql.ColumnType, error)
		}); ok {
			if cts, err := c.ColumnTypes(); err == nil {
				r.declTimes = make([]bool, len(cts))
				for j, ct := range cts {
					r.declTimes[j] = sqliteTextTimeType(ct.DatabaseTypeName())
				}
			}
		}
	}
	return i < len(r.declTimes) && r.declTimes[i]
}

// sqliteTextTimeType reports whether the declared type typ is a time that
// the driver hands over as text: DATE, DATETIME or TIMESTAMP with a
// precision, such as velox's datetime(9). The driver parses those words
// alone into a time.Time itself, and only them.
func sqliteTextTimeType(typ string) bool {
	word, rest, ok := strings.Cut(typ, "(")
	if !ok || !strings.HasSuffix(rest, ")") {
		return false
	}
	switch strings.ToUpper(strings.TrimSpace(word)) {
	case "DATE", "DATETIME", "TIMESTAMP":
		return true
	}
	return false
}

// scanHowCache holds sqliteScanHow's answers: reflect.Type to scanHow.
var scanHowCache sync.Map

// sqliteScanHow is how sqliteRows reads into a destination of type t,
// remembered by type. A pointer, at any depth, to time.Time or to a struct
// that converts to it is scanTime. Another pointer to a sql.Scanner, to an
// interface or to a struct is scanByDecl. The rest is scanAsIs.
func sqliteScanHow(t reflect.Type) scanHow {
	if t == nil || t.Kind() != reflect.Pointer {
		return scanAsIs
	}
	if v, ok := scanHowCache.Load(t); ok {
		return v.(scanHow)
	}
	how := scanAsIs
	scanner := reflect.TypeFor[sql.Scanner]()
	e := t
	for e.Kind() == reflect.Pointer {
		if e.Implements(scanner) {
			how = scanByDecl
		}
		e = e.Elem()
	}
	switch {
	case e.Kind() == reflect.Struct && e.ConvertibleTo(reflect.TypeFor[time.Time]()):
		how = scanTime
	case e.Kind() == reflect.Struct || e.Kind() == reflect.Interface:
		how = scanByDecl
	}
	scanHowCache.Store(t, how)
	return how
}

// sqliteTime scans into dst, a pointer that takes a time, the time SQLite's
// text holds, as well as what the driver hands over for a time column. What
// it does not read as a time, and NULL but into a **time.Time, goes to
// database/sql's conversion, which converts or fails as it would have.
type sqliteTime struct{ dst any }

// Scan implements sql.Scanner.
func (s *sqliteTime) Scan(src any) error {
	var (
		t  time.Time
		ok bool
	)
	switch v := src.(type) {
	case time.Time:
		t, ok = v, true
	case string:
		t, ok = sqliteTextTime(v)
	case []byte:
		t, ok = sqliteTextTime(v)
	case nil:
		if p, isPtr := s.dst.(**time.Time); isPtr {
			*p = nil
			return nil
		}
	}
	if !ok {
		return sql.ConvertAssign(driver.ScanContext{}, s.dst, src)
	}
	switch p := s.dst.(type) {
	case *time.Time:
		*p = t
		return nil
	case **time.Time:
		*p = new(t) // not &t, which would move t to the heap on every call
		return nil
	}
	return sql.ConvertAssign(driver.ScanContext{}, s.dst, t)
}

// sqliteTextTime reads s, in velox's form or another of sqliteTextTimes.
func sqliteTextTime[S string | []byte](s S) (time.Time, bool) {
	if t, ok := parseVeloxTime(s); ok {
		return t, true
	}
	t, err := parseSQLiteTime(string(s))
	return t, err == nil
}

// parseVeloxTime reads s in sqliteTimeLayout with the offset +00:00, the form
// velox writes, into a time in UTC. It allocates nothing. It does not read
// other text, an impossible date included: parseSQLiteTime reads that or
// refuses it.
func parseVeloxTime[S string | []byte](s S) (time.Time, bool) {
	if len(s) != len(sqliteTimeLayout) || s[4] != '-' || s[7] != '-' || s[10] != ' ' || s[13] != ':' ||
		s[16] != ':' || s[19] != '.' || string(s[29:]) != "+00:00" {
		return time.Time{}, false
	}
	num := func(from, to int) int {
		n := 0
		for i := from; i < to; i++ {
			c := s[i]
			if c < '0' || c > '9' {
				return -1
			}
			n = n*10 + int(c-'0')
		}
		return n
	}
	year, month, day := num(0, 4), num(5, 7), num(8, 10)
	hour, minute, sec, nsec := num(11, 13), num(14, 16), num(17, 19), num(20, 29)
	if year < 0 || month < 1 || month > 12 || day < 1 || hour < 0 || hour > 23 ||
		minute < 0 || minute > 59 || sec < 0 || sec > 59 || nsec < 0 {
		return time.Time{}, false
	}
	t := time.Date(year, time.Month(month), day, hour, minute, sec, nsec, time.UTC)
	if t.Day() != day { // past the month's last day
		return time.Time{}, false
	}
	return t, true
}

// sqliteTextTimes are the forms of a time that SQLite's date functions read and
// write. Parse takes a fraction of a second after the seconds, in any of them.
var sqliteTextTimes = []string{
	"2006-01-02 15:04:05Z07:00",
	"2006-01-02T15:04:05Z07:00",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05",
	"2006-01-02 15:04Z07:00",
	"2006-01-02T15:04Z07:00",
	"2006-01-02 15:04",
	"2006-01-02T15:04",
	"2006-01-02",
}

// parseSQLiteTime reads s in the first of sqliteTextTimes it fits. Without a
// zone it is UTC, as SQLite takes it.
func parseSQLiteTime(s string) (time.Time, error) {
	for _, layout := range sqliteTextTimes {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("%q is not a time", s)
}

// Exec runs a statement that returns no rows.
func (e sqliteTimes) Exec(ctx context.Context, s string, args []any) (Result, error) {
	return e.Executor.Exec(ctx, s, sqliteTimeArgs(args))
}

// Begin starts a transaction, whose statements the returned Executor runs.
//
// Drivers ignore a transaction's options on SQLite, so velox keeps them. Every
// SQLite transaction is serializable, so that level is the default and another
// is refused. A read-only one runs under PRAGMA query_only, a setting of the
// connection, and not of the transaction: it is as it was, which a DSN may have
// turned on, before the connection serves another statement.
func (e sqliteTimes) Begin(ctx context.Context, o sql.TxOptions) (Executor, Tx, error) {
	if o.Isolation != sql.LevelDefault && o.Isolation != sql.LevelSerializable {
		return nil, nil, fmt.Errorf("SQLite's transactions are serializable; it has no isolation level %s", o.Isolation)
	}
	o.Isolation = sql.LevelDefault
	var (
		ex  Executor
		tx  Tx
		err error
	)
	if s, ok := e.Executor.(SQL); ok && o.ReadOnly {
		ex, tx, err = s.BeginOnConn(ctx, sql.TxOptions{}, Setting{"PRAGMA query_only", "PRAGMA query_only = ON", "PRAGMA query_only = OFF"})
	} else {
		ex, tx, err = e.Executor.Begin(ctx, o)
	}
	if err != nil {
		return nil, nil, err
	}
	return sqliteTimes{ex}, tx, nil
}

// sqliteTimeArgs is args with its times as text. It copies args only when one is a
// time, as the caller's slice is a statement's, which hooks read.
func sqliteTimeArgs(args []any) []any {
	copied := false
	for i, a := range args {
		t, ok := timeOf(a)
		if !ok {
			continue
		}
		if !copied {
			args, copied = slices.Clone(args), true
		}
		args[i] = t.UTC().Format(sqliteTimeLayout)
	}
	return args
}

// timeOf is the time a is, points to or, as a driver.Valuer, gives. A nil pointer
// and a Valuer that fails are not times: database/sql handles them.
func timeOf(a any) (time.Time, bool) {
	switch x := a.(type) {
	case time.Time:
		return x, true
	case *time.Time:
		if x != nil {
			return *x, true
		}
	case driver.Valuer:
		if v := reflect.ValueOf(a); v.Kind() == reflect.Pointer && v.IsNil() {
			return time.Time{}, false
		}
		if v, err := x.Value(); err == nil {
			t, ok := v.(time.Time)
			return t, ok
		}
	}
	return time.Time{}, false
}
