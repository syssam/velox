package integration

import (
	"slices"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
)

// TestExprRaw runs ADR 0155's cases on every dialect: a text column IN a
// subquery of an integer key made text, and a correlated NOT EXISTS on a
// table no entity maps, on the outer row's columns, also under a self-join,
// inside a relation's EXISTS and in an UPDATE and a DELETE.
func TestExprRaw(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		// An integer made text: MySQL's CAST(x AS CHAR) has the
		// connection's collation and may not compare with the column's
		// (error 1267), so it is CONCAT there, as velox's own TextKey is.
		text := func(x string) string { return "CAST(" + x + " AS TEXT)" }
		if tg.d == velox.MySQL {
			text = func(x string) string { return "CONCAT(" + x + ")" }
		}
		for _, q := range []string{
			"DROP TABLE IF EXISTS xr_bill",
			"DROP TABLE IF EXISTS xr_audit",
			"CREATE TABLE xr_bill (bill_id BIGINT NOT NULL, org_id BIGINT NOT NULL)",
			"CREATE TABLE xr_audit (org_id BIGINT NOT NULL, entity_id VARCHAR(64) NOT NULL)",
		} {
			tg.exec(t, q)
		}
		tg.exec(t, "UPDATE invoices SET note = ? WHERE id = ?", "7", 2)
		tg.exec(t, "UPDATE invoices SET note = ? WHERE id = ?", "8", 4)
		for _, r := range [][2]int64{{7, 1}, {8, 2}, {9, 1}} {
			tg.exec(t, "INSERT INTO xr_bill (bill_id, org_id) VALUES (?, ?)", r[0], r[1])
		}
		for _, r := range []struct {
			org    int64
			entity string
		}{{1, "1"}, {1, "3"}, {1, "4"}, {2, "5"}, {2, "2"}} {
			tg.exec(t, "INSERT INTO xr_audit (org_id, entity_id) VALUES (?, ?)", r.org, r.entity)
		}

		// (1) note, a text column, IN the bills of some orgs, an integer key.
		inBills := "? IN (SELECT " + text("b.bill_id") + " FROM xr_bill b WHERE b.org_id IN (?, ?))"
		bills := func(a, b int64) velox.Pred[testmodel.Invoice] {
			return velox.ExprRaw[testmodel.Invoice]("bills are text-keyed", inBills, I.Note, a, b)
		}
		wantIDs(t, query(t, tg, bills(1, 3)), []int64{2})
		wantIDs(t, query(t, tg, bills(1, 2)), []int64{2, 4})

		// (3) no audit row names the invoice in its org; invoice 2's
		// audit row is another org's.
		unaudited := "NOT EXISTS (SELECT 1 FROM xr_audit m WHERE m.org_id = ? AND m.entity_id = " + text("?") + ")"
		invFree := velox.ExprRaw[testmodel.Invoice]("audit trail", unaudited, I.OrgID, I.ID)
		wantIDs(t, query(t, tg, invFree), []int64{2})
		wantIDs(t, query(t, tg, velox.Or(I.ID.EQ(5), velox.Not(velox.Not(invFree)))), []int64{2, 5})

		// The same on tickets under a self-join: the main table keeps its
		// name, the join an alias. Tickets with a parent: 2, 4, 5, 7, 8;
		// the audit rows name 4 in org 1.
		tkFree := velox.ExprRaw[testmodel.Ticket]("audit trail", unaudited, Tk.OrgID, Tk.ID)
		joined, err := tg.db.Query[testmodel.Ticket]().Join(Tk.Parent).Where(tkFree).OrderBy(Tk.ID.Asc()).AllowUnbounded().All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got := ticketIDs(joined); !slices.Equal(got, []int64{2, 5, 7, 8}) {
			t.Errorf("under a join: %v", got)
		}
		// Inside a relation's EXISTS: tickets whose invoice is unaudited.
		nested, err := tg.db.Query[testmodel.Ticket]().Where(Tk.Invoice.HasWith(invFree)).OrderBy(Tk.ID.Asc()).AllowUnbounded().All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got := ticketIDs(nested); !slices.Equal(got, []int64{3}) {
			t.Errorf("inside HasWith: %v", got)
		}

		upd, err := tg.db.Update[testmodel.Invoice]().Set(I.Note.Set("seen")).Where(invFree).Exec(ctx)
		if err != nil || upd != 1 {
			t.Errorf("update: %d, %v", upd, err)
		}
		del, err := tg.db.Delete[testmodel.Invoice]().Where(invFree).Exec(ctx)
		if err != nil || del != 1 {
			t.Errorf("delete: %d, %v", del, err)
		}
		wantIDs(t, query(t, tg), []int64{1, 3, 4, 5})
	})
}
