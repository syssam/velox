package gqlrelay

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/stretchr/testify/require"
	"github.com/vmihailenco/msgpack/v5"

	"github.com/syssam/velox"
)

// TestCursor_TimeKeepsItsOffset pins that a time order value survives the
// cursor round trip with its UTC offset. msgpack's time extension keeps only
// the instant and decodes it in the local zone, so the next page bound
// "2026-01-01 08:00:00+08:00" against a stored "2026-01-01 00:00:00+00:00".
// SQLite compares time columns as text, and forward paging returned an
// empty second page; backward paging repeated a page. (Ent's entgql encodes
// cursors the same way.)
func TestCursor_TimeKeepsItsOffset(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	for _, loc := range []*time.Location{time.UTC, ny, time.FixedZone("HKT", 8*3600), time.FixedZone("", -5*3600)} {
		at := time.Date(2026, 1, 1, 0, 0, 0, 123456789, loc)
		in := Cursor{ID: 7, Value: at, Values: []velox.Value{"x", at}}
		multi := Cursor{ID: 7, Value: []any{"x", at}} // multi-order cursors nest values in Value
		var mbuf bytes.Buffer
		multi.MarshalGQL(&mbuf)
		var mout Cursor
		require.NoError(t, mout.UnmarshalGQL(strings.Trim(mbuf.String(), `"`)))
		require.Equal(t, at.String(), mout.Value.([]any)[1].(time.Time).String(), "multi-order value for %v", loc)

		var buf bytes.Buffer
		in.MarshalGQL(&buf)
		var out Cursor
		require.NoError(t, out.UnmarshalGQL(strings.Trim(buf.String(), `"`)))

		for _, v := range []any{out.Value, out.Values[1]} {
			got, ok := v.(time.Time)
			require.True(t, ok, "decoded %T, want time.Time", v)
			require.Equal(t, at.Format(time.RFC3339Nano), got.Format(time.RFC3339Nano), "offset lost for %v", loc)
			// SQLite (modernc) binds a time as t.String(), zone name included.
			require.Equal(t, at.String(), got.String(), "SQLite text form changed for %v", loc)
		}
		require.Equal(t, "x", out.Values[0])
	}
}

// TestCursor_DecodesTokensOfTheOldTimeEncoding pins that cursors issued
// before the offset-preserving encoding — msgpack's plain time extension —
// still decode, so clients holding one keep paging.
func TestCursor_DecodesTokensOfTheOldTimeEncoding(t *testing.T) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var body bytes.Buffer
	enc := base64.NewEncoder(base64.RawStdEncoding, &body)
	require.NoError(t, msgpack.NewEncoder(enc).Encode(struct {
		ID    any `msgpack:"i"`
		Value any `msgpack:"v"`
	}{ID: 1, Value: at}))
	require.NoError(t, enc.Close())

	var out Cursor
	require.NoError(t, out.UnmarshalGQL(body.String()))
	got, ok := out.Value.(time.Time)
	require.True(t, ok, "decoded %T", out.Value)
	require.True(t, got.Equal(at))
}
