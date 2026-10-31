package apitest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
)

// The bidirectional many-to-many model: bnode's friends through bnodeFriend,
// a self-referencing edge with no inverse, written by hand as velox-gen
// writes a Desc. bnodeFriendP is the same join entity with an Insert policy
// and a hook.

type bnode struct {
	ID      int64
	Friends velox.Many[bnode]
}

var bnodeMeta velox.MetaOnce[bnode]

func (*bnode) VeloxMeta() *velox.Meta[bnode] {
	return bnodeMeta.Load(func() velox.Desc[bnode] {
		return velox.Desc[bnode]{
			Table:      "bnodes",
			Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}},
			PrimaryKey: []int{0},
			Tenant:     -1,
			Edges:      []velox.EdgeDesc{velox.EdgeThrough[bnodeFriend, bnode]("Friends", 0, 0, 1, 0)},
			Addr:       func(x *bnode, i int) any { return &x.ID },
			EdgeAddr:   func(x *bnode, i int) any { return &x.Friends },
		}
	})
}

type bnodeFriend struct {
	NodeID   int64
	FriendID int64
}

var bnodeFriends = struct {
	NodeID   velox.NumberCol[bnodeFriend, int64]
	FriendID velox.NumberCol[bnodeFriend, int64]
}{velox.NumberCol[bnodeFriend, int64]{I: 0}, velox.NumberCol[bnodeFriend, int64]{I: 1}}

var bnodeFriendMeta velox.MetaOnce[bnodeFriend]

func (*bnodeFriend) VeloxMeta() *velox.Meta[bnodeFriend] {
	return bnodeFriendMeta.Load(func() velox.Desc[bnodeFriend] {
		return velox.Desc[bnodeFriend]{
			Table:      "bnode_friends",
			Columns:    []velox.ColumnDesc{{Field: "NodeID", Name: "node_id"}, {Field: "FriendID", Name: "friend_id"}},
			PrimaryKey: []int{0, 1},
			Tenant:     -1,
			Bidi:       true,
			Addr: func(x *bnodeFriend, i int) any {
				if i == 0 {
					return &x.NodeID
				}
				return &x.FriendID
			},
		}
	})
}

type bnodeFriendP struct {
	NodeID   int64
	FriendID int64
}

// bidiSeen records what bnodeFriendP's policy and hook saw.
var bidiSeen struct {
	inserted []bnodeFriendP // the rows Policy.Insert was asked about
	hookRows int            // the rows the hook saw
	calls    int            // the hook's calls
	affected int64          // Mutation.Affected after next
}

func (*bnodeFriendP) Policy() velox.Policy[bnodeFriendP] {
	return velox.Policy[bnodeFriendP]{Insert: func(_ context.Context, x *bnodeFriendP) error {
		bidiSeen.inserted = append(bidiSeen.inserted, *x)
		return nil
	}}
}

func (*bnodeFriendP) Hooks() []velox.Hook[bnodeFriendP] {
	return []velox.Hook[bnodeFriendP]{func(next velox.Mutator[bnodeFriendP]) velox.Mutator[bnodeFriendP] {
		return velox.MutateFunc[bnodeFriendP](func(ctx context.Context, m *velox.Mutation[bnodeFriendP]) error {
			bidiSeen.calls++
			bidiSeen.hookRows = len(m.Rows())
			err := next.Mutate(ctx, m)
			bidiSeen.affected = m.Affected()
			return err
		})
	}}
}

var bnodeFriendPMeta velox.MetaOnce[bnodeFriendP]

func (*bnodeFriendP) VeloxMeta() *velox.Meta[bnodeFriendP] {
	return bnodeFriendPMeta.Load(func() velox.Desc[bnodeFriendP] {
		return velox.Desc[bnodeFriendP]{
			Policy:     func() velox.BoundPolicy { return velox.BindPolicy((*bnodeFriendP)(nil).Policy()) },
			Hooks:      func() velox.BoundHooks { return velox.BindHooks((*bnodeFriendP)(nil).Hooks()) },
			Table:      "bnode_friends",
			Columns:    []velox.ColumnDesc{{Field: "NodeID", Name: "node_id"}, {Field: "FriendID", Name: "friend_id"}},
			PrimaryKey: []int{0, 1},
			Tenant:     -1,
			Bidi:       true,
			Addr: func(x *bnodeFriendP, i int) any {
				if i == 0 {
					return &x.NodeID
				}
				return &x.FriendID
			},
		}
	})
}

// bkey is a named byte-slice key: == on it would panic.
type bkey []byte

type bnodeKeyed struct{ A, B bkey }

var bnodeKeyedMeta velox.MetaOnce[bnodeKeyed]

func (*bnodeKeyed) VeloxMeta() *velox.Meta[bnodeKeyed] {
	return bnodeKeyedMeta.Load(func() velox.Desc[bnodeKeyed] {
		return velox.Desc[bnodeKeyed]{
			Table:      "keyed_links",
			Columns:    []velox.ColumnDesc{{Field: "A", Name: "a"}, {Field: "B", Name: "b"}},
			PrimaryKey: []int{0, 1},
			Tenant:     -1,
			Bidi:       true,
			Addr: func(x *bnodeKeyed, i int) any {
				if i == 0 {
					return &x.A
				}
				return &x.B
			},
		}
	})
}

