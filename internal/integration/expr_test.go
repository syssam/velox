package integration

import (
	"context"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
)

// TestExpr runs Expr on every dialect: a column against a column and a
// value, string literals, a LIKE with an escape, and inside Or and Not.
func TestExpr(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		e := func(text string, args ...any) velox.Pred[testmodel.Invoice] {
			return velox.Expr[testmodel.Invoice](text, args...)
		}
		// Totals are 10 to 50, ids 1 to 5. nextapp's paid-share filter has
		// this shape: a condition on columns, with a value bound.
		wantIDs(t, query(t, tg, e("? > 0 AND ? * ? >= ?", I.Total, I.Total, 2, 40)), []int64{2, 3, 4, 5})
		// Odd ids have no note.
		wantIDs(t, query(t, tg, e("COALESCE(NULLIF(?, ''), 'none') = ?", I.Note, "none")), []int64{1, 3, 5})
		wantIDs(t, query(t, tg, e("COALESCE(NULLIF(?, ''), 'none') = 'a note'", I.Note)), []int64{2, 4})
		wantIDs(t, query(t, tg, e("REPLACE(LOWER(?), '_', '') = ?", I.Number, "50%off")), []int64{3})
		wantIDs(t, query(t, tg, e("? LIKE ? ESCAPE '!'", I.Number, "50!%%")), []int64{3})
		wantIDs(t, query(t, tg, velox.Or(e("? = ?", I.Total, 10), velox.Not(e("? < ?", I.Total, 50)))), []int64{1, 5})
		// With another condition and an upper bound on the rows.
		wantIDs(t, query(t, tg, I.Paid.EQ(true), e("? >= ?", I.Total, 30)), []int64{3, 5})
		n, err := tg.db.Query[testmodel.Invoice]().Where(e("? > ?", I.Total, 15)).Count(ctx)
		if err != nil || n != 4 {
			t.Errorf("count: %d, %v", n, err)
		}
		// In an UPDATE and a DELETE, unqualified by a table.
		upd, err := tg.db.Update[testmodel.Invoice]().Set(I.Note.Set("big")).Where(e("? >= ?", I.Total, 40)).Exec(ctx)
		if err != nil || upd != 2 {
			t.Errorf("update: %d, %v", upd, err)
		}
		del, err := tg.db.Delete[testmodel.Invoice]().Where(e("LENGTH(?) = 4", I.Number)).Exec(ctx)
		if err != nil || del != 1 { // 500X
			t.Errorf("delete: %d, %v", del, err)
		}
	})
}

// TestExprKeepsPolicy: on a policied DB an Expr that matches both orgs'
// tickets still returns only the caller's, and only their own.
func TestExprKeepsPolicy(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		title := velox.Expr[testmodel.Ticket]("LOWER(?) = LOWER(?)", Tk.Title, "a1")
		for _, tc := range []struct {
			name string
			ctx  context.Context
			want []int64
		}{
			// Tickets titled A1: 1 (org 1, ann) and 6 (org 2, ann).
			{"ann in org 1", testmodel.WithUser(testmodel.Org.With(context.Background(), int64(1)), "ann"), []int64{1}},
			{"ann in org 2", testmodel.WithUser(testmodel.Org.With(context.Background(), int64(2)), "ann"), []int64{6}},
			{"bob in org 1", testmodel.WithUser(testmodel.Org.With(context.Background(), int64(1)), "bob"), nil},
		} {
			got, err := tg.pdb.Query[testmodel.Ticket]().Where(title).OrderBy(Tk.ID.Asc()).Limit(10).All(tc.ctx)
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			var ids []int64
			for _, tk := range got {
				ids = append(ids, tk.ID)
			}
			if len(ids) != len(tc.want) || (len(ids) > 0 && ids[0] != tc.want[0]) {
				t.Errorf("%s: tickets %v, want %v", tc.name, ids, tc.want)
			}
		}
	})
}
