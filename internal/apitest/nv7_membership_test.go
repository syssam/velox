package apitest

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox"
)

// membership is a row of a dated membership table, read first under its own
// entity's policy: the store (here an org) belonged to group from from,
// until to (the zero time: open-ended).
type membership struct {
	org      int64
	group    int64
	from, to time.Time
}

// groupOf is the group each invoice's org belonged to on the invoice's
// date, as a CASE over the invoice's own columns with the memberships bound:
// NULL where none was in effect. It is how a port says a join to a dated
// membership table, with no edge, in one statement (ADR 0142).
func groupOf(ms []membership) velox.Term[inv] {
	var b strings.Builder
	var args []any
	b.WriteString("CASE")
	for _, m := range ms {
		b.WriteString(" WHEN ? = ? AND ? >= ?")
		args = append(args, I.OrgID, m.org, I.CreatedAt, m.from)
		if !m.to.IsZero() {
			b.WriteString(" AND ? < ?")
			args = append(args, I.CreatedAt, m.to)
		}
		// The group is a literal: a bound THEN has no type on PostgreSQL,
		// which would read the CASE as text.
		b.WriteString(" THEN " + strconv.FormatInt(m.group, 10))
	}
	b.WriteString(" END")
	return velox.Calc[inv](b.String(), args...)
}

// TestGroupByADatedMembership: invoices grouped by the group their org was
// in on the invoice's date; an invoice in no group is left out by a
// condition on the same term.
func TestGroupByADatedMembership(t *testing.T) {
	db := reportDB(t) // org 1: ids 1 (2025-12-31), 2 (2026-02-15), 3 (2026-03-31); org 2: id 4 (2026-04-01)
	day := func(s string) time.Time { d, _ := time.Parse("2006-01-02", s); return d }
	g := groupOf([]membership{
		{org: 1, group: 100, from: day("2025-01-01"), to: day("2026-03-01")},
		{org: 1, group: 200, from: day("2026-03-01")},
		{org: 2, group: 300, from: day("2026-05-01")}, // after invoice 4
	})
	type row struct {
		Group int64
		N     int64
		Sum   float64
	}
	got, err := velox.Scan[row](ctx, db.Query[inv]().
		Where(velox.Expr[inv]("? IS NOT NULL", g)).
		Select(g.As("group"), velox.CountAll[inv]().As("n"), I.Total.Sum().As("sum")).
		GroupBy(g).OrderBy(g.Asc()).Limit(10))
	if want := []row{{100, 2, 30}, {200, 1, 30}}; err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("%v %v, want %v", got, err, want)
	}
}