// TestBidiLinks: a link of a bidirectional edge is two join rows, written and
// removed together; a self-link is one row; an update is refused.
func TestBidiLinks(t *testing.T) {
	F := bnodeFriends
	sent := func(t *testing.T, e *fakeconn.Exec, sql string, args ...any) {
		t.Helper()
		if len(e.Log) != 1 || e.Log[0] != sql {
			t.Errorf("sent %q,\nwant %q", e.Log, sql)
		}
		if !reflect.DeepEqual(e.Args, args) {
			t.Errorf("args %v, want %v", e.Args, args)
		}
	}
	t.Run("insert", func(t *testing.T) {
		e := &fakeconn.Exec{}
		if err := openFake(velox.Postgres, e).Insert(&bnodeFriend{1, 2}).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		sent(t, e, `INSERT INTO "bnode_friends" ("node_id", "friend_id") VALUES ($1, $2), ($3, $4)`, int64(1), int64(2), int64(2), int64(1))
	})
	t.Run("self-link", func(t *testing.T) {
		e := &fakeconn.Exec{}
		if err := openFake(velox.Postgres, e).Insert(&bnodeFriend{3, 3}).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		sent(t, e, `INSERT INTO "bnode_friends" ("node_id", "friend_id") VALUES ($1, $2)`, int64(3), int64(3))
	})
	t.Run("insert all, do nothing", func(t *testing.T) {
		e := &fakeconn.Exec{}
		err := openFake(velox.Postgres, e).InsertAll([]bnodeFriend{{1, 2}, {1, 3}}).OnConflict(F.NodeID, F.FriendID).DoNothing().Exec(ctx)
		if err != nil {
			t.Fatal(err)
		}
		sent(t, e, `INSERT INTO "bnode_friends" ("node_id", "friend_id") VALUES ($1, $2), ($3, $4), ($5, $6), ($7, $8) ON CONFLICT ("node_id", "friend_id") DO NOTHING`,
			int64(1), int64(2), int64(2), int64(1), int64(1), int64(3), int64(3), int64(1))
	})
	t.Run("delete", func(t *testing.T) {
		e := &fakeconn.Exec{}
		if _, err := openFake(velox.Postgres, e).Delete[bnodeFriend]().Where(F.NodeID.EQ(1), F.FriendID.In(2, 3)).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		sent(t, e, `DELETE FROM "bnode_friends" WHERE (("node_id" = $1 AND "friend_id" IN ($2, $3)) OR ("friend_id" = $4 AND "node_id" IN ($5, $6)))`,
			int64(1), int64(2), int64(3), int64(1), int64(2), int64(3))
	})
	t.Run("delete, nested", func(t *testing.T) {
		e := &fakeconn.Exec{}
		w := velox.Or(F.NodeID.EQ(1), velox.Not(F.FriendID.EQ(2)))
		if _, err := openFake(velox.Postgres, e).Delete[bnodeFriend]().Where(w).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		sent(t, e, `DELETE FROM "bnode_friends" WHERE (("node_id" = $1 OR NOT ("friend_id" = $2)) OR ("friend_id" = $3 OR NOT ("node_id" = $4)))`,
			int64(1), int64(2), int64(1), int64(2))
	})
	t.Run("delete all", func(t *testing.T) {
		e := &fakeconn.Exec{}
		if _, err := openFake(velox.Postgres, e).Delete[bnodeFriend]().All().Exec(ctx); err != nil {
			t.Fatal(err)
		}
		sent(t, e, `DELETE FROM "bnode_friends"`)
	})
	t.Run("load", func(t *testing.T) {
		// Both rows are there, so a load reads the owner's column alone.
		e := &fakeconn.Exec{Results: [][][]any{{{int64(1)}, {int64(2)}}, {{int64(1), int64(2)}, {int64(2), int64(1)}}, {{int64(1)}, {int64(2)}}}}
		ns, err := openFake(velox.Postgres, e).Query[bnode]().With(velox.Rel[bnode, bnode]{I: 0}).Limit(2).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for i, want := range []int64{2, 1} {
			if fs, _ := ns[i].Friends.Get(); len(fs) != 1 || fs[0].ID != want {
				t.Errorf("node %d: friends %+v, want %d", ns[i].ID, fs, want)
			}
		}
		if want := `SELECT "node_id", "friend_id" FROM "bnode_friends" WHERE "node_id" IN ($1, $2) ORDER BY "node_id", "friend_id"`; e.Log[1] != want {
			t.Errorf("join SQL %s", e.Log[1])
		}
	})
	t.Run("updates refused", func(t *testing.T) {
		const want = "velox: apitest.bnodeFriend: the join entity of a bidirectional edge cannot be updated; delete the link and insert it again"
		e := &fakeconn.Exec{}
		db := openFake(velox.Postgres, e)
		_, errUpdate := db.Update[bnodeFriend]().Set(F.FriendID.Set(5)).All().Exec(ctx)
		errSave := db.Save(&bnodeFriend{1, 2}, F.FriendID).Exec(ctx)
		errUpsert := db.Insert(&bnodeFriend{1, 2}).OnConflict(F.NodeID, F.FriendID).Update(F.FriendID).Exec(ctx)
		for name, err := range map[string]error{"update": errUpdate, "save": errSave, "upsert": errUpsert} {
			if err == nil || err.Error() != want {
				t.Errorf("%s: %v, want %s", name, err, want)
			}
		}
		if len(e.Log) != 0 {
			t.Errorf("sent %q", e.Log)
		}
	})
	t.Run("named byte-slice keys", func(t *testing.T) {
		e := &fakeconn.Exec{}
		db := openFake(velox.Postgres, e)
		if err := db.InsertAll([]bnodeKeyed{{bkey("x"), bkey("x")}, {bkey("x"), bkey("y")}}).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		sent(t, e, `INSERT INTO "keyed_links" ("a", "b") VALUES ($1, $2), ($3, $4), ($5, $6)`,
			bkey("x"), bkey("x"), bkey("x"), bkey("y"), bkey("y"), bkey("x"))
	})
	t.Run("a refused update runs no hook", func(t *testing.T) {
		bidiSeen.calls = 0
		e := &fakeconn.Exec{}
		db := openFake(velox.Postgres, e)
		_, errUpdate := db.Update[bnodeFriendP]().Set(velox.NumberCol[bnodeFriendP, int64]{I: 1}.Set(5)).All().Exec(ctx)
		errSave := db.Save(&bnodeFriendP{1, 2}, velox.NumberCol[bnodeFriendP, int64]{I: 1}).Exec(ctx)
		if errUpdate == nil || errSave == nil || bidiSeen.calls != 0 || len(e.Log) != 0 {
			t.Errorf("update %v, save %v, %d hook calls, sent %q", errUpdate, errSave, bidiSeen.calls, e.Log)
		}
	})
	t.Run("policy and hooks see the caller's rows", func(t *testing.T) {
		bidiSeen.inserted, bidiSeen.hookRows, bidiSeen.affected = nil, 0, 0
		e := &fakeconn.Exec{Affected: 2}
		if err := openFake(velox.Postgres, e).Insert(&bnodeFriendP{1, 2}).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if want := []bnodeFriendP{{1, 2}}; !reflect.DeepEqual(bidiSeen.inserted, want) {
			t.Errorf("Policy.Insert saw %v, want %v", bidiSeen.inserted, want)
		}
		if bidiSeen.hookRows != 1 {
			t.Errorf("the hook saw %d rows, want 1", bidiSeen.hookRows)
		}
		if bidiSeen.affected != 2 {
			t.Errorf("Mutation.Affected %d, want 2", bidiSeen.affected)
		}
		if len(e.Log) != 1 || !strings.HasSuffix(e.Log[0], "VALUES ($1, $2), ($3, $4)") {
			t.Errorf("sent %q, want both rows", e.Log)
		}
	})
}

