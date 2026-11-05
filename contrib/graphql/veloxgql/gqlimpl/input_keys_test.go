package gqlimpl_test

import (
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlimpl"
)

// TestEdgeKeyOfAnotherType: an edge helper given a key its column cannot
// take, which the generated code never passes, fails the write with
// velox's error before any statement runs.
func TestEdgeKeyOfAnotherType(t *testing.T) {
	f := newInputFixture(t)
	f.log.take()
	bad := []string{"x"} // keys of int64 columns
	update := func(name string, add func(u *gqlimpl.Update, key int64)) {
		t.Helper()
		u, key := gqlimpl.NewUpdate[int64](f.ctx, gqlimpl.InputConn[acct]("Acct", int(accts.ID.I), acctInputGQL), acctGQL(1))
		add(u, key)
		if _, err := u.Exec(key, nil); err == nil {
			t.Errorf("%s: no error", name)
		}
		if got := f.log.take(); len(got) != 0 {
			t.Errorf("%s: ran %q", name, got)
		}
	}
	update("Children, an owner key of another type", func(u *gqlimpl.Update, _ int64) {
		gqlimpl.Children(u, "addKidIDs", "Kid", gqlimpl.Edit{Clear: true}, "one", acctKidsEdgeGQL())
	})
	update("Child, an owner key of another type", func(u *gqlimpl.Update, _ int64) {
		gqlimpl.Child(u, "petID", "Kid", true, nil, "one", acctKidsEdgeGQL())
	})
	update("Child, a child key of another type", func(u *gqlimpl.Update, key int64) {
		gqlimpl.Child(u, "petID", "Kid", false, new("x"), key, acctKidsEdgeGQL())
	})
	update("Through, an owner key of another type", func(u *gqlimpl.Update, _ int64) {
		gqlimpl.Through(u, "addLblIDs", "Lbl", gqlimpl.Edit{Clear: true}, "one", acctLblsEdgeGQL())
	})
	update("Exists, keys of another type", func(u *gqlimpl.Update, _ int64) {
		gqlimpl.Exists(u, "acctID", "Acct", velox.TableOf[acct](), int(accts.ID.I), bad)
	})

	x := &acct{Name: "n"}
	c := gqlimpl.NewCreate(f.ctx, gqlimpl.InputConn[acct]("Acct", 0, acctInputGQL), x, int(accts.Name.I))
	gqlimpl.Repoint(c, "kidIDs", "Kid", acctKidsEdgeGQL(), bad)
	if err := c.Insert(); err == nil {
		t.Error("Repoint, keys of another type: no error")
	}
	if got := f.log.take(); len(got) != 0 {
		t.Errorf("Repoint: ran %q", got)
	}
}

// TestUpdateSetNull: SetNull clears a nullable field.
func TestUpdateSetNull(t *testing.T) {
	f := newInputFixture(t)
	u, key := gqlimpl.NewUpdate[int64](f.ctx, gqlimpl.InputConn[kid]("Kid", int(kids.ID.I), kidInputGQL), veloxgql.EncodeID("Kid", int64(1)))
	u.SetNull(int(kids.AcctID.I))
	p, err := u.Exec(key, nil)
	if err != nil {
		t.Fatal(err)
	}
	if k := p.(*kid); k.AcctID != nil {
		t.Errorf("acct_id = %d, want NULL", *k.AcctID)
	}
	var n int
	if err := f.raw.QueryRow(`SELECT count(*) FROM kids WHERE id = 1 AND acct_id IS NULL`).Scan(&n); err != nil || n != 1 {
		t.Errorf("the row: %d %v", n, err)
	}
}
