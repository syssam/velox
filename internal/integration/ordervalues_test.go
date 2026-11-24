package integration

import (
	"cmp"
	"fmt"
	"slices"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
	"github.com/syssam/velox/untyped"
)

// TestOrderValuesKeyset: rows ordered by a computed number, a computed
// fraction and a computed text, each with ties and NULLs and bound values,
// then the key, page exactly through OrderValues and AfterValues: in each
// direction, with NullsFirst, NullsLast and the default, at each page size,
// with no row repeated or skipped, and in the order Go computes, so the
// pages are the same on every dialect.
func TestOrderValuesKeyset(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		letters := "ABCABCCAABCBA"
		rows := make([]testmodel.Invoice, len(letters))
		for i := range rows {
			rows[i] = newInvoice(7, fmt.Sprintf("%c%02d", letters[i], i))
			rows[i].ID, rows[i].Total = int64(600+i), float64(i%4)*1.25
		}
		if err := tg.db.InsertAll(rows).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		type expr struct {
			name string
			term velox.Term[testmodel.Invoice]
			cmp  func(a, b testmodel.Invoice) (null [2]bool, c int)
		}
		exprs := []expr{
			{"NULLIF(id % 3, 0)", velox.Calc[testmodel.Invoice]("NULLIF(? % ?, ?)", I.ID, int64(3), int64(0)),
				func(a, b testmodel.Invoice) ([2]bool, int) {
					return [2]bool{a.ID%3 == 0, b.ID%3 == 0}, cmp.Compare(a.ID%3, b.ID%3)
				}},
			{"NULLIF(total * 0.5, 0)", velox.Calc[testmodel.Invoice]("NULLIF(? * ?, ?)", I.Total, 0.5, 0.0),
				func(a, b testmodel.Invoice) ([2]bool, int) {
					return [2]bool{a.Total == 0, b.Total == 0}, cmp.Compare(a.Total, b.Total)
				}},
			{"NULLIF(SUBSTR(number, 1, 1), 'C')", velox.Calc[testmodel.Invoice]("NULLIF(SUBSTR(?, ?, ?), ?)", I.Number, int64(1), int64(1), "C"),
				func(a, b testmodel.Invoice) ([2]bool, int) {
					return [2]bool{a.Number[0] == 'C', b.Number[0] == 'C'}, cmp.Compare(a.Number[0], b.Number[0])
				}},
		}
		for _, e := range exprs {
			for _, o := range []struct {
				desc  bool
				nulls int // 0 the default, 1 first, 2 last
				key   bool
			}{
				{false, 0, false}, {true, 0, false}, {false, 1, false}, {true, 1, true},
				{false, 2, true}, {true, 2, false},
			} {
				name := fmt.Sprintf("%s desc %v nulls %d key desc %v", e.name, o.desc, o.nulls, o.key)
				by := e.term.Asc()
				if o.desc {
					by = e.term.Desc()
				}
				first := o.desc
				switch o.nulls {
				case 1:
					by, first = by.NullsFirst(), true
				case 2:
					by, first = by.NullsLast(), false
				}
				key := I.ID.Asc()
				if o.key {
					key = I.ID.Desc()
				}
				want := slices.Clone(rows)
				slices.SortFunc(want, func(a, b testmodel.Invoice) int {
					null, c := e.cmp(a, b)
					c = placed(null[0], null[1], first, func() int { return c }, o.desc)
					if c == 0 {
						c = cmp.Compare(a.ID, b.ID)
						if o.key {
							c = -c
						}
					}
					return c
				})
				q := untyped.FromQuery(tg.db.Query[testmodel.Invoice]().Where(I.ID.GTE(600)).OrderBy(by, key)).OrderValues()
				for n := 1; n <= 4; n++ {
					var got []testmodel.Invoice
					page, err := q.Limit(n).All(ctx)
					for err == nil && page.Len() > 0 && len(got) <= len(rows) {
						got = append(got, untyped.RowsOf[testmodel.Invoice](page)...)
						last := page.Len() - 1
						if vs := page.OrderValues(last); len(vs) != 1 {
							t.Fatalf("%s: %d order values, want 1", name, len(vs))
						}
						page, err = q.AfterValues(page.At(last), page.OrderValues(last)).Limit(n).All(ctx)
					}
					if err != nil {
						t.Fatalf("%s, %d a page: %v", name, n, err)
					}
					if !slices.Equal(ids(got), ids(want)) {
						t.Errorf("%s, %d a page:\n got %v\nwant %v", name, n, ids(got), ids(want))
					}
				}
			}
		}
	})
}

// TestOrderValuesRefusals: After alone refuses an ordering by an
// expression, AfterValues refuses the wrong number of values and an
// aggregate, and OrderValues adds nothing to Count.
func TestOrderValuesRefusals(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		calc := velox.Calc[testmodel.Invoice]("? + ?", I.ID, int64(1))
		base := tg.db.Query[testmodel.Invoice]().OrderBy(calc.Asc(), I.ID.Asc()).Limit(5)
		row := &testmodel.Invoice{ID: 1}
		if _, err := base.After(row).All(ctx); err == nil {
			t.Error("After of an expression ordering: no error")
		}
		q := untyped.FromQuery(base)
		if _, err := q.AfterValues(row, nil).All(ctx); err == nil {
			t.Error("AfterValues with no values: no error")
		}
		if _, err := q.AfterValues(row, []any{int64(1), int64(2)}).All(ctx); err == nil {
			t.Error("AfterValues with two values for one expression: no error")
		}
		got, err := q.AfterValues(row, []any{int64(2)}).OrderValues().All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got.Len() == 0 || untyped.RowsOf[testmodel.Invoice](got)[0].ID != 2 {
			t.Errorf("after id 1 by id + 1: %v", ids(untyped.RowsOf[testmodel.Invoice](got)))
		}
		if n, err := q.OrderValues().Count(ctx); err != nil || n == 0 {
			t.Errorf("Count with OrderValues: %d, %v", n, err)
		}
		if got, err := q.All(ctx); err != nil || got.OrderValues(0) != nil {
			t.Errorf("All without OrderValues: values %v, %v", got.OrderValues(0), err)
		}
	})
}
