package apitest

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
	"github.com/syssam/velox/untyped"
)

// TestKindMarks: a Calc marked Text, Integer, Time or Bool orders as the
// unmarked Calc, with the same SQL, and AfterValues refuses a cursor's
// value of another kind on each dialect before the statement runs, and
// takes one of its kind. A mark of a column fails the statement.
func TestKindMarks(t *testing.T) {
	type inv = testmodel.Invoice
	calc := func() velox.Term[inv] { return velox.Calc[inv]("? + ?", I.ID, int64(1)) }
	at := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	for _, d := range []velox.Dialect{velox.SQLite, velox.Postgres, velox.MySQL} {
		for _, c := range []struct {
			name      string
			term      velox.Term[inv]
			good, bad any
		}{
			{"Text", velox.Text(calc()), "abc", int64(1)},
			{"Integer", velox.Integer(calc()), int64(3), 2.5},
			{"Time", velox.Time(calc()), at, int64(1)},
			{"Bool", velox.Bool(calc()), true, "true"},
		} {
			e := &fakeconn.Exec{}
			db := openFake(d, e)
			q := db.Query[inv]().OrderBy(c.term.Asc(), I.ID.Asc()).Limit(2)
			got, _, err := q.SQL(ctx)
			want, _, _ := db.Query[inv]().OrderBy(calc().Asc(), I.ID.Asc()).Limit(2).SQL(ctx)
			if err != nil || got != want {
				t.Errorf("%v %s: %q, %v; want %q", d, c.name, got, err, want)
			}
			uq := untyped.FromQuery(q).OrderValues()
			row := &inv{ID: 1}
			if _, err := uq.AfterValues(row, []any{c.bad}).All(ctx); !errors.Is(err, untyped.ErrOrderValue) {
				t.Errorf("%v %s %#v: %v, want ErrOrderValue", d, c.name, c.bad, err)
			}
			if _, err := uq.AfterValues(row, []any{c.good}).All(ctx); err != nil {
				t.Errorf("%v %s %#v: %v", d, c.name, c.good, err)
			}
		}
	}
	column, _ := velox.ColumnByName[inv]("number")
	for _, term := range []velox.Term[inv]{
		velox.Text(column),
		velox.Integer(velox.CountAll[inv]()),
		velox.Time(velox.Text(calc())),
		velox.Bool(calc().Decimal()),
	} {
		_, _, err := openFake(velox.SQLite, &fakeconn.Exec{}).Query[inv]().OrderBy(term.Asc()).Limit(1).SQL(ctx)
		if err == nil || !strings.Contains(err.Error(), "velox: ") {
			t.Errorf("%v: want an error", err)
		}
	}
}
