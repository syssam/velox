package gqlrelay

import (
	"bytes"
	"encoding/base64"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/vmihailenco/msgpack/v5"

	"github.com/syssam/velox"
)

// referenceCursorToken is the cursor encoding as MarshalGQL first shipped
// it: a fresh msgpack encoder streaming into a fresh base64 encoder. Tokens
// are held by clients between requests, so the pooled implementation must
// produce the same bytes.
func referenceCursorToken(t *testing.T, c Cursor) string {
	t.Helper()
	var body bytes.Buffer
	enc := base64.NewEncoder(base64.RawStdEncoding, &body)
	c.Value = wrapTime(c.Value)
	if len(c.Values) > 0 {
		values := make([]velox.Value, len(c.Values))
		for i, v := range c.Values {
			values[i] = wrapTime(v)
		}
		c.Values = values
	}
	require.NoError(t, msgpack.NewEncoder(enc).Encode(c))
	require.NoError(t, enc.Close())
	return `"` + body.String() + `"`
}

func cursorShapes() []Cursor {
	at := time.Date(2026, 1, 1, 0, 0, 0, 123456789, time.FixedZone("HKT", 8*3600))
	return []Cursor{
		{ID: 1},
		{ID: int64(1 << 40)},
		{ID: "0195f2c0-7b7e-7c3a-9d5e-3b1f0a2c4d6e"},
		{ID: 7, Value: "a title long enough that the token is more than one base64 block"},
		{ID: 7, Value: at},
		{ID: 7, Value: 3.5},
		{ID: 7, Value: nil, Values: []velox.Value{"x", at, int64(9), nil}},
		{ID: 7, Value: []any{"x", at}},
	}
}

func TestCursorMarshalGQL_SameBytesAsTheReferenceEncoding(t *testing.T) {
	// Twice over the shapes, so every token is also produced from a pooled
	// buffer that last held a different (and a longer) one.
	for range 2 {
		for i, c := range cursorShapes() {
			want := referenceCursorToken(t, c)
			var got bytes.Buffer
			c.MarshalGQL(&got)
			require.Equal(t, want, got.String(), "shape %d", i)
		}
	}
}

func TestCursorMarshalGQL_UnencodableValueIsAnEmptyToken(t *testing.T) {
	var got bytes.Buffer
	Cursor{ID: 1, Value: make(chan int)}.MarshalGQL(&got)
	require.Equal(t, `""`, got.String())
	// The pooled buffer must not leak the failed attempt into the next token.
	got.Reset()
	Cursor{ID: 1}.MarshalGQL(&got)
	require.Equal(t, referenceCursorToken(t, Cursor{ID: 1}), got.String())
}

func TestCursorMarshalGQL_Concurrent(t *testing.T) {
	shapes := cursorShapes()
	want := make([]string, len(shapes))
	for i, c := range shapes {
		want[i] = referenceCursorToken(t, c)
	}
	var wg sync.WaitGroup
	errs := make(chan string, 64)
	for g := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := range 200 {
				i := (g + n) % len(shapes)
				var got bytes.Buffer
				shapes[i].MarshalGQL(&got)
				if got.String() != want[i] {
					select {
					case errs <- got.String():
					default:
					}
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatalf("a concurrent MarshalGQL produced a wrong token: %s", e)
	}
}

func BenchmarkCursorMarshalGQL(b *testing.B) {
	c := Cursor{ID: 12345, Value: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	var buf bytes.Buffer
	b.ReportAllocs()
	for b.Loop() {
		buf.Reset()
		c.MarshalGQL(&buf)
	}
}
