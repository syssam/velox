package veloxgql_test

import (
	"encoding/base64"
	"encoding/json"
	"math"
	"math/rand/v2"
	"testing"

	"github.com/syssam/velox/contrib/graphql/veloxgql"
)

// referenceCursor is EncodeCursor's text as json.Marshal writes the document,
// the one every cursor was written with before EncodeCursor had a fast path.
func referenceCursor(t *testing.T, c veloxgql.Cursor) string {
	t.Helper()
	type term struct {
		F *string                  `json:"f"`
		D *veloxgql.OrderDirection `json:"d"`
		V any                      `json:"v"`
	}
	type doc struct {
		V *int    `json:"v"`
		T *string `json:"t"`
		O *[]term `json:"o"`
		K any     `json:"k"`
	}
	v := 1
	o := make([]term, len(c.Terms))
	for i, x := range c.Terms {
		o[i] = term{F: &x.Field, D: &x.Direction, V: x.Value}
	}
	b, err := json.Marshal(doc{V: &v, T: &c.Type, O: &o, K: c.Key})
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// TestEncodeCursorPlain: the cursors EncodeCursor writes without reflection
// (plain text and integers, strings, booleans and nil) are byte for byte the
// json.Marshal text, and so are the neighbours it leaves to json.Marshal.
func TestEncodeCursorPlain(t *testing.T) {
	asc, desc := veloxgql.OrderDirectionAsc, veloxgql.OrderDirectionDesc
	values := []any{
		nil, true, false, 0, -7, int8(-128), int16(300), int32(-1 << 31), int64(math.MaxInt64), int64(math.MinInt64),
		uint(5), uint8(255), uint16(65535), uint32(1 << 31), uint64(math.MaxUint64), veloxgql.Uint64(1 << 63),
		"", "a", "name with spaces", "x/y:z-1_2.3", "quote\"in", "back\\slash", "<tag>", "a&b", "tab\t", "é", "日本", "\u2028", "\x7f",
		1.5, float32(2.5), math.NaN(), math.Inf(1),
	}
	keys := []any{int64(1), 42, uint64(1 << 63), "k", "uu-id-1234", "a<b", 3.5}
	n := 0
	check := func(c veloxgql.Cursor) {
		t.Helper()
		got, err := veloxgql.EncodeCursor(c)
		if err != nil {
			t.Fatalf("%#v: %v", c, err)
		}
		if want := referenceCursor(t, c); got != want {
			t.Errorf("%#v:\ngot  %s\nwant %s", c, got, want)
		}
		n++
	}
	for _, typ := range []string{"User", "Account_2", "A B", "Wé", "<T>"} {
		for _, k := range keys {
			check(veloxgql.Cursor{Type: typ, Key: k})
			for _, v := range values {
				if f, ok := v.(float64); ok && (math.IsNaN(f) || math.IsInf(f, 0)) {
					continue // json.Marshal refuses them; EncodeCursor writes strings, tested elsewhere
				}
				check(veloxgql.Cursor{Type: typ, Key: k, Terms: []veloxgql.CursorTerm{{Field: "NAME", Direction: asc, Value: v}}})
			}
			check(veloxgql.Cursor{Type: typ, Key: k, Terms: []veloxgql.CursorTerm{
				{Field: "NAME", Direction: desc, Value: "a"}, {Field: "CREATED_AT", Direction: asc, Value: int64(3)}, {Field: "X&Y", Direction: asc, Value: nil},
			}})
		}
	}
	rng := rand.New(rand.NewPCG(1, 2))
	for range 2000 {
		var terms []veloxgql.CursorTerm
		for range rng.IntN(4) {
			d := asc
			if rng.IntN(2) == 0 {
				d = desc
			}
			terms = append(terms, veloxgql.CursorTerm{Field: "F" + string(rune('A'+rng.IntN(26))), Direction: d, Value: values[rng.IntN(len(values))]})
		}
		v := values[rng.IntN(len(values))]
		if v == nil {
			v = "k"
		}
		if f, ok := v.(float64); ok && (math.IsNaN(f) || math.IsInf(f, 0)) {
			continue
		}
		for i := range terms {
			if f, ok := terms[i].Value.(float64); ok && (math.IsNaN(f) || math.IsInf(f, 0)) {
				terms[i].Value = nil
			}
		}
		check(veloxgql.Cursor{Type: "T", Key: v, Terms: terms})
	}
	if n < 1000 {
		t.Fatalf("only %d cursors checked", n)
	}
}

func BenchmarkEncodeCursor(b *testing.B) {
	c := veloxgql.Cursor{Type: "Post", Key: int64(12345), Terms: []veloxgql.CursorTerm{{Field: "TITLE", Direction: veloxgql.OrderDirectionAsc, Value: "hello world"}}}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := veloxgql.EncodeCursor(c); err != nil {
			b.Fatal(err)
		}
	}
}
