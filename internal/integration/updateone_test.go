package integration

import (
	"context"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
)

// TestUpdateOneByKey: UpdateQuery.One by key, under the policies, on each
// database. The caller's row is written and returned, and a row the policies
// hide (another owner's, another org's) is ErrNotFound and left as it was.
// A row the update leaves unchanged is found too: MySQL counts it only with
// clientFoundRows=true, which velox's Open requires.
func TestUpdateOneByKey(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		T := testmodel.Tickets
		tg.exec(t, "DELETE FROM tickets")
		for i, r := range []struct {
			org   int64
			owner string
		}{{1, "ann"}, {1, "bob"}, {2, "ann"}} {
			tg.exec(t, "INSERT INTO tickets (created_by, id, org_id, owner, title, done, invoice_id, parent_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
				"seed", int64(i+1), r.org, r.owner, r.owner+"s", false, int64(1), nil)
		}
		ann := testmodel.WithUser(testmodel.Org.With(context.Background(), int64(1)), "ann")
		got, err := tg.pdb.Update[testmodel.Ticket]().Where(T.ID.EQ(1)).Set(T.Title.Set("x")).One(ann)
		if err != nil || got.ID != 1 || got.Title != "x" || got.Owner != "ann" {
			t.Fatalf("ann's ticket: %+v, %v", got, err)
		}
		got, err = tg.pdb.Update[testmodel.Ticket]().Where(T.ID.EQ(1)).Set(T.Title.Set("x")).One(ann)
		if err != nil || got.Title != "x" {
			t.Errorf("an update that changes nothing: %+v, %v", got, err)
		}
		for _, id := range []int64{2, 3, 9} {
			if _, err := tg.pdb.Update[testmodel.Ticket]().Where(T.ID.EQ(id)).Set(T.Title.Set("y")).One(ann); !velox.IsNotFound(err) {
				t.Errorf("ticket %d: %v, want ErrNotFound", id, err)
			}
		}
		rows, err := tg.db.Query[testmodel.Ticket]().Where(T.Title.EQ("y")).AllowUnbounded().All(ctx)
		if err != nil || len(rows) != 0 {
			t.Errorf("a hidden ticket was written: %+v, %v", rows, err)
		}
	})
}
