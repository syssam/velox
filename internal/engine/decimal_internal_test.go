package engine

import (
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"
)

// tdec is a decimal held as text, with a Valuer, as kind.Dec is.
type tdec string

func (d tdec) Value() (driver.Value, error) { return string(d), nil }

// plainDec is a decimal held as text with no Valuer.
type plainDec string

// numDec is a Valuer that hands the driver a number.
type numDec float64

func (d numDec) Value() (driver.Value, error) { return float64(d), nil }

// bytesDec is a Valuer that hands the driver bytes.
type bytesDec string

func (d bytesDec) Value() (driver.Value, error) { return []byte(d), nil }

// badDec is a Valuer that fails.
type badDec struct{}

func (badDec) Value() (driver.Value, error) { return nil, errors.New("bad") }

// TestDecimalColumnType: a value is a decimal column's when its type, or the
// type it points to, is that column's Go type; nil, a nil pointer, another
// type, and a column without Decimal are not.
func TestDecimalColumnType(t *testing.T) {
	in := &EntityInfo{Columns: []ColumnInfo{
		{Name: "plain", val: reflect.TypeFor[plainDec]()},
		{Name: "amount", decimal: [2]uint8{19, 4}, val: reflect.TypeFor[tdec]()},
	}}
	d := tdec("1.5")
	var nilDec *tdec
	for _, c := range []struct {
		v    any
		want bool
	}{
		{d, true}, {&d, true}, {nil, false}, {nilDec, false}, {"1.5", false}, {plainDec("1"), false}, {new(int), false},
	} {
		if v, ok := decimalColumnType(in, c.v); ok != c.want || ok && v != any(d) {
			t.Errorf("%#v: %v %v", c.v, v, ok)
		}
	}
}

// TestSQLiteNumberKinds: sqliteNumber reads a Valuer's number, bytes and
// text, and a string kind with no Valuer; a failing Valuer and any other
// value are left as they are.
func TestSQLiteNumberKinds(t *testing.T) {
	for _, c := range []struct {
		in, want any
	}{
		{numDec(2.5), 2.5},
		{bytesDec("3"), int64(3)},
		{plainDec("-1.25"), -1.25},
		{plainDec("1e+2"), 100.0},
		{plainDec("1e-"), plainDec("1e-")},
		{plainDec("1e2x"), plainDec("1e2x")},
		{plainDec("+.5"), 0.5},
		{plainDec("."), plainDec(".")},
		{plainDec("1.2.3"), plainDec("1.2.3")},
		{badDec{}, badDec{}},
		{struct{ X int }{1}, struct{ X int }{1}},
	} {
		if got := sqliteNumber(c.in); got != c.want {
			t.Errorf("%#v: %#v, want %#v", c.in, got, c.want)
		}
	}
}