// The bidirectional one-to-one model: bspouse's Spouse, a self-referencing
// unique edge with no inverse, keyed by its own SpouseID, written by hand as
// velox-gen writes a Desc. Its hook records Mutation.Affected.

type bspouse struct {
	ID       int64
	SpouseID *int64
	Spouse   velox.One[bspouse]
}

var bspouses = struct {
	ID       velox.NumberCol[bspouse, int64]
	SpouseID velox.NullNumberCol[bspouse, int64]
}{velox.NumberCol[bspouse, int64]{I: 0}, velox.NullNumberCol[bspouse, int64]{I: 1}}

// bspouseAffected is what bspouse's hook read from Mutation.Affected.
var bspouseAffected int64

func (*bspouse) Hooks() []velox.Hook[bspouse] {
	return []velox.Hook[bspouse]{func(next velox.Mutator[bspouse]) velox.Mutator[bspouse] {
		return velox.MutateFunc[bspouse](func(ctx context.Context, m *velox.Mutation[bspouse]) error {
			err := next.Mutate(ctx, m)
			bspouseAffected = m.Affected()
			return err
		})
	}}
}

var bspouseMeta velox.MetaOnce[bspouse]

func (*bspouse) VeloxMeta() *velox.Meta[bspouse] {
	return bspouseMeta.Load(func() velox.Desc[bspouse] {
		return velox.Desc[bspouse]{
			Hooks:      func() velox.BoundHooks { return velox.BindHooks((*bspouse)(nil).Hooks()) },
			Table:      "bspouses",
			Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "SpouseID", Name: "spouse_id"}},
			PrimaryKey: []int{0},
			Tenant:     -1,
			Indexes:    []velox.IndexInfo{{Columns: []int{1}, Unique: true}},
			Edges:      []velox.EdgeDesc{velox.EdgeBidi[bspouse]("Spouse", 1, 0)},
			Addr: func(x *bspouse, i int) any {
				if i == 0 {
					return &x.ID
				}
				return &x.SpouseID
			},
			EdgeAddr: func(x *bspouse, i int) any { return &x.Spouse },
		}
	})
}

