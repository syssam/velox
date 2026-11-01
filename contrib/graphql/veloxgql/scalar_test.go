package veloxgql_test

import (
	"bytes"
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"

	"github.com/syssam/velox/contrib/graphql/veloxgql"
)

// TestJSONEmptyLiteral: a literal [] reaches UnmarshalGQL as gqlparser's nil
// slice, and stays [] wherever it is, as it does through a variable.
func TestJSONEmptyLiteral(t *testing.T) {
	for _, c := range []struct{ query, want string }{
		{`{ f(j: []) }`, `[]`},
		{`{ f(j: {}) }`, `{}`},
		{`{ f(j: [[]]) }`, `[[]]`},
		{`{ f(j: {a: [], b: [{c: []}]}) }`, `{"a":[],"b":[{"c":[]}]}`},
		{`{ f(j: null) }`, `null`},
		{`{ f(j: [1]) }`, `[1]`},
	} {
		doc, err := parser.ParseQuery(&ast.Source{Input: c.query})
		if err != nil {
			t.Fatal(err)
		}
		v, err := doc.Operations[0].SelectionSet[0].(*ast.Field).Arguments[0].Value.Value(nil)
		if err != nil {
			t.Fatal(err)
		}
		var j veloxgql.JSON
		if err := j.UnmarshalGQL(v); err != nil {
			t.Fatal(err)
		}
		if string(j) != c.want {
			t.Errorf("%s: %s, want %s", c.query, j, c.want)
		}
	}
}

// scalar is a pointer to a veloxgql scalar, as gqlgen holds one to unmarshal
// into.
type scalar = graphql.Unmarshaler

func marshal(m graphql.Marshaler) string {
	var buf bytes.Buffer
	m.MarshalGQL(&buf)
	return buf.String()
}

// scalarCase is one scalar: what values marshal to, what inputs unmarshal to,
// and inputs that are errors.
type scalarCase struct {
	name    string
	fresh   func() scalar                // a new zero value to unmarshal into
	value   func(scalar) any             // the unmarshalled value, to compare
	marshal map[string]graphql.Marshaler // wire form → value
	inputs  []struct{ in, want any }
	invalid []any
}

