package velox_test

import (
	"context"
	"errors"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

func TestBeforeUpdateRefuses(t *testing.T) {
	e := &velox.FakeExec{Affected: 1}
	db := velox.OpenFake(velox.Postgres, e)
	const text = "velox: testmodel.Ticket: before update: a ticket needs a title"
	_, err := db.Update[testmodel.Ticket]().Set(Tk.Title.Set("")).Where(Tk.ID.EQ(1)).Exec(asUser("admin"))
	if err == nil || err.Error() != text {
		t.Fatalf("got %v, want %q", err, text)
	}
	// Save runs the hook too, and leaves *x as it was.
	tk := testmodel.Ticket{ID: 1, OrgID: 1, Owner: "ann"}
	if err := db.Save(asUser("admin"), &tk, Tk.Title); err == nil || err.Error() != text {
		t.Errorf("Save: %v", err)
	}
	if e.Calls != 0 {
		t.Errorf("%d statements sent", e.Calls)
	}
	if _, err := db.Update[testmodel.Ticket]().Set(Tk.Title.Set("t")).Where(Tk.ID.EQ(1)).Exec(asUser("admin")); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, `UPDATE "tickets" SET "title" = $1 WHERE "id" = $2 AND "org_id" = $3`, "t", int64(1), int64(1))
}

func TestBeforeDeleteAddsItsCondition(t *testing.T) {
	e := &velox.FakeExec{Affected: 1}
	db := velox.OpenFake(velox.Postgres, e)
	d := db.Delete[testmodel.Ticket]().Where(Tk.ID.EQ(1))
	for range 2 { // the hook changes the statement, not the query
		if _, err := d.Exec(asUser("admin")); err != nil {
			t.Fatal(err)
		}
		wantSent(t, e, `DELETE FROM "tickets" WHERE "id" = $1 AND "done" = $2 AND "org_id" = $3`, int64(1), true, int64(1))
	}
	// SQL(ctx) runs the hook as well, and shows its condition once.
	for range 2 {
		if sql, _, err := d.SQL(asUser("admin")); err != nil || sql != e.SQL {
			t.Errorf("SQL(ctx) = %s %v", sql, err)
		}
	}
}

// TestEntityHooksRunBeforeRewriteAndScope checks the order: guard, entity
// hooks, Rewrite hooks, scope.
func TestEntityHooksRunBeforeRewriteAndScope(t *testing.T) {
	e := &velox.FakeExec{Affected: 1}
	var seen []int
	rw := velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		seen = append(seen, len(st.Where))
		return nil
	})
	db := velox.OpenFake(velox.Postgres, e, rw)
	if _, err := db.Delete[testmodel.Ticket]().Where(Tk.ID.EQ(1)).Exec(asUser("admin")); err != nil {
		t.Fatal(err)
	}
	// The Rewrite hook sees the caller's condition and the BeforeDelete's,
	// and not yet the tenant's, which the scope adds after it.
	if len(seen) != 1 || seen[0] != 2 {
		t.Errorf("the Rewrite hook saw %v conditions, want [2]", seen)
	}
	wantSent(t, e, `DELETE FROM "tickets" WHERE "id" = $1 AND "done" = $2 AND "org_id" = $3`, int64(1), true, int64(1))
	// A refusing entity hook stops the statement before the Rewrite hooks.
	_, err := db.Update[testmodel.Ticket]().Set(Tk.Title.Set("")).Where(Tk.ID.EQ(1)).Exec(asUser("admin"))
	if err == nil || len(seen) != 1 {
		t.Errorf("got %v, Rewrite calls %d", err, len(seen))
	}
}

// TestEntityHooksAfterTheGuard: the guard runs before the hooks, so an
// update with no Where is refused before a hook can narrow it.
func TestEntityHooksAfterTheGuard(t *testing.T) {
	e := &velox.FakeExec{Affected: 1}
	db := velox.OpenFake(velox.Postgres, e)
	if _, err := db.Delete[testmodel.Ticket]().Exec(asUser("admin")); !errors.Is(err, velox.ErrNoWhere) {
		t.Errorf("got %v, want ErrNoWhere", err)
	}
}
