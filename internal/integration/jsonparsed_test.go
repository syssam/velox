package integration

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox"
)

// jsonParsedSetup inserts records whose attrs hold, at "n", a number, a
// numeric string or junk, and at "t" an RFC 3339 time in some offset or
// junk; at "o"."n" and "o"."t" a nested pair. Records 1, 2 and 3 hold the
// same instant, 2026-03-01T02:00:00Z, in three offsets.
func jsonParsedSetup(t *testing.T, tg target) {
	t.Helper()
	jsonSetup(t, tg)
	for _, r := range []jrec{
		{ID: 1, Attrs: map[string]any{"n": 10, "t": "2026-03-01T10:00:00+08:00", "o": map[string]any{"n": "2.5", "t": "2026-03-01T02:00:00Z"}}},
		{ID: 2, Attrs: map[string]any{"n": "10", "t": "2026-03-01T02:00:00Z"}},
		{ID: 3, Attrs: map[string]any{"n": "-2.5", "t": "2026-02-28T20:30:00-05:30"}},
		{ID: 4, Attrs: map[string]any{"n": "1e3", "t": "2026-03-01T01:59:59.999999Z"}},
		{ID: 5, Attrs: map[string]any{"n": "1E+02", "t": "2026-03-01T02:00:00.5+00:00"}},
		{ID: 6, Attrs: map[string]any{"n": "0.5", "t": "2026-03-01T02:00:00.000001-00:00"}},
		{ID: 7, Attrs: map[string]any{"n": 7.25, "t": "2024-02-29T23:59:59.25+14:00"}},
		{ID: 8, Attrs: map[string]any{"n": "abc", "t": "2026-03-01 02:00:00Z"}},
		{ID: 9, Attrs: map[string]any{"n": true, "t": "2026-02-30T00:00:00Z"}},
		{ID: 10, Attrs: map[string]any{"n": "01", "t": "2026-03-01T02:00:00"}},
		{ID: 11, Attrs: map[string]any{"t": 5}},
		{ID: 12, Attrs: map[string]any{"n": " 7", "t": "2026-03-01T02:00:00+15:00"}},
		{ID: 13, Attrs: map[string]any{"n": map[string]any{"x": 1}, "t": "9999-12-31T23:00:00-05:00"}},
		{ID: 14, Attrs: map[string]any{"n": "1e100", "t": "2026-03-01t02:00:00z"}},
		{ID: 15, Attrs: map[string]any{"n": "+1", "t": "2023-02-29T00:00:00Z"}},
		{ID: 16, Attrs: map[string]any{"n": "1.", "t": "2026-03-01T24:00:00Z"}},
		{ID: 17, Attrs: map[string]any{"n": ".5", "t": "2026-03-01T02:00:60Z"}},
		{ID: 18, Attrs: map[string]any{"n": "1,000", "t": "2026-03-01T02:00:00.Z"}},
		{ID: 19, Attrs: map[string]any{"n": "NaN", "t": "2026-03-01T02:00:00.1234567890Z"}},
		{ID: 20, Attrs: map[string]any{"n": "10\n", "t": "2026-03-01T02:00:00Z\n"}},
		{ID: 21, Attrs: map[string]any{"n": "", "t": "2026-03-01T02:00:00 Z"}},
		{ID: 22, Attrs: map[string]any{"n": []any{1}, "t": "0999-03-01T02:00:00Z"}},
		{ID: 23, Attrs: map[string]any{"n": nil, "t": "2026-03-01T02:00:00+08:00Z"}},
		{ID: 24, Attrs: map[string]any{"n": "1e-2", "t": "2026-13-01T02:00:00Z"}},
	} {
		if err := tg.db.Insert(&r).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

// TestJSONTextNumber: Number of a JSONText reads a JSON number or a string
// that spells one in JSON's grammar, numerically, and reads anything else
// as NULL without an error, in a read, a write's Where, an order and a
// result column, on every dialect.
func TestJSONTextNumber(t *testing.T) {
	eachJSON(t, func(t *testing.T, tg target) {
		jsonParsedSetup(t, tg)
		n, all := JR.Attrs.Text("n").Number(), velox.And[jrec]()

		wantIDs(t, jsonIDs(t, tg, n.EQ(10)), []int64{1, 2})
		wantIDs(t, jsonIDs(t, tg, n.GT(5)), []int64{1, 2, 4, 5, 7})
		wantIDs(t, jsonIDs(t, tg, n.LT(1)), []int64{3, 6, 24})
		wantIDs(t, jsonIDs(t, tg, n.Between(0.01, 0.5)), []int64{6, 24})
		wantIDs(t, jsonIDs(t, tg, n.NEQ(10)), []int64{3, 4, 5, 6, 7, 24})
		wantIDs(t, jsonIDs(t, tg, velox.Not(n.GT(5))), []int64{3, 6, 24})
		wantIDs(t, jsonIDs(t, tg, velox.Or(n.EQ(1000), n.EQ(-2.5))), []int64{3, 4})
		wantIDs(t, jsonIDs(t, tg, n.NotNull()), []int64{1, 2, 3, 4, 5, 6, 7, 24})
		wantIDs(t, jsonIDs(t, tg, n.IsNull()), []int64{8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23})
		wantIDs(t, jsonIDs(t, tg, JR.Attrs.Text("o", "n").Number().EQ(2.5)), []int64{1})
		wantIDs(t, jsonIDs(t, tg, JR.Attrs.Number("n").EQ(10)), []int64{1}) // a JSON handle's Number reads no string
		wantIDs(t, jsonIDs(t, tg, n.NotNull(), n.Asc(), JR.ID.Asc()), []int64{3, 24, 6, 7, 1, 2, 5, 4})
		wantIDs(t, jsonIDs(t, tg, n.NotNull(), n.Desc(), JR.ID.Asc()), []int64{4, 5, 1, 2, 7, 6, 24, 3})
		wantIDs(t, jsonIDs(t, tg, all, n.Asc(), JR.ID.Asc())[:8], []int64{3, 24, 6, 7, 1, 2, 5, 4}) // NULL last

		// A write's Where reads junk as NULL too, which MySQL's strict mode
		// would make an error of a failed cast.
		res, err := tg.db.Update[jrec]().Set(JR.Tags.Set([]string{"x"})).Where(n.GT(5)).Exec(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if res != 5 {
			t.Errorf("update affected %d rows, want 5", res)
		}

		type row struct {
			ID int64
			N  *float64 `velox:"n"`
		}
		got, err := velox.Scan[row](ctx, tg.db.Query[jrec]().Select(JR.ID, n.Term().As("n")).Where(JR.ID.In(2, 3, 4, 8)).OrderBy(JR.ID.Asc()).Limit(10))
		if err != nil {
			t.Fatal(err)
		}
		want := []float64{10, -2.5, 1000}
		if len(got) != 4 || got[3].N != nil {
			t.Fatalf("got %+v", got)
		}
		for i, w := range want {
			if got[i].N == nil || *got[i].N != w {
				t.Errorf("row %d: n %v, want %v", got[i].ID, got[i].N, w)
			}
		}
	})
}

// TestJSONTextTime: Time of a JSONText reads an RFC 3339 string as its
// instant, whatever its offset, and anything else as NULL without an
// error, in a read, a write's Where, an order and a result column, on every
// dialect.
func TestJSONTextTime(t *testing.T) {
	eachJSON(t, func(t *testing.T, tg target) {
		jsonParsedSetup(t, tg)
		tm, all := JR.Attrs.Text("t").Time(), velox.And[jrec]()
		at := time.Date(2026, 3, 1, 2, 0, 0, 0, time.UTC)
		east := at.In(time.FixedZone("east", 9*3600))

		wantIDs(t, jsonIDs(t, tg, tm.EQ(at)), []int64{1, 2, 3})
		wantIDs(t, jsonIDs(t, tg, tm.EQ(east)), []int64{1, 2, 3}) // the argument is an instant too
		wantIDs(t, jsonIDs(t, tg, tm.GT(at)), []int64{5, 6})
		wantIDs(t, jsonIDs(t, tg, tm.GTE(at)), []int64{1, 2, 3, 5, 6})
		wantIDs(t, jsonIDs(t, tg, tm.LT(at)), []int64{4, 7})
		wantIDs(t, jsonIDs(t, tg, tm.LTE(at.Add(-time.Microsecond))), []int64{4, 7})
		wantIDs(t, jsonIDs(t, tg, tm.NEQ(at)), []int64{4, 5, 6, 7})
		wantIDs(t, jsonIDs(t, tg, tm.Between(at.Add(time.Microsecond), at.Add(time.Second))), []int64{5, 6})
		wantIDs(t, jsonIDs(t, tg, velox.Not(tm.GTE(at))), []int64{4, 7})
		wantIDs(t, jsonIDs(t, tg, velox.Or(tm.LT(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)), tm.EQ(at.Add(500*time.Millisecond)))), []int64{5, 7})
		wantIDs(t, jsonIDs(t, tg, tm.NotNull()), []int64{1, 2, 3, 4, 5, 6, 7})
		wantIDs(t, jsonIDs(t, tg, tm.IsNull()), []int64{8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24})
		wantIDs(t, jsonIDs(t, tg, JR.Attrs.Text("o", "t").Time().EQ(at)), []int64{1})
		wantIDs(t, jsonIDs(t, tg, tm.NotNull(), tm.Asc(), JR.ID.Asc()), []int64{7, 4, 1, 2, 3, 6, 5})
		wantIDs(t, jsonIDs(t, tg, tm.NotNull(), tm.Desc(), JR.ID.Asc()), []int64{5, 6, 1, 2, 3, 4, 7})
		wantIDs(t, jsonIDs(t, tg, all, tm.Asc(), JR.ID.Asc())[:7], []int64{7, 4, 1, 2, 3, 6, 5}) // NULL last

		res, err := tg.db.Update[jrec]().Set(JR.Tags.Set([]string{"x"})).Where(tm.GTE(at)).Exec(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if res != 5 {
			t.Errorf("update affected %d rows, want 5", res)
		}

		type row struct {
			ID int64
			T  *time.Time `velox:"t"`
		}
		got, err := velox.Scan[row](ctx, tg.db.Query[jrec]().Select(JR.ID, tm.Term().As("t")).Where(JR.ID.In(1, 3, 5, 7, 8)).OrderBy(JR.ID.Asc()).Limit(10))
		if err != nil {
			t.Fatal(err)
		}
		want := []time.Time{at, at, at.Add(500 * time.Millisecond), time.Date(2024, 2, 29, 9, 59, 59, 250_000_000, time.UTC)}
		if len(got) != 5 || got[4].T != nil {
			t.Fatalf("got %+v", got)
		}
		for i, w := range want {
			if got[i].T == nil || !got[i].T.Equal(w) {
				t.Errorf("row %d: t %v, want %v", got[i].ID, got[i].T, w)
			}
		}
	})
}

// parsedSamples are strings near the forms Number and Time read: valid
// ones, each with every character replaced by a few others, cut short and
// lengthened, and every day around the ends of months and leap days.
func parsedSamples() []string {
	bases := []string{
		"2026-03-01T10:00:00+08:00", "2024-02-29T23:59:59.123456Z", "1999-12-31T00:00:00.5-14:59",
		"10", "-2.5", "1e3", "0.25E-07", "123456789012345678901234567890",
	}
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, b := range bases {
		add(b)
		for i := range len(b) {
			for _, c := range "09125+-.:TZtze \n" {
				add(b[:i] + string(c) + b[i+1:])
			}
			add(b[:i])
			add(b[:i] + "0" + b[i:])
		}
	}
	for _, y := range []string{"1000", "1900", "2000", "2023", "2024", "9999"} {
		for _, md := range []string{"01-31", "02-28", "02-29", "02-30", "04-30", "04-31", "12-31", "13-01", "00-10", "06-00"} {
			add(y + "-" + md + "T12:00:00Z")
			add(y + "-" + md + "T00:30:00+01:00")
			add(y + "-" + md + "T23:30:00-01:00")
		}
	}
	for _, s := range []string{"-0", "1e99", "1e100", "1e-99", strings.Repeat("9", 200), strings.Repeat("9", 201), "Infinity", "0x10", "1_000", `"1"`} {
		add(s)
	}
	return out
}

// numberRef is the number Number reads from the string s, by Go's rules:
// JSON's grammar, at most 200 characters and an exponent of at most two
// digits.
func numberRef(s string) (float64, bool) {
	if s == "" || len(s) > 200 || !json.Valid([]byte(s)) || strings.Trim(s, "-+.0123456789eE") != "" {
		return 0, false
	}
	if i := strings.IndexAny(s, "eE"); i >= 0 && len(strings.TrimLeft(s[i+1:], "+-")) > 2 {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	return f, err == nil
}

// timeRef is the instant Time reads from the string s, by Go's RFC 3339
// parser: a fraction of at most nine digits, Z or an offset within ±14:59,
// and a date of a year from 1000 to 9999, 9999-12-31 aside.
func timeRef(s string) (time.Time, bool) {
	tm, err := time.Parse(time.RFC3339Nano, s)
	if err != nil || len(s) < 20 || s[10] != 'T' || strings.ContainsAny(s, "tz \n") ||
		s[0] == '0' || s[:10] == "9999-12-31" {
		return time.Time{}, false
	}
	zone := "Z"
	if !strings.HasSuffix(s, "Z") {
		zone = s[len(s)-6:]
		if zone[1:3] > "14" {
			return time.Time{}, false
		}
	}
	if frac := s[19 : len(s)-len(zone)]; frac != "" && (len(frac) < 2 || len(frac) > 10) {
		return time.Time{}, false
	}
	return tm.UTC(), true
}

// TestJSONTextParsedParity: on every dialect, Number and Time read exactly
// the strings Go's reference reads, as the same values, and the rest as
// NULL, among strings a character away from those they read.
func TestJSONTextParsedParity(t *testing.T) {
	samples := parsedSamples()
	eachJSON(t, func(t *testing.T, tg target) {
		jsonSetup(t, tg)
		for i := 0; i < len(samples); i += 200 {
			var qs []velox.InsertQuery[jrec]
			for j := i; j < min(i+200, len(samples)); j++ {
				qs = append(qs, tg.db.Insert(&jrec{ID: int64(j + 1), Attrs: map[string]any{"v": samples[j]}}))
			}
			if err := tg.db.InsertBulk(qs...).Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}
		type row struct {
			ID int64
			N  *float64   `velox:"n"`
			T  *time.Time `velox:"t"`
		}
		v := JR.Attrs.Text("v")
		got, err := velox.Scan[row](ctx, tg.db.Query[jrec]().Select(JR.ID, v.Number().Term().As("n"), v.Time().Term().As("t")).
			OrderBy(JR.ID.Asc()).AllowUnbounded())
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(samples) {
			t.Fatalf("%d rows, want %d", len(got), len(samples))
		}
		numbers, times := 0, 0
		for _, r := range got {
			s := samples[r.ID-1]
			if f, ok := numberRef(s); ok != (r.N != nil) || ok && *r.N != f {
				t.Errorf("Number of %q: %v, want %v %v", s, r.N, f, ok)
			} else if ok {
				numbers++
			}
			// PostgreSQL and MySQL round a time to the microsecond.
			if tm, ok := timeRef(s); ok != (r.T != nil) || ok && !r.T.Equal(tm) && !r.T.Equal(tm.Round(time.Microsecond)) {
				t.Errorf("Time of %q: %v, want %v %v", s, r.T, tm, ok)
			} else if ok {
				times++
			}
		}
		if numbers < 50 || times < 50 {
			t.Errorf("%d numbers and %d times read: too few samples are valid", numbers, times)
		}
	})
}
