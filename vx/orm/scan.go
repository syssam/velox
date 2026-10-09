package orm

import (
	stdsql "database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/google/uuid"

	"github.com/syssam/velox/schema/field"
)

// convertAssign stores v in dest when v's type converts to the destination's.
// It is the fallback for id types the fast paths of assignScanned do not know.
func convertAssign(dest, v any) error {
	dv := reflect.ValueOf(dest)
	if dv.Kind() != reflect.Pointer || dv.IsNil() {
		return fmt.Errorf("orm: destination %T is not a pointer", dest)
	}
	vv := reflect.ValueOf(v)
	// Go converts an integer to a string as a code point; that is never meant here.
	if !vv.IsValid() || !vv.Type().ConvertibleTo(dv.Elem().Type()) ||
		(dv.Elem().Kind() == reflect.String && vv.Kind() != reflect.String && vv.Kind() != reflect.Slice) {
		return fmt.Errorf("orm: cannot store %T in %T", v, dest)
	}
	dv.Elem().Set(vv.Convert(dv.Elem().Type()))
	return nil
}

// The types below are the scan destinations of a column that may be NULL
// while its Go field is not a pointer (an Optional field that is not
// Nillable): NULL leaves the field at its zero value. Each is one pointer
// wide, so handing it to rows.Scan as an interface does not allocate.

// nullString scans into a string field.
type nullString struct{ P *string }

// Scan implements sql.Scanner.
func (d nullString) Scan(src any) error {
	// The driver's own types are stored directly; database/sql's Null type,
	// which costs an allocation per call, handles the rest.
	switch v := src.(type) {
	case nil:
		*d.P = ""
		return nil
	case string:
		*d.P = v
		return nil
	case []byte:
		*d.P = string(v)
		return nil
	}
	var n stdsql.NullString
	if err := n.Scan(src); err != nil {
		return err
	}
	*d.P = n.String
	return nil
}

// nullInt scans into an int field.
type nullInt struct{ P *int }

// Scan implements sql.Scanner.
func (d nullInt) Scan(src any) error {
	// The driver's own types are stored directly; database/sql's Null type,
	// which costs an allocation per call, handles the rest.
	switch v := src.(type) {
	case nil:
		*d.P = 0
		return nil
	case int64:
		*d.P = int(v)
		return nil
	}
	var n stdsql.NullInt64
	if err := n.Scan(src); err != nil {
		return err
	}
	*d.P = int(n.Int64)
	return nil
}

// nullInt64 scans into an int64 field.
type nullInt64 struct{ P *int64 }

// Scan implements sql.Scanner.
func (d nullInt64) Scan(src any) error {
	// The driver's own types are stored directly; database/sql's Null type,
	// which costs an allocation per call, handles the rest.
	switch v := src.(type) {
	case nil:
		*d.P = 0
		return nil
	case int64:
		*d.P = v
		return nil
	}
	var n stdsql.NullInt64
	if err := n.Scan(src); err != nil {
		return err
	}
	*d.P = n.Int64
	return nil
}

// nullFloat64 scans into a float64 field.
type nullFloat64 struct{ P *float64 }

// Scan implements sql.Scanner.
func (d nullFloat64) Scan(src any) error {
	// The driver's own types are stored directly; database/sql's Null type,
	// which costs an allocation per call, handles the rest.
	switch v := src.(type) {
	case nil:
		*d.P = 0
		return nil
	case float64:
		*d.P = v
		return nil
	}
	var n stdsql.NullFloat64
	if err := n.Scan(src); err != nil {
		return err
	}
	*d.P = n.Float64
	return nil
}

// nullBool scans into a bool field.
type nullBool struct{ P *bool }

// Scan implements sql.Scanner.
func (d nullBool) Scan(src any) error {
	// The driver's own types are stored directly; database/sql's Null type,
	// which costs an allocation per call, handles the rest.
	switch v := src.(type) {
	case nil:
		*d.P = false
		return nil
	case bool:
		*d.P = v
		return nil
	}
	var n stdsql.NullBool
	if err := n.Scan(src); err != nil {
		return err
	}
	*d.P = n.Bool
	return nil
}

