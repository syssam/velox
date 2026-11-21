package veloxgql_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
)

// terms are order terms F0, F1, … ascending with the values vs.
func terms(vs ...any) []veloxgql.CursorTerm {
	out := make([]veloxgql.CursorTerm, len(vs))
	for i, v := range vs {
		out[i] = veloxgql.CursorTerm{Field: "F" + strconv.Itoa(i), Direction: veloxgql.OrderDirectionAsc, Value: v}
	}
	return out
}

// values are the Values of c's terms.
func values(c veloxgql.Cursor) []any {
	out := make([]any, len(c.Terms))
	for i, t := range c.Terms {
		out[i] = t.Value
	}
	return out
}

// cursorJSON is the JSON document of the cursor s.
func cursorJSON(t *testing.T, s string) string {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		t.Fatalf("%q is not base64url without padding: %v", s, err)
	}
	return string(b)
}

func TestCursorRoundTrip(t *testing.T) {
	hk := time.FixedZone("", 8*3600)
	when := time.Date(2026, 10, 20, 1, 2, 3, 123456789, hk)
	big := int64(1<<53 + 1) // a float64 would round it to 1<<53
	u := uuid.MustParse("0193a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b")
	c := veloxgql.Cursor{
		Type: "User",
		Terms: []veloxgql.CursorTerm{
			{Field: "NAME", Direction: veloxgql.OrderDirectionAsc, Value: "ann"},
			{Field: "AGE", Direction: veloxgql.OrderDirectionDesc, Value: big},
		},
		Key: int64(1<<62 + 3),
	}
	s := encode(t, c)
	const want = `{"v":1,"t":"User","o":[{"f":"NAME","d":"ASC","v":"ann"},{"f":"AGE","d":"DESC","v":9007199254740993}],"k":4611686018427387907}`
	if got := cursorJSON(t, s); got != want {
		t.Fatalf("cursor JSON\n got %s\nwant %s", got, want)
	}
	d, err := veloxgql.DecodeCursor(s)
	if err != nil {
		t.Fatal(err)
	}
	if d.Type != "User" || len(d.Terms) != 2 || d.Terms[0].Field != "NAME" || d.Terms[0].Direction != veloxgql.OrderDirectionAsc ||
		d.Terms[1].Field != "AGE" || d.Terms[1].Direction != veloxgql.OrderDirectionDesc {
		t.Fatalf("decoded %+v", d)
	}
	if encode(t, d) != s {
		t.Errorf("a decoded cursor encodes to %q, want %q", encode(t, d), s)
	}

	// Every kind of order value.
	c = veloxgql.Cursor{
		Type:  "User",
		Terms: terms("ann", big, when, true, 1.5, u, uint64(math.MaxUint64), veloxgql.Time(when.UTC()), []byte{0, 0xff}),
		Key:   int64(1),
	}
	d, err = veloxgql.DecodeCursor(encode(t, c))
	if err != nil {
		t.Fatal(err)
	}
	const wantValues = `["ann",9007199254740993,"2026-10-20T01:02:03.123456789+08:00",true,1.5,` +
		`"0193a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b",18446744073709551615,"2026-10-19T17:02:03.123456789Z","AP8="]`
	if got, _ := json.Marshal(values(d)); string(got) != wantValues {
		t.Fatalf("values\n got %s\nwant %s", got, wantValues)
	}

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
		s := encode(t, veloxgql.Cursor{Type: "User", Key: k.key})
		if want := `{"v":1,"t":"User","o":[],"k":` + k.json + `}`; cursorJSON(t, s) != want {
			t.Errorf("key %v: %s, want %s", k.key, cursorJSON(t, s), want)
		}
	}
	d, err = veloxgql.DecodeCursor(encode(t, veloxgql.Cursor{Type: "User", Key: u}))
	if err != nil {
		t.Fatal(err)
	}
	if d.Key != u.String() {
		t.Errorf("a UUID key decodes to %#v, want its string", d.Key)
	}

	// Floats that JSON has no number for are strings.
	for _, f := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		d, err := veloxgql.DecodeCursor(encode(t, veloxgql.Cursor{Type: "User", Terms: terms(f), Key: 1}))
		if err != nil {
			t.Fatal(err)
		}
		if want := strconv.FormatFloat(f, 'g', -1, 64); d.Terms[0].Value != want {
			t.Errorf("float %v decoded to %#v, want %q", f, d.Terms[0].Value, want)
		}
	}

	// The scalar: a JSON string on the wire, both ways.
	s = encode(t, c)
	var buf bytes.Buffer
	if err := c.MarshalGQLContext(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	if want := `"` + s + `"`; buf.String() != want {
		t.Errorf("MarshalGQLContext = %s, want %s", &buf, want)
	}
	var back veloxgql.Cursor
	if err := back.UnmarshalGQLContext(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if encode(t, back) != s {
		t.Errorf("UnmarshalGQLContext then EncodeCursor = %q, want %q", encode(t, back), s)
	}
}

func TestCursorMalformed(t *testing.T) {
	doc := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	good := doc(`{"v":1,"t":"User","o":[],"k":1}`)
	if _, err := veloxgql.DecodeCursor(good); err != nil {
		t.Fatalf("the good cursor: %v", err)
	}
	// A variant of good's last character with other trailing bits: it
	// decodes to the same bytes unless the decoder checks them.
	trailing := trailingBits(good)
	if b, err := base64.RawURLEncoding.DecodeString(trailing); err != nil || string(b) != cursorJSON(t, good) {
		t.Fatalf("the trailing-bits variant does not test what it should: %q, %v", b, err)
	}
	for name, s := range map[string]string{
		"empty":                   "",
		"bad base64":              "!!",
		"padded":                  base64.URLEncoding.EncodeToString([]byte(`{"v":1,"t":"User","o":[],"k":1}`)),
		"trailing bits":           trailing,
		"newline":                 good[:4] + "\n" + good[4:],
		"carriage return":         good[:4] + "\r" + good[4:],
		"trailing newline":        good + "\n",
		"not JSON":                doc("not json"),
		"not an object":           doc(`[1]`),
		"version 2":               doc(`{"v":2,"t":"User","o":[],"k":1}`),
		"no version":              doc(`{"t":"User","o":[],"k":1}`),
		"version string":          doc(`{"v":"1","t":"User","o":[],"k":1}`),
		"no type":                 doc(`{"v":1,"o":[],"k":1}`),
		"empty type":              doc(`{"v":1,"t":"","o":[],"k":1}`),
		"type a number":           doc(`{"v":1,"t":1,"o":[],"k":1}`),
		"no order":                doc(`{"v":1,"t":"User","k":1}`),
		"null order":              doc(`{"v":1,"t":"User","o":null,"k":1}`),
		"order an object":         doc(`{"v":1,"t":"User","o":{},"k":1}`),
		"term a bare value":       doc(`{"v":1,"t":"User","o":["ann"],"k":1}`),
		"term with no field":      doc(`{"v":1,"t":"User","o":[{"d":"ASC","v":1}],"k":1}`),
		"term with empty field":   doc(`{"v":1,"t":"User","o":[{"f":"","d":"ASC","v":1}],"k":1}`),
		"term with no direction":  doc(`{"v":1,"t":"User","o":[{"f":"NAME","v":1}],"k":1}`),
		"term with bad direction": doc(`{"v":1,"t":"User","o":[{"f":"NAME","d":"asc","v":1}],"k":1}`),
		"term with unknown field": doc(`{"v":1,"t":"User","o":[{"f":"NAME","d":"ASC","v":1,"x":0}],"k":1}`),
		"nested value":            doc(`{"v":1,"t":"User","o":[{"f":"NAME","d":"ASC","v":[1]}],"k":1}`),
		"object value":            doc(`{"v":1,"t":"User","o":[{"f":"NAME","d":"ASC","v":{"a":1}}],"k":1}`),
		"no key":                  doc(`{"v":1,"t":"User","o":[]}`),
		"null key":                doc(`{"v":1,"t":"User","o":[],"k":null}`),
		"key an object":           doc(`{"v":1,"t":"User","o":[],"k":{}}`),
		"key a bool":              doc(`{"v":1,"t":"User","o":[],"k":true}`),
		"unknown field":           doc(`{"v":1,"t":"User","o":[],"k":1,"x":0}`),
		"trailing data":           doc(`{"v":1,"t":"User","o":[],"k":1} 1`),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := veloxgql.DecodeCursor(s); !errors.Is(err, veloxgql.ErrBadCursor) {
				t.Errorf("DecodeCursor = %v, want ErrBadCursor", err)
			}
			var c veloxgql.Cursor
			if err := c.UnmarshalGQLContext(context.Background(), s); !errors.Is(err, veloxgql.ErrBadCursor) {
				t.Errorf("UnmarshalGQLContext = %v, want ErrBadCursor", err)
			}
		})
	}
	var c veloxgql.Cursor
	for _, v := range []any{int64(5), nil, json.Number("1")} {
		if err := c.UnmarshalGQLContext(context.Background(), v); !errors.Is(err, veloxgql.ErrBadCursor) {
			t.Errorf("UnmarshalGQLContext(%#v) = %v, want ErrBadCursor", v, err)
		}
	}
}

