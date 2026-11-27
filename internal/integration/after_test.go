package integration

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
	"github.com/syssam/velox/untyped"
)

// TestAfterNullsExact: an order by a nullable column, with many NULLs and ties
// among the values, sorts NULL after every value ascending and before them
// descending on every dialect, and After pages it exactly, in each direction of
// the column and of the key and at each page size: every row once, in the
// order. A page from the last row backward is the reversed order's page, which
// is how a connection pages backward.
func TestAfterNullsExact(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		t1, t2 := t0.Add(time.Hour), t0.Add(2*time.Hour)
		at := []*time.Time{nil, &t1, nil, &t2, &t1, nil, nil, &t2, &t1, nil, &t1, nil, &t2}
		rows := make([]testmodel.Invoice, len(at))
		for i, p := range at {
			rows[i] = newInvoice(7, fmt.Sprintf("N-%02d", i))
			rows[i].ID, rows[i].PaidAt = int64(100+i), p
		}
		if err := tg.db.InsertAll(rows).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		for _, o := range []struct {
			paidDesc, idDesc bool
		}{{false, false}, {false, true}, {true, false}, {true, true}} {
			name := fmt.Sprintf("paid desc %v, id desc %v", o.paidDesc, o.idDesc)
			want := slices.Clone(rows)
			slices.SortFunc(want, func(a, b testmodel.Invoice) int {
				c := nullsLast(a.PaidAt, b.PaidAt)
				if o.paidDesc {
					c = -c
				}
				if c == 0 {
					c = int(a.ID - b.ID)
					if o.idDesc {
						c = -c
					}
				}
				return c
			})
			orders := []velox.Order[testmodel.Invoice]{I.PaidAt.Asc(), I.ID.Asc()}
			if o.paidDesc {
				orders[0] = I.PaidAt.Desc()
			}
			if o.idDesc {
				orders[1] = I.ID.Desc()
			}
			q := tg.db.Query[testmodel.Invoice]().Where(I.OrgID.EQ(7)).OrderBy(orders...)
			all, err := q.AllowUnbounded().All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			wantIDs(t, ids(all), ids(want))
			for n := 1; n <= 4; n++ {
				var got []*testmodel.Invoice
				page, err := q.Limit(n).All(ctx)
				for ; err == nil && len(page) > 0; page, err = q.After(page[len(page)-1]).Limit(n).All(ctx) {
					got = append(got, page...)
					if len(got) > len(rows) {
						break
					}
				}
				if err != nil {
					t.Fatalf("%s, %d a page: %v", name, n, err)
				}
				if !slices.Equal(ids(got), ids(want)) {
					t.Errorf("%s, %d a page:\n got %v\nwant %v", name, n, ids(got), ids(want))
				}
				if c, err := q.After(&want[3]).Count(ctx); err != nil || c != len(want)-4 {
					t.Errorf("%s: Count after the fourth: %d, %v, want %d", name, c, err, len(want)-4)
				}
			}
		}
	})
}

// nullsLast compares a and b with NULL after every time.
func nullsLast(a, b *time.Time) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return 1
	case b == nil:
		return -1
	}
	return a.Compare(*b)
}

// TestBeforeNullsExact: Before is After on the reversed order, so After(a)
// with Before(b) is the rows strictly between a and b, and Before(b) alone
// the rows ahead of b, on every dialect, in each direction of a nullable
// column and of the key, among many NULLs and ties.
func TestBeforeNullsExact(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		t1, t2 := t0.Add(time.Hour), t0.Add(2*time.Hour)
		at := []*time.Time{nil, &t1, nil, &t2, &t1, nil, &t2, &t1, nil}
		rows := make([]testmodel.Invoice, len(at))
		for i, p := range at {
			rows[i] = newInvoice(7, fmt.Sprintf("B-%02d", i))
			rows[i].ID, rows[i].PaidAt = int64(100+i), p
		}
		if err := tg.db.InsertAll(rows).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		for _, o := range []struct{ paidDesc, idDesc bool }{{false, false}, {false, true}, {true, false}, {true, true}} {
			name := fmt.Sprintf("paid desc %v, id desc %v", o.paidDesc, o.idDesc)
			orders := []velox.Order[testmodel.Invoice]{I.PaidAt.Asc(), I.ID.Asc()}
			if o.paidDesc {
				orders[0] = I.PaidAt.Desc()
			}
			if o.idDesc {
				orders[1] = I.ID.Desc()
			}
			q := tg.db.Query[testmodel.Invoice]().Where(I.OrgID.EQ(7)).OrderBy(orders...)
			want, err := q.AllowUnbounded().All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			read := func(u untyped.TableQuery) []int64 {
				t.Helper()
				rs, err := u.AllowUnbounded().All(ctx)
				if err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				out := make([]int64, rs.Len())
				for i := range out {
					out[i] = rs.At(i).(*testmodel.Invoice).ID
				}
				return out
			}
			u := untyped.FromQuery(q)
			for j := range want {
				if got := read(u.Before(want[j], nil)); !slices.Equal(got, ids(want[:j])) {
					t.Errorf("%s: Before %d: %v, want %v", name, want[j].ID, got, ids(want[:j]))
				}
				for i := range want[:j] {
					if got := read(u.After(want[i]).Before(want[j], nil)); !slices.Equal(got, ids(want[i+1:j])) {
						t.Errorf("%s: After %d, Before %d: %v, want %v", name, want[i].ID, want[j].ID, got, ids(want[i+1:j]))
					}
				}
			}
		}
	})
}