// nullTime scans into a time.Time field.
type nullTime struct{ P *time.Time }

// Scan implements sql.Scanner.
func (d nullTime) Scan(src any) error {
	// The driver's own types are stored directly; database/sql's Null type,
	// which costs an allocation per call, handles the rest.
	switch v := src.(type) {
	case nil:
		*d.P = time.Time{}
		return nil
	case time.Time:
		*d.P = v
		return nil
	}
	var n stdsql.NullTime
	if err := n.Scan(src); err != nil {
		return err
	}
	*d.P = n.Time
	return nil
}

// nullable scans into a field of any other type, through database/sql's own
// conversion; NULL leaves the field untouched. P is a pointer to the field.
type nullable struct{ P any }

// Scan implements sql.Scanner.
func (d nullable) Scan(src any) error {
	if src == nil {
		return nil
	}
	if s, ok := d.P.(stdsql.Scanner); ok {
		return s.Scan(src)
	}
	switch v := src.(type) {
	case []byte:
		// The driver may reuse the buffer after Scan returns.
		if p, ok := d.P.(*[]byte); ok {
			*p = append([]byte(nil), v...)
			return nil
		}
		return convertAssign(d.P, string(v))
	default:
		return convertAssign(d.P, src)
	}
}

// jsonDest scans a jsonDest column into the field P points to; NULL and an empty
// value leave it untouched.
type jsonDest struct{ P any }

// Scan implements sql.Scanner.
func (d jsonDest) Scan(src any) error {
	var b []byte
	switch v := src.(type) {
	case nil:
		return nil
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return fmt.Errorf("orm: cannot scan %T into a jsonDest field", src)
	}
	if len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, d.P)
}

// destKind says how a column is handed to rows.Scan.
type destKind uint8

const (
	destRaw  destKind = iota // the field's own address
	destJSON                 // through encoding/json
	destNull                 // NULL leaves the field at its zero value
	destBool                 // a named bool type
)

// destKindOf picks the scan destination of a column from its schema field
// (nil for a foreign key without one) and the address of its Go field.
func destKindOf(f *FieldMeta, isID, ptr bool, p any) destKind {
	switch {
	case f == nil:
		return destRaw
	case f.Type == field.TypeJSON:
		return destJSON
	}
	// database/sql stores a driver value in a named string or number through
	// reflection, but a bool only in an exact *bool.
	if _, isScanner := p.(stdsql.Scanner); !isScanner && f.Type == field.TypeBool {
		t := reflect.TypeOf(p).Elem()
		if ptr {
			t = t.Elem()
		}
		if t != reflect.TypeFor[bool]() {
			return destBool
		}
	}
	if isID || f.Nillable || !f.Optional {
		return destRaw
	}
	// The column may be NULL but the Go field is not a pointer.
	return destNull
}

// namedBool scans into a field whose type is a named bool, or a pointer to one.
type namedBool struct{ P any }

// Scan implements sql.Scanner.
func (d *namedBool) Scan(src any) error {
	v := reflect.ValueOf(d.P).Elem()
	if src == nil {
		v.SetZero()
		return nil
	}
	b, err := driver.Bool.ConvertValue(src)
	if err != nil {
		return err
	}
	if v.Kind() == reflect.Pointer {
		v.Set(reflect.New(v.Type().Elem()))
		v = v.Elem()
	}
	v.SetBool(b.(bool)) //nolint:errcheck // driver.Bool returns a bool or an error
	return nil
}

// slot is the per-column scratch of one result set. The scan destinations
// that carry an interface are two words wide and would be allocated for every
// row if handed to rows.Scan by value; a pointer into a slot is not.
type slot struct {
	n nullable
	j jsonDest
	b namedBool
}

