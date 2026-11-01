package apitest

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
)

// TestTenantRefSelfExcuseOnlyWhenInserted: a reference to a row's own key is excused
// on the assumption that the row is inserted. An upsert may not insert it, so its key
// proves nothing: under tenant 1, a row whose key and parent are tenant 2's ticket 2
// must not excuse that parent, for itself or for another row of the statement.
func TestTenantRefSelfExcuseOnlyWhenInserted(t *testing.T) {
	t1 := tenant(int64(1))
	two := int64(2)
	parentOf := func(t *testing.T, raw *sql.DB, id int64) (p sql.NullInt64) {
		t.Helper()
		if err := raw.QueryRow("SELECT parent_id FROM tickets WHERE id = ?", id).Scan(&p); err != nil && !errors.Is(err, sql.ErrNoRows) {
			t.Fatal(err)
		}
		return p
	}
	t.Run("an update branch onto the tenant's own row", func(t *testing.T) {
		db, raw := refDB(t)
		err := db.Insert(&testmodel.Ticket{ID: 2, Owner: "ann", Title: "one", InvoiceID: 1, ParentID: &two}).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.ParentID).Exec(t1)
		wantRefused(t, err, "parent_id")
		if p := parentOf(t, raw, 1); p.Valid {
			t.Errorf("ticket 1's parent is now %d", p.Int64)
		}
	})
	t.Run("a row DoNothing keeps out excuses another row", func(t *testing.T) {
		db, raw := refDB(t)
		err := db.InsertAll([]testmodel.Ticket{
			{ID: 2, Owner: "ann", Title: "a", InvoiceID: 1, ParentID: &two},
			{ID: 50, Owner: "ann", Title: "b", InvoiceID: 1, ParentID: &two},
		}).OnConflict(Tk.ID).DoNothing().Exec(t1)
		wantRefused(t, err, "parent_id")
		if n := ticketCount(t, raw); n != 2 {
			t.Errorf("%d tickets", n)
		}
	})
	t.Run("a Rewrite hook's conflict clause counts", func(t *testing.T) {
		_, raw := refDB(t)
		hooked := velox.Open(velox.SQLite, raw, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
			id, _ := st.Entity.Column("id")
			st.Conflict = &velox.ConflictClause{Columns: []int{id}, DoNothing: true}
			return nil
		}))
		wantRefused(t, hooked.Insert(&testmodel.Ticket{ID: 2, Owner: "ann", Title: "a", InvoiceID: 1, ParentID: &two}).Exec(t1), "parent_id")
	})
	t.Run("an upsert's reference to the tenant's row passes, its own key does not", func(t *testing.T) {
		db, raw := refDB(t)
		one := int64(1)
		if err := db.Insert(&testmodel.Ticket{ID: 60, Owner: "ann", Title: "c", InvoiceID: 1, ParentID: &one}).OnConflict(Tk.ID).DoNothing().Exec(t1); err != nil {
			t.Error(err)
		}
		// A new row naming itself: nothing says the row will be there, so it fails closed.
		own := int64(61)
		wantRefused(t, db.Insert(&testmodel.Ticket{ID: own, Owner: "ann", Title: "d", InvoiceID: 1, ParentID: &own}).OnConflict(Tk.ID).DoNothing().Exec(t1), "parent_id")
		if p := parentOf(t, raw, 61); p.Valid {
			t.Error("row 61 was written")
		}
	})
}