func TestScalars(t *testing.T) {
	hk := time.FixedZone("", 8*3600)
	when := time.Date(2026, 10, 20, 1, 2, 3, 123456789, hk)
	type io = struct{ in, want any }
	cases := []scalarCase{
		{
			name:  "Time",
			fresh: func() scalar { return new(veloxgql.Time) },
			value: func(s scalar) any { return time.Time(*s.(*veloxgql.Time)).Format(time.RFC3339Nano) },
			marshal: map[string]graphql.Marshaler{
				`"2026-10-20T01:02:03.123456789+08:00"`: veloxgql.Time(when),
				`"2026-10-19T17:02:03.123456789Z"`:      veloxgql.Time(when.UTC()),
				`"0001-01-01T00:00:00Z"`:                veloxgql.Time{},
			},
			inputs: []io{
				{"2026-10-20T01:02:03.123456789+08:00", "2026-10-20T01:02:03.123456789+08:00"},
				{"2026-10-20T01:02:03Z", "2026-10-20T01:02:03Z"},
				{"2026-10-20T01:02:03.5-05:00", "2026-10-20T01:02:03.5-05:00"},
			},
			invalid: []any{"", "yesterday", "2026-10-20", "2026-10-20 01:02:03", int64(5), nil, true},
		},
		{
			name:  "UUID",
			fresh: func() scalar { return new(veloxgql.UUID) },
			value: func(s scalar) any { return s.(*veloxgql.UUID).String() },
			marshal: map[string]graphql.Marshaler{
				`"0193a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"`: veloxgql.UUID{0x01, 0x93, 0xa1, 0xb2, 0xc3, 0xd4, 0x7e, 0x5f, 0x8a, 0x9b, 0x0c, 0x1d, 0x2e, 0x3f, 0x4a, 0x5b},
				`"00000000-0000-0000-0000-000000000000"`: veloxgql.UUID{},
			},
			inputs: []io{
				{"0193a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b", "0193a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"},
				{"0193A1B2-C3D4-7E5F-8A9B-0C1D2E3F4A5B", "0193a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"},
			},
			invalid: []any{"", "xyz", "0193a1b2c3d47e5f8a9b0c1d2e3f4a5b", "0193a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5g",
				"0193a1b2-c3d4-7e5f-8a9b_0c1d2e3f4a5b", int64(1), nil},
		},
		{
			name:  "Bytes",
			fresh: func() scalar { return new(veloxgql.Bytes) },
			value: func(s scalar) any { return string(*s.(*veloxgql.Bytes)) },
			marshal: map[string]graphql.Marshaler{
				`"AAH/"`: veloxgql.Bytes{0, 1, 0xff},
				`""`:     veloxgql.Bytes(nil),
			},
			inputs:  []io{{"AAH/", "\x00\x01\xff"}, {"", ""}, {"aGk=", "hi"}},
			invalid: []any{"!!!", "aGk", "AAH_", int64(5), nil},
		},
		{
			name:  "JSON",
			fresh: func() scalar { return new(veloxgql.JSON) },
			value: func(s scalar) any { return string(*s.(*veloxgql.JSON)) },
			marshal: map[string]graphql.Marshaler{
				`{"a":[1,2]}`: veloxgql.JSON(`{"a":[1,2]}`),
				`"x"`:         veloxgql.JSON(`"x"`),
				`null`:        veloxgql.JSON(nil),
			},
			inputs: []io{
				{map[string]any{"b": []any{json.Number("1"), "x"}, "a": true}, `{"a":true,"b":[1,"x"]}`},
				{int64(5), `5`},
				{"s", `"s"`},
				{nil, `null`},
			},
			invalid: []any{make(chan int), math.NaN(), []any{func() {}}},
		},
		{
			name:  "Uint64",
			fresh: func() scalar { return new(veloxgql.Uint64) },
			value: func(s scalar) any { return uint64(*s.(*veloxgql.Uint64)) },
			marshal: map[string]graphql.Marshaler{
				`"18446744073709551615"`: veloxgql.Uint64(math.MaxUint64),
				`"0"`:                    veloxgql.Uint64(0),
			},
			inputs: []io{{"18446744073709551615", uint64(math.MaxUint64)}, {"0", uint64(0)}, {"42", uint64(42)}},
			invalid: []any{"", "-1", "1.5", "+1", "18446744073709551616", " 1", "0x10",
				int64(5), json.Number("5"), float64(5), nil},
		},
		{
			name:  "OrderDirection",
			fresh: func() scalar { return new(veloxgql.OrderDirection) },
			value: func(s scalar) any { return *s.(*veloxgql.OrderDirection) },
			marshal: map[string]graphql.Marshaler{
				`"ASC"`:  veloxgql.OrderDirectionAsc,
				`"DESC"`: veloxgql.OrderDirectionDesc,
			},
			inputs:  []io{{"ASC", veloxgql.OrderDirectionAsc}, {"DESC", veloxgql.OrderDirectionDesc}},
			invalid: []any{"", "asc", "UP", int64(1), nil},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for want, m := range c.marshal {
				if got := marshal(m); got != want {
					t.Errorf("MarshalGQL(%#v) = %s, want %s", m, got, want)
				}
			}
			for _, in := range c.inputs {
				s := c.fresh()
				if err := s.UnmarshalGQL(in.in); err != nil {
					t.Errorf("UnmarshalGQL(%#v): %v", in.in, err)
					continue
				}
				if got := c.value(s); got != in.want {
					t.Errorf("UnmarshalGQL(%#v) = %#v, want %#v", in.in, got, in.want)
				}
				// What unmarshals marshals back to the same wire form.
				if m, ok := any(s).(graphql.Marshaler); ok && c.name != "JSON" {
					back := c.fresh()
					var wire any
					if err := json.Unmarshal([]byte(marshal(m)), &wire); err != nil {
						t.Fatal(err)
					}
					if err := back.UnmarshalGQL(wire); err != nil || c.value(back) != in.want {
						t.Errorf("%#v did not round-trip: %#v, %v", in.in, c.value(back), err)
					}
				}
			}
			for _, in := range c.invalid {
				if err := c.fresh().UnmarshalGQL(in); err == nil {
					t.Errorf("UnmarshalGQL(%#v) succeeded, want an error", in)
				}
			}
		})
	}
}

// An invalid JSON value marshals as null, so the response stays valid JSON.
func TestJSONInvalidMarshalsNull(t *testing.T) {
	if got := marshal(veloxgql.JSON(`{"a":`)); got != "null" {
		t.Errorf("MarshalGQL of invalid JSON = %s, want null", got)
	}
}

// PageInfo has the fields gqlgen binds the PageInfo type's to.
var _ = veloxgql.PageInfo{HasNextPage: true, HasPreviousPage: false, StartCursor: &veloxgql.Cursor{}, EndCursor: nil}
