// This file holds the scan destination that keeps pgx's stdlib driver from
// inspecting a column's type on every row: colScanner, and scanWraps, the rule
// for the destinations it wraps.

package velox

import (
	"database/sql"
	"database/sql/driver"
	"reflect"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
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

// scanWrapsType reports whether a destination of type t is worth wrapping in a
// colScanner: whether pgx's stdlib driver would check its type on every row,
// and database/sql converts into it. t is a pointer to the field. It is true
// for a pointer to a bool, an integer, a float, a string, a byte slice or a
// struct that converts to time.Time, or to a pointer to one of them. It is
// false for a destination that is a sql.Scanner already, for one that pgx hands
// to its own scan plans (pgtype's ArraySetter and BytesScanner), for sql.RawBytes
// and for every other kind, whose handling stays as it is. A pointer type that
// has a name of its own is left alone, as pgx leaves it.
func scanWrapsType(t reflect.Type) bool {
	if t == nil || t.Kind() != reflect.Pointer || t.Name() != "" {
		return false
	}
	scanner := reflect.TypeFor[sql.Scanner]()
	arraySetter := reflect.TypeFor[pgtype.ArraySetter]()
	bytesScanner := reflect.TypeFor[pgtype.BytesScanner]()
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
