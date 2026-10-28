package gqlimpl_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/syssam/velox2/contrib/graphql/veloxgql"
	"github.com/syssam/velox2/contrib/graphql/veloxgql/gqlimpl"
)

// expect converts the decoded cursor value v to the type of want.
func expect[T comparable](t *testing.T, v any, want T) {
	t.Helper()
	got, err := gqlimpl.CursorValue[T](v)
	if err != nil || got != want {
		t.Errorf("CursorValue[%T](%#v) = %v, %v, want %v", want, v, got, err, want)
	}
}

// TestCursorValues: a decoded cursor's values convert back to their column's
// Go types, to the full digit and the nanosecond.
func TestCursorValues(t *testing.T) {
	hk := time.FixedZone("", 8*3600)
	when := time.Date(2026, 10, 20, 1, 2, 3, 123456789, hk)
	big := int64(1<<53 + 1) // a float64 would round it to 1<<53
	u := uuid.MustParse("0193a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b")
	terms := []veloxgql.CursorTerm{}
	for i, v := range []any{"ann", big, when, true, 1.5, u, uint64(math.MaxUint64), veloxgql.Time(when.UTC()), []byte{0, 0xff}} {
		terms = append(terms, veloxgql.CursorTerm{Field: "F" + strconv.Itoa(i), Direction: veloxgql.OrderDirectionAsc, Value: v})
	}
	d, err := veloxgql.DecodeCursor(veloxgql.EncodeCursor(veloxgql.Cursor{Type: "User", Terms: terms, Key: u}))
	if err != nil {
		t.Fatal(err)
	}
	v := make([]any, len(d.Terms))
	for i, term := range d.Terms {
		v[i] = term.Value
	}
	expect(t, v[0], "ann")
	expect(t, v[1], big)
	gotTime, err := gqlimpl.CursorValue[time.Time](v[2])
	if err != nil || !gotTime.Equal(when) || gotTime.Nanosecond() != 123456789 {
		t.Errorf("time = %v, %v, want %v", gotTime, err, when)
	}
	if _, off := gotTime.Zone(); off != 8*3600 {
		t.Errorf("time zone offset = %d, want %d", off, 8*3600)
	}
	expect(t, v[3], true)
	expect(t, v[4], 1.5)
	expect(t, v[5], u)
	expect(t, v[5], [16]byte(u))
	expect(t, v[5], veloxgql.UUID(u))
	expect(t, v[6], uint64(math.MaxUint64))
	expect(t, v[6], veloxgql.Uint64(math.MaxUint64))
	expect(t, v[7], veloxgql.Time(when.UTC()))
	if got, err := gqlimpl.CursorValue[[]byte](v[8]); err != nil || !bytes.Equal(got, []byte{0, 0xff}) {
		t.Errorf("bytes = %v, %v", got, err)
	}
	if got, err := gqlimpl.CursorValue[veloxgql.Bytes](v[8]); err != nil || !bytes.Equal(got, []byte{0, 0xff}) {
		t.Errorf("Bytes = %v, %v", got, err)
	}
	expect(t, d.Key, u)

	bigKey, err := veloxgql.DecodeCursor(veloxgql.EncodeCursor(veloxgql.Cursor{Type: "User", Key: int64(1<<62 + 3)}))
	if err != nil {
		t.Fatal(err)
	}
	expect(t, bigKey.Key, int64(1<<62+3))

	// Floats that JSON has no number for are strings.
	for _, f := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		c := veloxgql.Cursor{Type: "User", Terms: []veloxgql.CursorTerm{{Field: "F", Direction: veloxgql.OrderDirectionAsc, Value: f}}, Key: 1}
		d, err := veloxgql.DecodeCursor(veloxgql.EncodeCursor(c))
		if err != nil {
			t.Fatal(err)
		}
		got, err := gqlimpl.CursorValue[float64](d.Terms[0].Value)
		if err != nil || !(got == f || math.IsNaN(got) && math.IsNaN(f)) {
			t.Errorf("float %v round-tripped to %v, %v", f, got, err)
		}
	}
}