// TestBidiOne: a write that sets the key of a bidirectional one-to-one edge
// re-points both rows in a transaction: the caller's UPDATE, the clearing of
// the row that pointed at it, and the link of the new partner, which a taken
// partner refuses.
func TestBidiOne(t *testing.T) {
	S := bspouses
	const (
		upd    = `UPDATE "bspouses" SET "spouse_id" = $1 WHERE "id" = $2`
		unlink = `UPDATE "bspouses" SET "spouse_id" = $1 WHERE "spouse_id" = $2`
		link   = `UPDATE "bspouses" SET "spouse_id" = $1 WHERE "id" = $2 AND "spouse_id" IS NULL`
		probe  = `SELECT 1 FROM "bspouses" WHERE "id" = $1 LIMIT $2`
		ins    = `INSERT INTO "bspouses" ("id", "spouse_id") VALUES ($1, $2)`
	)
	type stmt struct {
		sql  string
		args []any
	}
	// open returns a DB on a fake whose statements affect one row, but
	// those affected names, which affect its value.
	open := func(affected map[string]int64) (*velox.DB, *fakeconn.Exec) {
		e := &fakeconn.Exec{AffectedBy: func(sql string) int64 {
			if n, ok := affected[sql]; ok {
				return n
			}
			return 1
		}}
		return openFake(velox.Postgres, e), e
	}
	sent := func(t *testing.T, e *fakeconn.Exec, want ...stmt) {
		t.Helper()
		var got []stmt
		for i, s := range e.Log {
			got = append(got, stmt{s, e.ArgLog[i]})
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("sent\n%v,\nwant\n%v", got, want)
		}
	}
	linked := []stmt{
		{"BEGIN", nil},
		{upd, []any{new(int64(3)), int64(1)}},
		{unlink, []any{nil, int64(1)}},
		{link, []any{int64(1), int64(3)}},
		{"COMMIT", nil},
	}
	t.Run("save", func(t *testing.T) {
		bspouseAffected = 0
		db, e := open(nil)
		if err := db.Save(&bspouse{ID: 1, SpouseID: new(int64(3))}, S.SpouseID).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		sent(t, e, linked...)
		if bspouseAffected != 1 {
			t.Errorf("Mutation.Affected %d, want 1", bspouseAffected)
		}
	})
	t.Run("save nil", func(t *testing.T) {
		db, e := open(nil)
		if err := db.Save(&bspouse{ID: 1}, S.SpouseID).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		sent(t, e, stmt{"BEGIN", nil}, stmt{upd, []any{(*int64)(nil), int64(1)}}, stmt{unlink, []any{nil, int64(1)}}, stmt{"COMMIT", nil})
	})
	t.Run("update", func(t *testing.T) {
		db, e := open(nil)
		if _, err := db.Update[bspouse]().Set(S.SpouseID.Set(3)).Where(S.ID.EQ(1)).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		want := slices.Clone(linked)
		want[1].args = []any{int64(3), int64(1)}
		sent(t, e, want...)
	})
	t.Run("SQL renders the caller's statement", func(t *testing.T) {
		db, e := open(nil)
		q, args, err := db.Save(&bspouse{ID: 1, SpouseID: new(int64(3))}, S.SpouseID).SQL(ctx)
		if err != nil || q != upd || !reflect.DeepEqual(args, []any{new(int64(3)), int64(1)}) {
			t.Errorf("SQL: %q %v %v", q, args, err)
		}
		sent(t, e)
	})
	taken := func(t *testing.T, found bool, want string) {
		t.Helper()
		db, e := open(map[string]int64{link: 0})
		if found {
			e.Rows = [][]any{{int64(1)}}
		}
		err := db.Save(&bspouse{ID: 1, SpouseID: new(int64(3))}, S.SpouseID).Exec(ctx)
		var ce *velox.ConstraintError
		if !errors.As(err, &ce) || ce.Kind != velox.ConstraintUnique || ce.Constraint != "spouse_id" || err.Error() != want {
			t.Fatalf("err = %v,\nwant a unique ConstraintError: %s", err, want)
		}
		sent(t, e, append(slices.Clone(linked[:4]), stmt{probe, []any{int64(3), 1}}, stmt{"ROLLBACK", nil})...)
	}
	t.Run("taken partner", func(t *testing.T) {
		taken(t, true, "velox: apitest.bspouse: unique constraint spouse_id violated: row 3 already has a Spouse; clear it first")
	})
	t.Run("hidden partner", func(t *testing.T) {
		taken(t, false, "velox: apitest.bspouse: unique constraint spouse_id violated: row 3 cannot be linked")
	})
	t.Run("no row", func(t *testing.T) {
		db, e := open(map[string]int64{upd: 0})
		err := db.Save(&bspouse{ID: 1, SpouseID: new(int64(3))}, S.SpouseID).Exec(ctx)
		if !errors.Is(err, velox.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
		sent(t, e, linked[0], linked[1], stmt{"COMMIT", nil})
	})
	t.Run("more rows", func(t *testing.T) {
		db, e := open(map[string]int64{upd: 2})
		err := db.Save(&bspouse{ID: 1, SpouseID: new(int64(3))}, S.SpouseID).Exec(ctx)
		if want := "velox: apitest.bspouse: the update that sets the bidirectional key SpouseID wrote 2 rows"; err == nil || err.Error() != want {
			t.Fatalf("err = %v, want %s", err, want)
		}
		sent(t, e, linked[0], linked[1], stmt{"ROLLBACK", nil})
	})
	t.Run("unknown counts", func(t *testing.T) {
		const want = "velox: apitest.bspouse: the driver did not report the rows the link wrote"
		// The caller's UPDATE and the link must say how many rows they
		// wrote; the clearing may write any number, so its count is not read.
		for _, c := range []struct {
			sql  string
			sent int
		}{{upd, 2}, {link, 4}} {
			db, e := open(map[string]int64{c.sql: -1})
			err := db.Save(&bspouse{ID: 1, SpouseID: new(int64(3))}, S.SpouseID).Exec(ctx)
			if err == nil || err.Error() != want {
				t.Errorf("%s: err = %v, want %s", c.sql, err, want)
			}
			sent(t, e, append(slices.Clone(linked[:c.sent]), stmt{"ROLLBACK", nil})...)
		}
		db, e := open(map[string]int64{unlink: -1})
		if err := db.Save(&bspouse{ID: 1, SpouseID: new(int64(3))}, S.SpouseID).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		sent(t, e, linked...)
	})
	t.Run("savepoint", func(t *testing.T) {
		db, e := open(nil)
		err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
			return tx.Save(&bspouse{ID: 1, SpouseID: new(int64(3))}, S.SpouseID).Exec(ctx)
		})
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{"BEGIN", "SAVEPOINT", upd, unlink, link, "RELEASE SAVEPOINT", "COMMIT"}; !sameStarts(e.Log, want) {
			t.Errorf("sent %q,\nwant %q", e.Log, want)
		}
	})
	t.Run("savepoint rolled back", func(t *testing.T) {
		db, e := open(map[string]int64{link: 0})
		err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
			if err := tx.Save(&bspouse{ID: 1, SpouseID: new(int64(3))}, S.SpouseID).Exec(ctx); err == nil {
				t.Error("a taken partner was linked")
			}
			_, err := tx.Update[bspouse]().Set(S.SpouseID.SetNull()).Where(S.ID.EQ(2)).Exec(ctx)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"BEGIN", "SAVEPOINT", upd, unlink, link, probe, "ROLLBACK TO SAVEPOINT", "RELEASE SAVEPOINT",
			"SAVEPOINT", upd, unlink, "RELEASE SAVEPOINT", "COMMIT"}
		if !sameStarts(e.Log, want) {
			t.Errorf("sent %q,\nwant %q", e.Log, want)
		}
	})
	t.Run("insert", func(t *testing.T) {
		db, e := open(nil)
		if err := db.Insert(&bspouse{ID: 5, SpouseID: new(int64(1))}).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		sent(t, e, stmt{"BEGIN", nil}, stmt{ins, []any{int64(5), new(int64(1))}}, stmt{link, []any{int64(5), int64(1)}}, stmt{"COMMIT", nil})
	})
	t.Run("insert without a key", func(t *testing.T) {
		db, e := open(nil)
		if err := db.Insert(&bspouse{ID: 5}).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		sent(t, e, stmt{ins, []any{int64(5), (*int64)(nil)}})
	})
	insGen := `INSERT INTO "bspouses" ("spouse_id") VALUES ($1) RETURNING "id"`
	t.Run("insert, the key not returned", func(t *testing.T) {
		// RETURNING gave no row: the row's key is unknown, so its partner
		// cannot be linked, and the insert is rolled back.
		const want = "velox: apitest.bspouse: the insert did not return the row's key, so its Spouse cannot be linked"
		db, e := open(nil)
		if err := db.Insert(&bspouse{SpouseID: new(int64(1))}).Exec(ctx); err == nil || err.Error() != want {
			t.Fatalf("err = %v, want %s", err, want)
		}
		if w := []string{"BEGIN", insGen, "ROLLBACK"}; !slices.Equal(e.Log, w) {
			t.Errorf("sent %q, want %q", e.Log, w)
		}
		// MySQL without LastInsertId.
		e = &fakeconn.Exec{Affected: 1}
		my := openFake(velox.MySQL, e)
		if err := my.Insert(&bspouse{SpouseID: new(int64(1))}).Exec(ctx); err == nil || err.Error() != want {
			t.Fatalf("MySQL: err = %v, want %s", err, want)
		}
		if w := []string{"BEGIN", "INSERT INTO `bspouses` (`spouse_id`) VALUES (?)", "ROLLBACK"}; !slices.Equal(e.Log, w) {
			t.Errorf("MySQL: sent %q, want %q", e.Log, w)
		}
		// Inside a caller's transaction: its savepoint is rolled back, and
		// the transaction goes on.
		db, e = open(nil)
		err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
			if err := tx.Insert(&bspouse{SpouseID: new(int64(1))}).Exec(ctx); err == nil || err.Error() != want {
				t.Errorf("in a Tx: err = %v, want %s", err, want)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if w := []string{"BEGIN", "SAVEPOINT", insGen, "ROLLBACK TO SAVEPOINT", "RELEASE SAVEPOINT", "COMMIT"}; !sameStarts(e.Log, w) {
			t.Errorf("in a Tx: sent %q, want %q", e.Log, w)
		}
	})
	t.Run("insert, the generated key is the partner", func(t *testing.T) {
		db, e := open(nil)
		e.Rows = [][]any{{int64(1)}} // RETURNING "id": 1, the partner given
		err := db.Insert(&bspouse{SpouseID: new(int64(1))}).Exec(ctx)
		if want := "velox: apitest.bspouse: SpouseID: a row cannot be its own Spouse"; err == nil || err.Error() != want {
			t.Fatalf("err = %v, want %s", err, want)
		}
		if w := []string{"BEGIN", insGen, "ROLLBACK"}; !slices.Equal(e.Log, w) {
			t.Errorf("sent %q, want %q", e.Log, w)
		}
	})
	t.Run("a Rewrite hook may not re-target the row", func(t *testing.T) {
		e := &fakeconn.Exec{Affected: 1}
		db := openFake(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
			if st.Op == velox.OpUpdate && len(st.Set) == 1 && st.Set[0].Value != nil {
				st.Where = []velox.Predicate{velox.ColumnEQ(0, int64(2))}
			}
			return nil
		}))
		err := db.Save(&bspouse{ID: 1, SpouseID: new(int64(3))}, S.SpouseID).Exec(ctx)
		if want := "velox: apitest.bspouse: a Rewrite hook changed the row that the update of the bidirectional key SpouseID names"; err == nil || err.Error() != want {
			t.Fatalf("err = %v, want %s", err, want)
		}
		if w := []string{"BEGIN", "ROLLBACK"}; !slices.Equal(e.Log, w) {
			t.Errorf("sent %q, want %q", e.Log, w)
		}
	})
	t.Run("a Rewrite hook may not make the insert an upsert", func(t *testing.T) {
		e := &fakeconn.Exec{Affected: 1}
		db := openFake(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
			if st.Op == velox.OpInsert {
				st.Conflict = &velox.ConflictClause{Columns: []int{0}, DoNothing: true}
			}
			return nil
		}))
		err := db.Insert(&bspouse{ID: 5, SpouseID: new(int64(1))}).Exec(ctx)
		if want := "velox: apitest.bspouse: an upsert cannot set the key of the bidirectional edge Spouse; insert the row, then Save the key"; err == nil || err.Error() != want {
			t.Fatalf("err = %v, want %s", err, want)
		}
		if w := []string{"BEGIN", "ROLLBACK"}; !slices.Equal(e.Log, w) {
			t.Errorf("sent %q, want %q", e.Log, w)
		}
	})
	t.Run("a Rewrite hook that replaces the statement may not re-target the row", func(t *testing.T) {
		e := &fakeconn.Exec{Affected: 1}
		db := openFake(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
			freshStatement(st)
			if st.Op == velox.OpUpdate && len(st.Set) == 1 && st.Set[0].Value != nil {
				st.Where = []velox.Predicate{velox.ColumnEQ(0, int64(2))}
			}
			return nil
		}))
		err := db.Save(&bspouse{ID: 1, SpouseID: new(int64(3))}, S.SpouseID).Exec(ctx)
		if want := "velox: apitest.bspouse: a Rewrite hook changed the row that the update of the bidirectional key SpouseID names"; err == nil || err.Error() != want {
			t.Fatalf("err = %v, want %s", err, want)
		}
		if w := []string{"BEGIN", "ROLLBACK"}; !slices.Equal(e.Log, w) {
			t.Errorf("sent %q, want %q", e.Log, w)
		}
	})
	t.Run("a Rewrite hook may not change the key's value", func(t *testing.T) {
		const want = "velox: apitest.bspouse: a Rewrite hook changed the value of SpouseID, the key of the bidirectional edge Spouse"
		for _, c := range []struct {
			name   string
			insert bool
			edit   func(st *velox.Statement)
		}{
			{"update, another value", false, func(st *velox.Statement) { st.Set[0].Value = int64(4) }},
			{"update, cleared", false, func(st *velox.Statement) { st.Set[0].Value = nil }},
			{"update, not a Set", false, func(st *velox.Statement) { st.Set[0].Op = velox.AssignAdd }},
			{"update, set twice", false, func(st *velox.Statement) { st.Set = append(st.Set, st.Set[0]) }},
			{"update, replaced", false, func(st *velox.Statement) {
				freshStatement(st)
				st.Set = []velox.Assignment{{Column: 1, Op: velox.AssignSet, Value: int64(4)}}
			}},
			{"insert, another value", true, func(st *velox.Statement) { st.Rows[0][1] = new(int64(4)) }},
			{"insert, cleared", true, func(st *velox.Statement) { st.Rows[0][1] = (*int64)(nil) }},
			{"insert, dropped", true, func(st *velox.Statement) { st.Columns, st.Rows = st.Columns[:1], [][]any{st.Rows[0][:1]} }},
		} {
			e := &fakeconn.Exec{Affected: 1}
			first := true
			db := openFake(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
				if first {
					first = false
					c.edit(st)
				}
				return nil
			}))
			var err error
			if c.insert {
				err = db.Insert(&bspouse{ID: 5, SpouseID: new(int64(3))}).Exec(ctx)
			} else {
				err = db.Save(&bspouse{ID: 1, SpouseID: new(int64(3))}, S.SpouseID).Exec(ctx)
			}
			if err == nil || err.Error() != want {
				t.Errorf("%s: err = %v, want %s", c.name, err, want)
			}
			if w := []string{"BEGIN", "ROLLBACK"}; !slices.Equal(e.Log, w) {
				t.Errorf("%s: sent %q, want %q", c.name, e.Log, w)
			}
		}
	})
	t.Run("a benign Rewrite hook", func(t *testing.T) {
		// A hook that tags every statement, and replaces it with a copy of
		// its exported fields, changes nothing a bidirectional write follows.
		e := &fakeconn.Exec{Affected: 1}
		db := openFake(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
			freshStatement(st)
			st.Tags = append(st.Tags, velox.Tag{Key: "audit", Value: "yes"})
			return nil
		}))
		if err := db.Save(&bspouse{ID: 1, SpouseID: new(int64(3))}, S.SpouseID).Exec(ctx); err != nil {
			t.Fatalf("Save: %v", err)
		}
		sent(t, e, linked...)
		e.Log, e.ArgLog = nil, nil
		if _, err := db.Update[bspouse]().Set(S.SpouseID.Set(3)).Where(S.ID.EQ(1)).Exec(ctx); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if w := []string{"BEGIN", upd, unlink, link, "COMMIT"}; !slices.Equal(e.Log, w) {
			t.Errorf("Update: sent %q, want %q", e.Log, w)
		}
		e.Log, e.ArgLog = nil, nil
		if err := db.Insert(&bspouse{ID: 5, SpouseID: new(int64(1))}).Exec(ctx); err != nil {
			t.Fatalf("Insert: %v", err)
		}
		sent(t, e, stmt{"BEGIN", nil}, stmt{ins, []any{int64(5), new(int64(1))}}, stmt{link, []any{int64(5), int64(1)}}, stmt{"COMMIT", nil})
	})
	t.Run("refusals before the transaction name the entity", func(t *testing.T) {
		save := func(ctx context.Context, db *velox.DB) error {
			return db.Save(&bspouse{ID: 1, SpouseID: new(int64(3))}, S.SpouseID).Exec(ctx)
		}
		insert := func(ctx context.Context, db *velox.DB) error {
			return db.Insert(&bspouse{ID: 5, SpouseID: new(int64(1))}).Exec(ctx)
		}
		check := func(t *testing.T, name string, err, is error, sent []string, e *fakeconn.Exec) {
			t.Helper()
			if !errors.Is(err, is) || !strings.HasPrefix(fmt.Sprint(err), "velox: apitest.bspouse: ") {
				t.Errorf("%s: %v, want velox: apitest.bspouse: …, matching %v", name, err, is)
			}
			if !slices.Equal(e.Log, sent) {
				t.Errorf("%s: sent %q, want %q", name, e.Log, sent)
			}
		}
		for _, w := range []func(context.Context, *velox.DB) error{save, insert} {
			e := &fakeconn.Exec{Affected: 1}
			dry, db := dryContext(t, e)
			check(t, "dry run", w(dry, db), velox.ErrDryRun, nil, e)

			e = &fakeconn.Exec{Affected: 1}
			db = openFake(velox.Postgres, e)
			var err error
			_ = db.Tx(ctx, func(ctx context.Context, _ *velox.DB) error {
				err = w(ctx, db) // db, not the transaction's
				return nil
			})
			check(t, "outside its transaction", err, velox.ErrOutsideTx, []string{"BEGIN", "COMMIT"}, e)

			gone := errors.New("the connection is gone")
			e = &fakeconn.Exec{Affected: 1, Errs: []error{nil, gone}} // SAVEPOINT, then ROLLBACK TO
			db = openFake(velox.Postgres, e)
			_ = db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
				_ = tx.Tx(ctx, func(context.Context, *velox.DB) error { return errors.New("boom") })
				err = w(ctx, tx)
				return nil
			})
			check(t, "broken", err, gone, []string{"BEGIN", e.Log[1], e.Log[2], "ROLLBACK"}, e)

			s := openShards(false)
			check(t, "no shard", w(ctx, s.db), velox.ErrNoShard, nil, s.euP)
			if s.calls() != [3]int{} {
				t.Errorf("no shard: sent %v", s.calls())
			}
		}
	})
	t.Run("refused", func(t *testing.T) {
		db, e := open(nil)
		const (
			self   = "velox: apitest.bspouse: SpouseID: a row cannot be its own Spouse"
			oneRow = "velox: apitest.bspouse: an update that sets SpouseID, the key of the bidirectional edge Spouse, must name one row by its primary key (Where on ID.EQ), as velox re-points the other row too"
		)
		_, errIn := db.Update[bspouse]().Set(S.SpouseID.Set(3)).Where(S.ID.In(1, 2)).Exec(ctx)
		_, errAll := db.Update[bspouse]().Set(S.SpouseID.Set(3)).All().Exec(ctx)
		_, errAdd := db.Update[bspouse]().Set(S.SpouseID.Add(1)).Where(S.ID.EQ(1)).Exec(ctx)
		for name, err := range map[string]error{
			"save self":   db.Save(&bspouse{ID: 1, SpouseID: new(int64(1))}, S.SpouseID).Exec(ctx),
			"insert self": db.Insert(&bspouse{ID: 1, SpouseID: new(int64(1))}).Exec(ctx),
			"update self": func() error {
				_, err := db.Update[bspouse]().Set(S.SpouseID.Set(1)).Where(S.ID.EQ(1)).Exec(ctx)
				return err
			}(),
		} {
			// The value of one field is refused: GraphQL maps it to the
			// input field, as BAD_USER_INPUT.
			var ve *velox.ValidationError
			if !errors.As(err, &ve) || ve.Entity != "apitest.bspouse" || ve.Field != "SpouseID" {
				t.Errorf("%s: %v, want a ValidationError on SpouseID", name, err)
			}
		}
		for _, c := range []struct {
			name string
			err  error
			want string
		}{
			{"save self", db.Save(&bspouse{ID: 1, SpouseID: new(int64(1))}, S.SpouseID).Exec(ctx), self},
			{"insert self", db.Insert(&bspouse{ID: 1, SpouseID: new(int64(1))}).Exec(ctx), self},
			{"update in", errIn, oneRow},
			{"update all", errAll, oneRow},
			{"update add", errAdd, "velox: apitest.bspouse: SpouseID, the key of the bidirectional edge Spouse, can only be set"},
			{"upsert", db.Insert(&bspouse{ID: 5, SpouseID: new(int64(1))}).OnConflict(S.ID).DoNothing().Exec(ctx),
				"velox: apitest.bspouse: an upsert cannot set the key of the bidirectional edge Spouse; insert the row, then Save the key"},
			{"insert all", db.InsertAll([]bspouse{{ID: 5}, {ID: 6, SpouseID: new(int64(1))}}).Exec(ctx),
				"velox: apitest.bspouse: InsertAll cannot set the key of the bidirectional edge Spouse; insert the rows, then Save each key"},
		} {
			if c.err == nil || c.err.Error() != c.want {
				t.Errorf("%s: %v,\nwant %s", c.name, c.err, c.want)
			}
		}
		if len(e.Log) != 0 {
			t.Errorf("sent %q", e.Log)
		}
	})
}