// dest returns the scan destination of Columns[i] in e. s is that column's
// slot of the result set being read.
func (m *Meta) dest(e any, i int, s *slot) any {
	p := m.Ptr(e, i)
	switch m.cols[i].dest {
	case destJSON:
		s.j.P = p
		return &s.j
	case destBool:
		s.b.P = p
		return &s.b
	case destNull:
		switch p := p.(type) {
		case *string:
			return nullString{P: p}
		case *int:
			return nullInt{P: p}
		case *int64:
			return nullInt64{P: p}
		case *float64:
			return nullFloat64{P: p}
		case *bool:
			return nullBool{P: p}
		case *time.Time:
			return nullTime{P: p}
		}
		s.n.P = p
		return &s.n
	}
	// database/sql stores an integer into a *int by formatting and parsing
	// it, which allocates for every value of three digits or more.
	if ip, ok := p.(*int); ok {
		return intDest{P: ip}
	}
	return p
}

// intDest scans a column that is never NULL into an int field.
type intDest struct{ P *int }

// Scan implements sql.Scanner.
func (d intDest) Scan(src any) error {
	if v, ok := src.(int64); ok {
		*d.P = int(v)
		return nil
	}
	var n stdsql.NullInt64
	if err := n.Scan(src); err != nil {
		return err
	}
	if !n.Valid {
		return errors.New("orm: NULL in a column that is not optional")
	}
	*d.P = int(n.Int64)
	return nil
}

// get returns the value of column in e; a nil pointer field yields nil.
func (m *Meta) get(e any, column string) any {
	i := m.col(column)
	if i < 0 {
		return nil
	}
	return m.read(e, i)
}

// read returns the value of Columns[i] in e. The common key types are read
// directly, any other through reflection.
func (m *Meta) read(e any, i int) any {
	switch p := m.Ptr(e, i).(type) {
	case *int:
		return *p
	case *int64:
		return *p
	case *string:
		return *p
	case *uuid.UUID:
		return *p
	case **int:
		if *p == nil {
			return nil
		}
		return **p
	case **int64:
		if *p == nil {
			return nil
		}
		return **p
	case **string:
		if *p == nil {
			return nil
		}
		return **p
	case **uuid.UUID:
		if *p == nil {
			return nil
		}
		return **p
	default:
		v := reflect.ValueOf(p).Elem()
		if m.cols[i].ptr {
			if v.IsNil() {
				return nil
			}
			v = v.Elem()
		}
		return v.Interface()
	}
}

// assign stores v, a value as passed to a setter or produced by a default,
// into column of e. A value of another type is converted when Go allows it.
func (m *Meta) assign(e any, column string, v any) error {
	i := m.col(column)
	if i < 0 || v == nil {
		return nil
	}
	switch p := m.Ptr(e, i).(type) {
	case *string:
		if x, ok := v.(string); ok {
			*p = x
			return nil
		}
	case *int:
		if x, ok := v.(int); ok {
			*p = x
			return nil
		}
	case *int64:
		if x, ok := v.(int64); ok {
			*p = x
			return nil
		}
	case *float64:
		if x, ok := v.(float64); ok {
			*p = x
			return nil
		}
	case *bool:
		if x, ok := v.(bool); ok {
			*p = x
			return nil
		}
	case *time.Time:
		if x, ok := v.(time.Time); ok {
			*p = x
			return nil
		}
	case **string:
		if x, ok := v.(string); ok {
			*p = &x
			return nil
		}
	case **int:
		if x, ok := v.(int); ok {
			*p = &x
			return nil
		}
	case **time.Time:
		if x, ok := v.(time.Time); ok {
			*p = &x
			return nil
		}
	}
	dst := reflect.ValueOf(m.Ptr(e, i)).Elem()
	if m.cols[i].ptr {
		dst.Set(reflect.New(dst.Type().Elem()))
		dst = dst.Elem()
	}
	src := reflect.ValueOf(v)
	switch {
	case src.Type().AssignableTo(dst.Type()):
		dst.Set(src)
	case src.Type().ConvertibleTo(dst.Type()) && (dst.Kind() != reflect.String || src.Kind() == reflect.String):
		dst.Set(src.Convert(dst.Type()))
	default:
		return fmt.Errorf("orm: %s.%s: cannot store a %T in a %s", m.Name, column, v, dst.Type())
	}
	return nil
}
