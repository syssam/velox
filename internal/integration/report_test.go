package integration

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
)

// reportTimes are the invoices' created_at in the report tests, by id, UTC:
// a Wednesday, a Sunday, a Tuesday, a Wednesday, a Wednesday. 3 and 5 are
// in British Summer Time.
var reportTimes = []time.Time{
	time.Date(2025, 12, 31, 23, 30, 0, 0, time.UTC),
	time.Date(2026, 2, 15, 10, 0, 0, 0, time.UTC),
	time.Date(2026, 3, 31, 23, 30, 0, 0, time.UTC),
	time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
	time.Date(2026, 7, 1, 0, 30, 0, 0, time.UTC),
}

// setReportTimes sets each invoice's created_at to its reportTimes', through
// velox, which writes a time on SQLite as its own text.
func setReportTimes(t *testing.T, tg target) {
	t.Helper()
	for i, at := range reportTimes {
		if _, err := tg.db.Exec(ctx, "test", rebind(tg.d, "UPDATE invoices SET created_at = ? WHERE id = ?"), at, i+1); err != nil {
			t.Fatal(err)
		}
	}
}

// truncated reads d of each invoice, by id, formatted in UTC.
func truncated(t *testing.T, tg target, d velox.Term[testmodel.Invoice]) []string {
	t.Helper()
	var out []string
	for r, err := range tg.db.Query[testmodel.Invoice]().Select(d).OrderBy(I.ID.Asc()).Limit(10).Rows(ctx) {
		if err != nil {
			t.Fatal(err)
		}
		var at time.Time
		if err := r.Scan(&at); err != nil {
			t.Fatal(err)
		}
		out = append(out, at.UTC().Format("2006-01-02 15:04"))
	}
	return out
}

// TestDateTruncOnRealDatabases: every unit, an offset and a fiscal year on
// every dialect, each read into a time.Time that is the same instant.
func TestDateTruncOnRealDatabases(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		type inv = testmodel.Invoice
		setReportTimes(t, tg)
		at := I.CreatedAt
		for _, tc := range []struct {
			name string
			d    velox.Term[inv]
			want string
		}{
			{"year", velox.DateTrunc[inv](velox.UnitYear, at), "2025-01-01 00:00 2026-01-01 00:00 2026-01-01 00:00 2026-01-01 00:00 2026-01-01 00:00"},
			{"quarter", velox.DateTrunc[inv](velox.UnitQuarter, at), "2025-10-01 00:00 2026-01-01 00:00 2026-01-01 00:00 2026-04-01 00:00 2026-07-01 00:00"},
			{"month", velox.DateTrunc[inv](velox.UnitMonth, at), "2025-12-01 00:00 2026-02-01 00:00 2026-03-01 00:00 2026-04-01 00:00 2026-07-01 00:00"},
			{"week", velox.DateTrunc[inv](velox.UnitWeek, at), "2025-12-29 00:00 2026-02-09 00:00 2026-03-30 00:00 2026-03-30 00:00 2026-06-29 00:00"},
			{"day", velox.DateTrunc[inv](velox.UnitDay, at), "2025-12-31 00:00 2026-02-15 00:00 2026-03-31 00:00 2026-04-01 00:00 2026-07-01 00:00"},
			{"hour", velox.DateTrunc[inv](velox.UnitHour, at), "2025-12-31 23:00 2026-02-15 10:00 2026-03-31 23:00 2026-04-01 00:00 2026-07-01 00:00"},
			{"month +08:00", velox.DateTrunc[inv](velox.UnitMonth, at, velox.InZone("+08:00")), "2025-12-31 16:00 2026-01-31 16:00 2026-03-31 16:00 2026-03-31 16:00 2026-06-30 16:00"},
			{"fiscal year April", velox.DateTrunc[inv](velox.UnitYear, at, velox.FiscalYear(time.April)), "2025-04-01 00:00 2025-04-01 00:00 2025-04-01 00:00 2026-04-01 00:00 2026-04-01 00:00"},
			{"fiscal quarter February", velox.DateTrunc[inv](velox.UnitQuarter, at, velox.FiscalYear(time.February)), "2025-11-01 00:00 2026-02-01 00:00 2026-02-01 00:00 2026-02-01 00:00 2026-05-01 00:00"},
		} {
			if got := strings.Join(truncated(t, tg, tc.d), " "); got != tc.want {
				t.Errorf("%s:\n got %s\nwant %s", tc.name, got, tc.want)
			}
		}

		// One DateTrunc is the result column, the group and the ordering.
		month := velox.DateTrunc[inv](velox.UnitQuarter, at, velox.InZone("+08:00"))
		wantRows(t, "per quarter", rowsOf(t, ctx, tg.db.Query[inv]().
			Select(velox.CountAll[inv](), I.Total.Sum()).GroupBy(month).OrderBy(month.Asc()).Limit(10), 2),
			[]string{"2 30", "2 70", "1 50"})
	})
}