// freshStatement replaces *st with a Statement that holds only its exported
// fields, as a Rewrite hook that writes *st = velox.Statement{…} does: every
// field velox keeps unexported is zero.
func freshStatement(st *velox.Statement) {
	var fresh velox.Statement
	from, to := reflect.ValueOf(st).Elem(), reflect.ValueOf(&fresh).Elem()
	for i := range from.NumField() {
		if from.Type().Field(i).IsExported() {
			to.Field(i).Set(from.Field(i))
		}
	}
	*st = fresh
}

// sameStarts reports whether got is want, where an element of want that
// ends in SAVEPOINT matches one that starts with it and names the savepoint.
func sameStarts(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] && (!strings.HasSuffix(want[i], "SAVEPOINT") || !strings.HasPrefix(got[i], want[i]+" ")) {
			return false
		}
	}
	return true
}

// btspouse is bspouse with a tenant column: the bidirectional one-to-one
// model of TestBidiOneTenant.
type btspouse struct {
	ID       int64
	OrgID    int64
	SpouseID *int64
	Spouse   velox.One[btspouse]
}

// Policy lets every write: the clearing of the old partner bypasses it, and
// says so.
func (*btspouse) Policy() velox.Policy[btspouse] {
	return velox.Policy[btspouse]{Write: func(context.Context) (velox.Pred[btspouse], error) { return velox.Allow[btspouse](), nil }}
}

