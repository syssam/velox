package engine

import (
	"database/sql/driver"
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

// numeric stands for pgx's pgtype.Numeric: a Valuer of text with a
// NumericValue method.
type numeric struct{ s string }

func (n numeric) Value() (driver.Value, error) {
	if n.s == "" {
		return nil, nil
	}
	return n.s, nil
}
func (n numeric) NumericValue() (numeric, error) { return n, nil }

// TestOrderValueKinds: a value the driver read becomes the order value its
// column's database type says: MySQL's DECIMAL text a DecimalText, its
// DATETIME text without parseTime a TimeText, pgx's NUMERIC text a
// DecimalText, pgx's own pgtype.Numeric a DecimalText, other text a string,
// and anything typed as it is.
func TestOrderValueKinds(t *testing.T) {
	at := time.Date(2026, 3, 1, 10, 0, 0, 123456000, time.UTC)
	for _, c := range []struct {
		v    any
		typ  string
		want any
	}{
		{[]byte("1.50"), "DECIMAL", DecimalText("1.50")},
		{"1.50", "NUMERIC", DecimalText("1.50")},
		{[]byte("2026-03-01 10:00:00.123456"), "DATETIME", TimeText("2026-03-01 10:00:00.123456")},
		{[]byte("2026-03-01"), "DATE", TimeText("2026-03-01")},
		{[]byte("abc"), "VARCHAR", "abc"},
		{"abc", "", "abc"},
		{numeric{"12.345"}, "", DecimalText("12.345")},
		{numeric{}, "", nil},
		{int64(7), "BIGINT", int64(7)},
		{uint64(math.MaxUint64), "UNSIGNED BIGINT", uint64(math.MaxUint64)},
		{2.5, "DOUBLE", 2.5},
		{at, "DATETIME", at},
		{nil, "DECIMAL", nil},
	} {
		if got := orderValue(c.v, c.typ); !reflect.DeepEqual(got, c.want) {
			t.Errorf("orderValue(%#v, %q) = %#v, want %#v", c.v, c.typ, got, c.want)
		}
	}
}

// TestOrderValueRoundTrip: every kind of order value comes back from its
// JSON as the same Go value; an unsigned value past int64 is exact, and one
// within it is an int64.
func TestOrderValueRoundTrip(t *testing.T) {
	at := time.Date(2026, 3, 1, 10, 0, 0, 123456789, time.FixedZone("x", 8*3600))
	for _, c := range []struct{ v, want any }{
		{nil, nil}, {true, true}, {int64(-3), int64(-3)}, {int32(5), int64(5)},
		{uint64(math.MaxInt64), int64(math.MaxInt64)},
		{uint64(math.MaxUint64), uint64(math.MaxUint64)},
		{uint64(math.MaxInt64) + 1, uint64(math.MaxInt64) + 1},
		{3.0, 3.0}, {0.1, 0.1}, {1e300, 1e300}, {float32(0.5), 0.5},
		{math.Inf(1), math.Inf(1)}, {math.Inf(-1), math.Inf(-1)},
		{"", ""}, {"s:x", "s:x"}, {"12", "12"},
		{DecimalText("0.37500000000000000700"), DecimalText("0.37500000000000000700")},
		{DecimalText("-1E+40"), DecimalText("-1E+40")},
		{TimeText("2026-03-01 10:00:00.123456"), TimeText("2026-03-01 10:00:00.123456")},
		{at, at},
	} {
		enc, err := EncodeOrderValue(c.v)
		if err != nil {
			t.Fatalf("%#v: %v", c.v, err)
		}
		b, err := json.Marshal(enc)
		if err != nil {
			t.Fatalf("%#v: %v", c.v, err)
		}
		dec := json.NewDecoder(strings.NewReader(string(b)))
		dec.UseNumber()
		var back any
		if err := dec.Decode(&back); err != nil {
			t.Fatal(err)
		}
		got, err := DecodeOrderValue(back)
		if err != nil {
			t.Fatalf("%#v as %s: %v", c.v, b, err)
		}
		if tm, ok := c.want.(time.Time); ok {
			if g, ok := got.(time.Time); !ok || !g.Equal(tm) {
				t.Errorf("%#v as %s: %#v", c.v, b, got)
			}
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%#v as %s: %#v, want %#v", c.v, b, got, c.want)
		}
	}
	nan, err := EncodeOrderValue(math.NaN())
	if err != nil {
		t.Fatal(err)
	}
	if got, err := DecodeOrderValue(nan); err != nil || !math.IsNaN(got.(float64)) {
		t.Errorf("NaN: %v %v", got, err)
	}
	if _, err := EncodeOrderValue(struct{}{}); err == nil {
		t.Error("a struct: no error")
	}
	for _, bad := range []any{"x", "u:12", "u:x", "d:abc", "t:now", "f:1.5", json.Number("1x"), 1.5, []any{}} {
		if _, err := DecodeOrderValue(bad); err == nil {
			t.Errorf("%#v: no error", bad)
		}
	}
}

// TestBoundValue: a decimal read as text is bound cast to a decimal, and a
// time cast to DATETIME(6) on MySQL; anything else as it is.
func TestBoundValue(t *testing.T) {
	at := time.Unix(0, 0)
	for _, c := range []struct {
		d    Dialect
		v    any
		text string
		arg  any
	}{
		{MySQL, DecimalText("1.5"), "CAST(? AS DECIMAL(65,30))", "1.5"},
		{Postgres, DecimalText("1.5"), "CAST(? AS NUMERIC)", "1.5"},
		{SQLite, DecimalText("1.5"), "CAST(? AS NUMERIC)", "1.5"},
		{MySQL, TimeText("2026-03-01"), "CAST(? AS DATETIME(6))", "2026-03-01"},
		{Postgres, TimeText("2026-03-01"), "?", "2026-03-01"},
		{MySQL, at, "CAST(? AS DATETIME(6))", at},
		{Postgres, at, "?", at},
		{MySQL, uint64(math.MaxUint64), "?", uint64(math.MaxUint64)},
		{MySQL, "x", "?", "x"},
	} {
		text, arg := boundValue(c.d, c.v)
		if text != c.text || !reflect.DeepEqual(arg, c.arg) {
			t.Errorf("%v %#v: %s %#v, want %s %#v", c.d, c.v, text, arg, c.text, c.arg)
		}
	}
}