// TestDateTruncNamedZones: a zone of the IANA database, with summer time,
// on PostgreSQL and MySQL. SQLite has none and refuses it.
func TestDateTruncNamedZones(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		type inv = testmodel.Invoice
		setReportTimes(t, tg)
		london := velox.DateTrunc[inv](velox.UnitMonth, I.CreatedAt, velox.InZone("Europe/London"))
		if tg.d == velox.SQLite {
			for _, err := range tg.db.Query[inv]().Select(london).Limit(1).Rows(ctx) {
				if err == nil || !strings.Contains(err.Error(), "SQLite has no time zones") {
					t.Errorf("got %v", err)
				}
			}
			return
		}
		if tg.d == velox.MySQL {
			var tz *string
			for r, err := range tg.db.Rows(ctx, "test", "SELECT CONVERT_TZ('2026-01-01 00:00:00', '+00:00', 'Europe/London')") {
				if err == nil {
					err = r.Scan(&tz)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			if tz == nil {
				t.Skip("MySQL has no time zone tables: load them with mysql_tzinfo_to_sql")
			}
		}
		want := []string{"2025-12-01 00:00", "2026-02-01 00:00", "2026-03-31 23:00", "2026-03-31 23:00", "2026-06-30 23:00"}
		if got := truncated(t, tg, london); !slices.Equal(got, want) {
			t.Errorf("Europe/London:\n got %q\nwant %q", got, want)
		}
		hk := velox.DateTrunc[inv](velox.UnitDay, I.CreatedAt, velox.InZone("Asia/Hong_Kong"))
		want = []string{"2025-12-31 16:00", "2026-02-14 16:00", "2026-03-31 16:00", "2026-03-31 16:00", "2026-06-30 16:00"}
		if got := truncated(t, tg, hk); !slices.Equal(got, want) {
			t.Errorf("Asia/Hong_Kong:\n got %q\nwant %q", got, want)
		}
	})
}

// TestRollupOnRealDatabases: Rollup's subtotal rows on PostgreSQL and MySQL,
// told from the groups by GROUPING, and Cube's on PostgreSQL; SQLite refuses
// both. The invoices are TestSelect's.
func TestRollupOnRealDatabases(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		type inv = testmodel.Invoice
		grouping := velox.Calc[inv]("GROUPING(?)", I.OrgID)
		rollup := tg.db.Query[inv]().
			Select(I.OrgID, I.Total.Sum(), grouping).
			GroupBy(velox.Rollup[inv](I.OrgID)).
			OrderBy(grouping.Asc(), I.OrgID.Asc()).Limit(10)
		if tg.d == velox.SQLite {
			for _, err := range rollup.Rows(ctx) {
				if err == nil || !strings.Contains(err.Error(), "SQLite has no ROLLUP") {
					t.Errorf("got %v", err)
				}
			}
			return
		}
		wantRows(t, "rollup", rowsOf(t, ctx, rollup, 3), []string{"1 100 0", "2 50 0", "<nil> 150 1"})
		if tg.d != velox.Postgres {
			return
		}
		cube := rowsOf(t, ctx, tg.db.Query[inv]().
			Select(velox.Calc[inv]("GROUPING(?, ?)", I.OrgID, I.Paid), I.Total.Sum()).
			GroupBy(velox.Cube[inv](I.OrgID, I.Paid)).
			OrderBy(velox.Calc[inv]("GROUPING(?, ?)", I.OrgID, I.Paid).Desc()).Limit(20), 2)
		if len(cube) != 8 || cube[0] != "3 150" {
			t.Errorf("cube: got %q, want 8 rows, the grand total first", cube)
		}
	})
}