var btspouseMeta velox.MetaOnce[btspouse]

func (*btspouse) VeloxMeta() *velox.Meta[btspouse] {
	return btspouseMeta.Load(func() velox.Desc[btspouse] {
		return velox.Desc[btspouse]{
			Policy:     func() velox.BoundPolicy { return velox.BindPolicy((*btspouse)(nil).Policy()) },
			Table:      "btspouses",
			Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "OrgID", Name: "org_id"}, {Field: "SpouseID", Name: "spouse_id"}},
			PrimaryKey: []int{0},
			Tenant:     1,
			Indexes:    []velox.IndexInfo{{Columns: []int{2}, Unique: true}},
			Edges:      []velox.EdgeDesc{velox.EdgeBidi[btspouse]("Spouse", 2, 0)},
			Addr: func(x *btspouse, i int) any {
				return [...]any{&x.ID, &x.OrgID, &x.SpouseID}[i]
			},
			EdgeAddr: func(x *btspouse, i int) any { return &x.Spouse },
		}
	})
}

// TestBidiOneTenant: under WithTenant, the caller's UPDATE, the link and the
// probe are the tenant's, and the clearing of the old partner is not: it is
// the edge's integrity, which hooks see by its bypass reasons. A partner the
// tenant cannot see, one of another tenant, cannot be linked, and the write
// is rolled back.
func TestBidiOneTenant(t *testing.T) {
	const (
		upd    = `UPDATE "btspouses" SET "spouse_id" = $1 WHERE "id" = $2 AND "org_id" = $3`
		unlink = `UPDATE "btspouses" SET "spouse_id" = $1 WHERE "spouse_id" = $2`
		link   = `UPDATE "btspouses" SET "spouse_id" = $1 WHERE "id" = $2 AND "spouse_id" IS NULL AND "org_id" = $3`
		probe  = `SELECT 1 FROM "btspouses" WHERE "id" = $1 AND "org_id" = $2 LIMIT $3`
		reason = "velox: clearing the old partner of a bidirectional one-to-one edge"
	)
	type seen struct{ sql, tenantBypass, policyBypass string }
	var got []seen
	e := &fakeconn.Exec{AffectedBy: func(sql string) int64 {
		if sql == link {
			return 0 // row 3 is another tenant's
		}
		return 1
	}}
	db := openFake(velox.Postgres, e, velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
		got = append(got, seen{st.SQL, st.TenantBypass, st.PolicyBypass})
		return next(ctx)
	}))
	err := db.Save(&btspouse{ID: 1, SpouseID: new(int64(3))}, velox.NullNumberCol[btspouse, int64]{I: 2}).Exec(velox.WithTenant(ctx, int64(7)))
	var ce *velox.ConstraintError
	if want := "velox: apitest.btspouse: unique constraint spouse_id violated: row 3 cannot be linked"; !errors.As(err, &ce) || err.Error() != want {
		t.Fatalf("err = %v, want %s", err, want)
	}
	want := []seen{{upd, "", ""}, {unlink, reason, reason}, {link, "", ""}, {probe, "", ""}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the hooks saw\n%q,\nwant\n%q", got, want)
	}
	if w := []string{"BEGIN", upd, unlink, link, probe, "ROLLBACK"}; !slices.Equal(e.Log, w) {
		t.Errorf("sent %q, want %q", e.Log, w)
	}
	if w := []any{int64(1), int64(3), int64(7)}; len(e.ArgLog) <= 3 || !reflect.DeepEqual(e.ArgLog[3], w) {
		t.Errorf("the link's args %v, want %v", e.ArgLog, w)
	}
}
