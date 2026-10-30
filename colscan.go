// This file holds the scan destination that makes pgx's stdlib driver pass its
// check of a column's type at the first step: colScanner, and scanWraps, the
// rule for the destinations it wraps.

package velox

import (
	"database/sql"
	"database/sql/driver"
	"reflect"
	"sync"
	"time"
)

// colScanner is a scan destination for a column whose field is dst, a pointer
// to it. It converts exactly as database/sql would for dst. It exists because
// pgx's stdlib driver checks the type of a destination on every column of every
// row, and a destination that is a sql.Scanner passes that check at once.
type colScanner struct {
	dst any // a pointer to the field
}

// Scan converts src into the field as database/sql does, with the same errors.
func (s *colScanner) Scan(src any) error {
	return sql.ConvertAssign(driver.ScanContext{}, s.dst, src)
}

// pgBytesScanner is pgtype.BytesScanner, and pgArraySetter the part of
// pgtype.ArraySetter that does not name a pgtype type; TestPgxTypesMirrored pins
// both. A type that has only this part is not wrapped, which leaves its
// handling as it was.
type (
	pgBytesScanner interface{ ScanBytes(v []byte) error }
	pgArraySetter  interface {
		ScanIndex(i int) any
		ScanIndexType() any
	}
)

// scanWrapsType reports whether a destination of type t is worth wrapping in a
// colScanner: whether pgx's stdlib driver would check its type on every row,
// and database/sql converts into it. t is a pointer to the field. It is true
// for a pointer to a bool, an integer, a float, a string, a byte slice or a
// struct that converts to time.Time, or to pointers to one of them, at any
// depth. It is false for a destination that is a sql.Scanner already, for one
// that pgx hands to its own scan plans (pgtype's ArraySetter and BytesScanner),
// for sql.RawBytes and for every other kind, whose handling stays as it is. A
// pointer type that has a name of its own is not wrapped: pgx handles it as
// before.
func scanWrapsType(t reflect.Type) bool {
	if t == nil || t.Kind() != reflect.Pointer || t.Name() != "" {
		return false
	}
	scanner := reflect.TypeFor[sql.Scanner]()
	arraySetter := reflect.TypeFor[pgArraySetter]()
	bytesScanner := reflect.TypeFor[pgBytesScanner]()
	e := t
	for {
		if e.Implements(scanner) || e.Implements(arraySetter) || e.Implements(bytesScanner) {
			return false
		}
		if e.Kind() != reflect.Pointer {
			break
		}
		if e != t && e.Name() != "" {
			return false
		}
		e = e.Elem()
	}
	if e == reflect.TypeFor[sql.RawBytes]() {
		return false
	}
	switch e.Kind() {
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64, reflect.String:
		return true
	case reflect.Slice:
		return e.Elem().Kind() == reflect.Uint8
	case reflect.Struct:
		return e.ConvertibleTo(reflect.TypeFor[time.Time]())
	default:
		return false
	}
}

// scanWrapCache holds scanWraps's answers: reflect.Type to bool.
var scanWrapCache sync.Map

// scanWraps is scanWrapsType, remembered by type. A nil t is false and is not
// remembered.
func scanWraps(t reflect.Type) bool {
	if t == nil {
		return false
	}
	if v, ok := scanWrapCache.Load(t); ok {
		return v.(bool)
	}
	w := scanWrapsType(t)
	scanWrapCache.Store(t, w)
	return w
}

// colSlot is one column of a colRows: the destination type its last scan
// was given, whether scanWraps wraps it, and the colScanner that does.
type colSlot struct {
	typ  reflect.Type
	wrap bool
	cs   colScanner
}

// colRows are rows read through pgx's stdlib driver. Scan hands the driver a
// colScanner, in place of each destination that scanWraps wraps, so the
// driver's type check of the destination passes at its first step, and the
// conversion is database/sql's. slots and args are reused from row to row.
type colRows struct {
	*sql.Rows
	slots []colSlot
	args  []any
}

func (r *colRows) Close() { _ = r.Rows.Close() }

// Scan scans the row into dst as sql.Rows.Scan does, through a colScanner
// for each wrapped destination. It never writes to dst. A nil pointer is
// passed to the driver as it is, so the driver rejects it with its own error.
func (r *colRows) Scan(dst ...any) error {
	if len(r.slots) != len(dst) {
		r.slots = make([]colSlot, len(dst))
		r.args = make([]any, len(dst))
	}
	for i, d := range dst {
		s := &r.slots[i]
		if t := reflect.TypeOf(d); t != s.typ {
			s.typ, s.wrap = t, scanWraps(t)
		}
		if s.wrap && !reflect.ValueOf(d).IsNil() {
			s.cs.dst = d
			r.args[i] = &s.cs
		} else {
			r.args[i] = d
		}
	}
	return r.Rows.Scan(r.args...)
}

// pgxStdlib reports whether db's driver is pgx's stdlib driver, however db
// was opened from it. A driver that wraps it is not detected. A nil db, which
// Open allows for a DB that only renders, is not.
func pgxStdlib(db *sql.DB) bool {
	if db == nil {
		return false
	}
	t := reflect.TypeOf(db.Driver())
	return t != nil && t.Kind() == reflect.Pointer &&
		t.Elem().Name() == "Driver" && t.Elem().PkgPath() == "github.com/jackc/pgx/v5/stdlib"
}