func TestEncodeCursorRefuses(t *testing.T) {
	for name, c := range map[string]veloxgql.Cursor{
		"no key":            {Type: "User", Terms: terms(1)},
		"no type":           {Terms: terms(1), Key: 1},
		"no field":          {Type: "User", Terms: []veloxgql.CursorTerm{{Direction: veloxgql.OrderDirectionAsc, Value: 1}}, Key: 1},
		"invalid direction": {Type: "User", Terms: []veloxgql.CursorTerm{{Field: "NAME", Direction: "UP", Value: 1}}, Key: 1},
		"unencodable value": {Type: "User", Terms: terms(make(chan int)), Key: 1},
	} {
		t.Run(name, func(t *testing.T) {
			if s, err := veloxgql.EncodeCursor(c); err == nil {
				t.Errorf("EncodeCursor = %q, want an error", s)
			}
			if err := c.MarshalGQLContext(context.Background(), io.Discard); err == nil {
				t.Error("MarshalGQLContext wrote the cursor, want an error")
			}
			var buf strings.Builder
			func() {
				defer func() {
					if p, _ := recover().(string); !strings.Contains(p, "veloxgql: Cursor.MarshalGQL: the cursor does not encode") || buf.Len() != 0 {
						t.Errorf("MarshalGQL: panic %q, wrote %q; want a panic and nothing written", p, buf.String())
					}
				}()
				c.MarshalGQL(&buf)
			}()
		})
	}
}

