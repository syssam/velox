package engine

import (
	"errors"
	"strings"
	"testing"
)

// TestDateText: each unit's SQL on each dialect, with @ for the operand. A
// zone and a fiscal year are literals in the text, never arguments.
func TestDateText(t *testing.T) {
	x := func(u DateUnit, zone string, fiscal int) *termExt {
		e := &termExt{unit: u, fiscal: fiscal}
		if err := e.setZone(zone); err != nil {
			t.Fatal(err)
		}
		return e
	}
	for _, tc := range []struct {
		d    Dialect
		x    *termExt
		want string
	}{
		{Postgres, x(DateMonth, "", 1), `(date_trunc('month', (@ AT TIME ZONE 'UTC')) AT TIME ZONE 'UTC')`},
		{Postgres, x(DateWeek, "Europe/London", 1), `(date_trunc('week', (@ AT TIME ZONE 'Europe/London')) AT TIME ZONE 'Europe/London')`},
		{Postgres, x(DateDay, "+08:00", 1), `(date_trunc('day', (@ AT TIME ZONE INTERVAL '+08:00')) AT TIME ZONE INTERVAL '+08:00')`},
		{Postgres, x(DateYear, "UTC", 4), `((date_trunc('month', (@ AT TIME ZONE 'UTC')) - ((EXTRACT(MONTH FROM (@ AT TIME ZONE 'UTC'))::int + 8) % 12) * INTERVAL '1 month') AT TIME ZONE 'UTC')`},
		{MySQL, x(DateYear, "", 1), "CAST(DATE_FORMAT(@, '%Y-01-01') AS DATETIME)"},
		{MySQL, x(DateQuarter, "", 1), "(CAST(DATE_FORMAT(@, '%Y-%m-01') AS DATETIME) - INTERVAL ((MONTH(@) + 11) % 3) MONTH)"},
		{MySQL, x(DateMonth, "", 1), "CAST(DATE_FORMAT(@, '%Y-%m-01') AS DATETIME)"},
		{MySQL, x(DateWeek, "", 1), "CAST(DATE(@) - INTERVAL WEEKDAY(@) DAY AS DATETIME)"},
		{MySQL, x(DateDay, "", 1), "CAST(DATE(@) AS DATETIME)"},
		{MySQL, x(DateHour, "-05:30", 1), "CONVERT_TZ(CAST(DATE_FORMAT(CONVERT_TZ(@, '+00:00', '-05:30'), '%Y-%m-%d %H:00:00') AS DATETIME), '-05:30', '+00:00')"},
		{MySQL, x(DateMonth, "Asia/Hong_Kong", 1), "CONVERT_TZ(CAST(DATE_FORMAT(CONVERT_TZ(@, '+00:00', 'Asia/Hong_Kong'), '%Y-%m-01') AS DATETIME), 'Asia/Hong_Kong', '+00:00')"},
		{SQLite, x(DateYear, "", 1), "datetime(@, 'start of year')"},
		{SQLite, x(DateYear, "", 7), "datetime(@, 'start of month', '-' || ((CAST(strftime('%m', @) AS INTEGER) + 5) % 12) || ' months')"},
		{SQLite, x(DateMonth, "", 1), "datetime(@, 'start of month')"},
		{SQLite, x(DateWeek, "", 1), "datetime(@, 'start of day', '-6 days', 'weekday 1')"},
		{SQLite, x(DateDay, "+00:00", 1), "datetime(@, 'start of day')"},
		{SQLite, x(DateHour, "+08:00", 1), "datetime(strftime('%Y-%m-%d %H:00:00', @, '+480 minutes'), '-480 minutes')"},
		{SQLite, x(DateQuarter, "-01:00", 1), "datetime(datetime(@, '-60 minutes', 'start of month', '-' || ((CAST(strftime('%m', @, '-60 minutes') AS INTEGER) + 11) % 3) || ' months'), '+60 minutes')"},
	} {
		if got := dateText(tc.d, tc.x); got != tc.want {
			t.Errorf("%v %+v:\n got %s\nwant %s", tc.d, *tc.x, got, tc.want)
		}
	}
}

