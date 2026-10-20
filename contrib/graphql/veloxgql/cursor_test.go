package veloxgql_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/google/uuid"

	"github.com/syssam/velox2/contrib/graphql/veloxgql"
)

var (
	_ graphql.Marshaler   = veloxgql.Cursor{}
	_ graphql.Unmarshaler = (*veloxgql.Cursor)(nil)
)

func TestCursorRoundTrip(t *testing.T) {
	hk := time.FixedZone("", 8*3600)
	when := time.Date(2026, 10, 20, 1, 2, 3, 123456789, hk)
	big := int64(1<<53 + 1) // a float64 would round it to 1<<53
	u := uuid.MustParse("0193a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b")
	c := veloxgql.Cursor{
		Values: []any{"ann", big, when, true, 1.5, u, uint64(math.MaxUint64), veloxgql.Time(when.UTC())},
		Key:    int64(1<<62 + 3),
	}
	s := veloxgql.EncodeCursor(c)
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		t.Fatalf("EncodeCursor = %q, not base64url without padding: %v", s, err)
	}
	const want = `{"v":1,"o":["ann",9007199254740993,"2026-10-20T01:02:03.123456789+08:00",true,1.5,` +
		`"0193a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b",18446744073709551615,"2026-10-19T17:02:03.123456789Z"],"k":4611686018427387907}`
	if string(b) != want {
		t.Fatalf("cursor JSON\n got %s\nwant %s", b, want)
	}

	d, err := veloxgql.DecodeCursor(s)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Values) != len(c.Values) {
		t.Fatalf("decoded %d values, want %d", len(d.Values), len(c.Values))
	}
	expect(t, d.Values[0], "ann")
	expect(t, d.Values[1], big)
	gotTime, err := veloxgql.CursorValue[time.Time](d.Values[2])
	if err != nil || !gotTime.Equal(when) || gotTime.Nanosecond() != 123456789 {
		t.Errorf("time = %v, %v, want %v", gotTime, err, when)
	}
	if _, off := gotTime.Zone(); off != 8*3600 {
		t.Errorf("time zone offset = %d, want %d", off, 8*3600)
	}
	expect(t, d.Values[3], true)
	expect(t, d.Values[4], 1.5)
	expect(t, d.Values[5], u)
	expect(t, d.Values[5], [16]byte(u))
	expect(t, d.Values[5], veloxgql.UUID(u))
	expect(t, d.Values[6], uint64(math.MaxUint64))
	expect(t, d.Values[6], veloxgql.Uint64(math.MaxUint64))
	expect(t, d.Values[7], veloxgql.Time(when.UTC()))
	expect(t, d.Key, int64(1<<62+3))

	// A key keeps its JSON type: a number for an integer, a string otherwise.
	for _, k := range []struct {
		key  any
		json string
	}{
		{int64(-7), `-7`},
		{uint64(math.MaxUint64), `18446744073709551615`},
		{"k:1", `"k:1"`},
		{u, `"0193a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"`},
		{[16]byte(u), `"0193a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"`},
	} {
		s := veloxgql.EncodeCursor(veloxgql.Cursor{Key: k.key})
		b, _ := base64.RawURLEncoding.DecodeString(s)
		if want := `{"v":1,"o":[],"k":` + k.json + `}`; string(b) != want {
			t.Errorf("key %v: %s, want %s", k.key, b, want)
		}
	}
	d, err = veloxgql.DecodeCursor(veloxgql.EncodeCursor(veloxgql.Cursor{Key: u}))
	if err != nil {
		t.Fatal(err)
	}
	expect(t, d.Key, u)

	// Floats that JSON has no number for are strings.
	for _, f := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		d, err := veloxgql.DecodeCursor(veloxgql.EncodeCursor(veloxgql.Cursor{Values: []any{f}, Key: 1}))
		if err != nil {
			t.Fatal(err)
		}
		got, err := veloxgql.CursorValue[float64](d.Values[0])
		if err != nil || !(got == f || math.IsNaN(got) && math.IsNaN(f)) {
			t.Errorf("float %v round-tripped to %v, %v", f, got, err)
		}
	}

	// The scalar: a JSON string on the wire, both ways.
	var buf bytes.Buffer
	c.MarshalGQL(&buf)
	if want := `"` + s + `"`; buf.String() != want {
		t.Errorf("MarshalGQL = %s, want %s", &buf, want)
	}
	var back veloxgql.Cursor
	if err := back.UnmarshalGQL(s); err != nil {
		t.Fatal(err)
	}
	if veloxgql.EncodeCursor(back) != s {
		t.Errorf("UnmarshalGQL then EncodeCursor = %q, want %q", veloxgql.EncodeCursor(back), s)
	}
}