// encode is EncodeCursor for a cursor that must encode.
func encode(t *testing.T, c veloxgql.Cursor) string {
	t.Helper()
	s, err := veloxgql.EncodeCursor(c)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// trailingBits is s with other unused bits in its last character, which
// a lenient decoder decodes to the same bytes.
func trailingBits(s string) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	i := strings.IndexByte(alphabet, s[len(s)-1])
	return s[:len(s)-1] + string(alphabet[i^1])
}

// TestCursorLegacy: a cursor veloxe wrote (the parity probe's) decodes
// through UnmarshalGQL and UnmarshalGQLContext with no error, to a Cursor
// whose Key is the legacy cursor, which keeps the error DecodeCursor gives
// its text, for a connection to use or return.
// DecodeCursor still refuses it, and any other text still fails in
// UnmarshalGQL as it did.
func TestCursorLegacy(t *testing.T) {
	for _, s := range []string{"gqFpAaF2wA", "gqFpAqF2plVzZXItNA", "gqFpA6F2FA"} {
		_, want := veloxgql.DecodeCursor(s)
		if !errors.Is(want, veloxgql.ErrBadCursor) {
			t.Fatalf("DecodeCursor(%q) = %v, want ErrBadCursor", s, want)
		}
		for name, un := range map[string]func(*veloxgql.Cursor) error{
			"UnmarshalGQL":        func(c *veloxgql.Cursor) error { return c.UnmarshalGQL(s) },
			"UnmarshalGQLContext": func(c *veloxgql.Cursor) error { return c.UnmarshalGQLContext(context.Background(), s) },
		} {
			c := veloxgql.Cursor{Type: "User", Key: int64(9)}
			if err := un(&c); err != nil {
				t.Errorf("%s(%q) = %v, want no error", name, s, err)
				continue
			}
			l, ok := c.Key.(*gqlcore.LegacyCursor)
			if c.Type != "" || c.Terms != nil || !ok {
				t.Errorf("%s(%q) = %+v, want only the legacy cursor as the key", name, s, c)
				continue
			}
			if l.Err == nil || l.Err.Error() != want.Error() || !errors.Is(l.Err, veloxgql.ErrBadCursor) {
				t.Errorf("%s(%q): kept %v, want %v", name, s, l.Err, want)
			}
		}
	}
	var c veloxgql.Cursor
	if err := c.UnmarshalGQL("garbage!!"); !errors.Is(err, veloxgql.ErrBadCursor) {
		t.Errorf("UnmarshalGQL(garbage!!) = %v, want ErrBadCursor", err)
	}
}