// TestCursorValueMismatch: a value that is not of the order term's type is
// ErrBadCursor, and a type that is not supported is another error.
func TestCursorValueMismatch(t *testing.T) {
	for name, f := range map[string]func() error{
		"string as int64":   func() error { _, err := gqlimpl.CursorValue[int64]("ann"); return err },
		"number as string":  func() error { _, err := gqlimpl.CursorValue[string](json.Number("1")); return err },
		"fraction as int64": func() error { _, err := gqlimpl.CursorValue[int64](json.Number("1.5")); return err },
		"int8 overflow":     func() error { _, err := gqlimpl.CursorValue[int8](json.Number("300")); return err },
		"negative uint":     func() error { _, err := gqlimpl.CursorValue[uint64](json.Number("-1")); return err },
		"string as time":    func() error { _, err := gqlimpl.CursorValue[time.Time]("yesterday"); return err },
		"number as time":    func() error { _, err := gqlimpl.CursorValue[time.Time](json.Number("1")); return err },
		"string as bool":    func() error { _, err := gqlimpl.CursorValue[bool]("true"); return err },
		"bad UUID":          func() error { _, err := gqlimpl.CursorValue[uuid.UUID]("x"); return err },
		"bad bare UUID":     func() error { _, err := gqlimpl.CursorValue[[16]byte]("x"); return err },
		"null as int64":     func() error { _, err := gqlimpl.CursorValue[int64](nil); return err },
		"string as float":   func() error { _, err := gqlimpl.CursorValue[float64]("1.5"); return err },
	} {
		if err := f(); !errors.Is(err, veloxgql.ErrBadCursor) {
			t.Errorf("%s: %v, want ErrBadCursor", name, err)
		}
	}
	// The message takes the type name's article (english.Article).
	for _, c := range []struct {
		err  error
		want string
	}{
		{func() error { _, err := gqlimpl.CursorValue[int64]("ann"); return err }(), "a value is not an int64"},
		{func() error { _, err := gqlimpl.CursorValue[uint64](json.Number("-1")); return err }(), "a value is not a uint64"},
		{func() error { _, err := gqlimpl.CursorValue[string](json.Number("1")); return err }(), "a value is not a string"},
	} {
		if c.err == nil || !strings.HasSuffix(c.err.Error(), ": "+c.want) {
			t.Errorf("%v, want it to end %q", c.err, c.want)
		}
	}
	_, err := gqlimpl.CursorValue[struct{}]("x")
	if err == nil || errors.Is(err, veloxgql.ErrBadCursor) {
		t.Errorf("CursorValue[struct{}] = %v, want an unsupported-type error", err)
	}
	// An interface type is named, not printed as <nil>.
	_, err = gqlimpl.CursorValue[io.Reader]("x")
	if err == nil || !strings.Contains(err.Error(), "io.Reader") {
		t.Errorf("CursorValue[io.Reader] = %v, want an error naming io.Reader", err)
	}
}

// Review Focus 1 and 2: a cursor is refused on another type and under
// another order, before any query runs.
func TestCursorCheck(t *testing.T) {
	name := gqlimpl.CursorOrder{Field: "NAME", Direction: veloxgql.OrderDirectionAsc}
	c := veloxgql.Cursor{
		Type:  "User",
		Terms: []veloxgql.CursorTerm{{Field: "NAME", Direction: veloxgql.OrderDirectionAsc, Value: "ann"}},
		Key:   int64(1),
	}
	d, err := veloxgql.DecodeCursor(veloxgql.EncodeCursor(c))
	if err != nil {
		t.Fatal(err)
	}
	if err := gqlimpl.CheckCursor(d, "User", []gqlimpl.CursorOrder{name}); err != nil {
		t.Errorf("the cursor's own type and order: %v", err)
	}
	key := veloxgql.Cursor{Type: "User", Key: int64(1)}
	if err := gqlimpl.CheckCursor(key, "User", nil); err != nil {
		t.Errorf("a key-only cursor under the key order: %v", err)
	}

	// Another type's cursor, with the same key type and order.
	post := c
	post.Type = "Post"
	d, err = veloxgql.DecodeCursor(veloxgql.EncodeCursor(post))
	if err != nil {
		t.Fatal(err)
	}
	err = gqlimpl.CheckCursor(d, "User", []gqlimpl.CursorOrder{name})
	if !errors.Is(err, veloxgql.ErrBadCursor) || !errors.Is(err, veloxgql.ErrWrongIDType) {
		t.Errorf("a Post cursor on User: %v, want ErrBadCursor and ErrWrongIDType", err)
	}
	if err != nil && (!strings.Contains(err.Error(), "User") || strings.Contains(err.Error(), "Post")) {
		t.Errorf("%q does not name only the expected type", err)
	}

	for label, order := range map[string][]gqlimpl.CursorOrder{
		"NAME vs EMAIL":  {{Field: "EMAIL", Direction: veloxgql.OrderDirectionAsc}},
		"ASC vs DESC":    {{Field: "NAME", Direction: veloxgql.OrderDirectionDesc}},
		"fewer terms":    nil,
		"more terms":     {name, {Field: "AGE", Direction: veloxgql.OrderDirectionAsc}},
		"terms reversed": {{Field: "AGE", Direction: veloxgql.OrderDirectionAsc}, name},
	} {
		cur := c
		if label == "terms reversed" {
			cur.Terms = []veloxgql.CursorTerm{c.Terms[0], {Field: "AGE", Direction: veloxgql.OrderDirectionAsc, Value: 1}}
		}
		err := gqlimpl.CheckCursor(cur, "User", order)
		if !errors.Is(err, veloxgql.ErrBadCursor) || errors.Is(err, veloxgql.ErrWrongIDType) {
			t.Errorf("%s: %v, want ErrBadCursor alone", label, err)
		}
		if err != nil && (strings.Contains(err.Error(), "NAME") || strings.Contains(err.Error(), "EMAIL")) {
			t.Errorf("%s: %q echoes the fields", label, err)
		}
	}
}