// TestDateTruncRefuses: a bad unit, zone, fiscal month or operand is an
// InvalidTerm whose error says what is wrong.
func TestDateTruncRefuses(t *testing.T) {
	errBoom := errors.New("boom")
	col := ColumnTerm(0)
	for _, tc := range []struct {
		t    Term
		want string
	}{
		{DateTruncTerm(0, col, "", 1), "unit 0"},
		{DateTruncTerm(DateHour+1, col, "", 1), "unit 7"},
		{DateTruncTerm(DateYear, col, "", 13), "month 1 to 12"},
		{DateTruncTerm(DateMonth, col, "", 4), "not months"},
		{DateTruncTerm(DateDay, col, "Europe/London'; --", 1), "not a time zone's name"},
		{DateTruncTerm(DateDay, col, "1Europe", 1), "not a time zone's name"},
		{DateTruncTerm(DateDay, col, "+15:00", 1), "within ±14:00"},
		{DateTruncTerm(DateDay, col, "+08:60", 1), "within ±14:00"},
		{DateTruncTerm(DateDay, col, "+8:00", 1), "within ±14:00"},
		{DateTruncTerm(DateDay, col, "-+8:00", 1), "within ±14:00"},
		{DateTruncTerm(DateDay, Term{}, "", 1), "of no term"},
		{DateTruncTerm(DateDay, ExprTerm("COUNT(*) OVER ()", nil, nil), "", 1), "window"},
		{DateTruncTerm(DateDay, GroupingTerm(false, []Term{col}), "", 1), "Rollup or Cube"},
		{DateTruncTerm(DateDay, InvalidTerm(errBoom), "", 1), "boom"},
		{GroupingTerm(true, nil), "Cube of no keys"},
		{GroupingTerm(false, []Term{AggTerm(TermSum, col)}), "Rollup of an aggregate"},
		{GroupingTerm(false, []Term{InvalidTerm(errBoom)}), "boom"},
		{GroupingTerm(false, []Term{GroupingTerm(true, []Term{col})}), "Rollup of a Rollup or Cube"},
	} {
		err := tc.t.problem()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("got %v, want an error with %q", err, tc.want)
		}
	}
	if err := DateTruncTerm(DateDay, col, "-14:00", 1).problem(); err != nil {
		t.Errorf("-14:00: %v", err)
	}
}

// TestDateTruncShape: the zone, the unit and the fiscal month shape the
// render cache's key, as they shape the text; a grouping set's keys and
// kind do too.
func TestDateTruncShape(t *testing.T) {
	col := ColumnTerm(0)
	shapes := map[string]string{}
	for name, x := range map[string]Term{
		"month":        DateTruncTerm(DateMonth, col, "", 1),
		"month London": DateTruncTerm(DateMonth, col, "Europe/London", 1),
		"month Paris":  DateTruncTerm(DateMonth, col, "Europe/Paris", 1),
		"day":          DateTruncTerm(DateDay, col, "", 1),
		"year":         DateTruncTerm(DateYear, col, "", 1),
		"year April":   DateTruncTerm(DateYear, col, "", 4),
		"month col 1":  DateTruncTerm(DateMonth, ColumnTerm(1), "", 1),
		"rollup":       GroupingTerm(false, []Term{col}),
		"cube":         GroupingTerm(true, []Term{col}),
		"rollup 2":     GroupingTerm(false, []Term{col, ColumnTerm(1)}),
	} {
		if other, ok := shapes[x.shape]; ok {
			t.Errorf("%s and %s have one shape", name, other)
		}
		shapes[x.shape] = name
	}
	if DateTruncTerm(DateMonth, col, "UTC", 1).shape != DateTruncTerm(DateMonth, col, "", 1).shape {
		t.Error("UTC and no zone shape differently")
	}
}

// TestDateTruncSwap: on a bidirectional join entity a DateTrunc's and a
// grouping set's columns 0 and 1 trade places.
func TestDateTruncSwap(t *testing.T) {
	d := swapTerm(DateTruncTerm(DateDay, ColumnTerm(0), "", 1))
	if c, _ := TermColumn(*d.of); c != 1 {
		t.Errorf("swapped DateTrunc names column %d", c)
	}
	g := swapTerm(GroupingTerm(false, []Term{ColumnTerm(1), ColumnTerm(2)}))
	if a, _ := TermColumn(g.ext.set[0]); a != 0 {
		t.Errorf("swapped Rollup names column %d first", a)
	}
}