// expect converts the decoded cursor value v to the type of want.
func expect[T comparable](t *testing.T, v any, want T) {
	t.Helper()
	got, err := veloxgql.CursorValue[T](v)
	if err != nil || got != want {
		t.Errorf("CursorValue[%T](%#v) = %v, %v, want %v", want, v, got, err, want)
	}
}

func TestCursorMalformed(t *testing.T) {
	doc := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	for name, s := range map[string]string{
		"empty":           "",
		"bad base64":      "!!",
		"padded":          base64.URLEncoding.EncodeToString([]byte(`{"v":1,"o":[],"k":1}`)),
		"not JSON":        doc("not json"),
		"not an object":   doc(`[1]`),
		"version 2":       doc(`{"v":2,"o":[],"k":1}`),
		"no version":      doc(`{"o":[],"k":1}`),
		"version string":  doc(`{"v":"1","o":[],"k":1}`),
		"no order":        doc(`{"v":1,"k":1}`),
		"null order":      doc(`{"v":1,"o":null,"k":1}`),
		"order an object": doc(`{"v":1,"o":{},"k":1}`),
		"no key":          doc(`{"v":1,"o":[]}`),
		"null key":        doc(`{"v":1,"o":[],"k":null}`),
		"key an object":   doc(`{"v":1,"o":[],"k":{}}`),
		"key a bool":      doc(`{"v":1,"o":[],"k":true}`),
		"nested value":    doc(`{"v":1,"o":[[1]],"k":1}`),
		"object value":    doc(`{"v":1,"o":[{"a":1}],"k":1}`),
		"unknown field":   doc(`{"v":1,"o":[],"k":1,"x":0}`),
		"trailing data":   doc(`{"v":1,"o":[],"k":1} 1`),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := veloxgql.DecodeCursor(s); !errors.Is(err, veloxgql.ErrBadCursor) {
				t.Errorf("DecodeCursor = %v, want ErrBadCursor", err)
			}
			var c veloxgql.Cursor
			if err := c.UnmarshalGQL(s); !errors.Is(err, veloxgql.ErrBadCursor) {
				t.Errorf("UnmarshalGQL = %v, want ErrBadCursor", err)
			}
		})
	}
	var c veloxgql.Cursor
	for _, v := range []any{int64(5), nil, json.Number("1")} {
		if err := c.UnmarshalGQL(v); !errors.Is(err, veloxgql.ErrBadCursor) {
			t.Errorf("UnmarshalGQL(%#v) = %v, want ErrBadCursor", v, err)
		}
	}

	// A value that is not of the order term's type: the cursor was taken
	// under another order.
	for name, f := range map[string]func() error{
		"string as int64":   func() error { _, err := veloxgql.CursorValue[int64]("ann"); return err },
		"number as string":  func() error { _, err := veloxgql.CursorValue[string](json.Number("1")); return err },
		"fraction as int64": func() error { _, err := veloxgql.CursorValue[int64](json.Number("1.5")); return err },
		"int8 overflow":     func() error { _, err := veloxgql.CursorValue[int8](json.Number("300")); return err },
		"negative uint":     func() error { _, err := veloxgql.CursorValue[uint64](json.Number("-1")); return err },
		"string as time":    func() error { _, err := veloxgql.CursorValue[time.Time]("yesterday"); return err },
		"number as time":    func() error { _, err := veloxgql.CursorValue[time.Time](json.Number("1")); return err },
		"string as bool":    func() error { _, err := veloxgql.CursorValue[bool]("true"); return err },
		"bad UUID":          func() error { _, err := veloxgql.CursorValue[uuid.UUID]("x"); return err },
		"bad bare UUID":     func() error { _, err := veloxgql.CursorValue[[16]byte]("x"); return err },
		"null as int64":     func() error { _, err := veloxgql.CursorValue[int64](nil); return err },
		"string as float":   func() error { _, err := veloxgql.CursorValue[float64]("1.5"); return err },
	} {
		if err := f(); !errors.Is(err, veloxgql.ErrBadCursor) {
			t.Errorf("%s: %v, want ErrBadCursor", name, err)
		}
	}
	if _, err := veloxgql.CursorValue[struct{}]("x"); err == nil || errors.Is(err, veloxgql.ErrBadCursor) {
		t.Errorf("CursorValue[struct{}] = %v, want an unsupported-type error", err)
	}
}

func TestCursorEncodeKeyless(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("EncodeCursor of a cursor with no key did not panic")
		}
	}()
	veloxgql.EncodeCursor(veloxgql.Cursor{Values: []any{1}})
}
