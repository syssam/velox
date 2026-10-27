package velox_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/syssam/velox2"
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
	sent := func(t *testing.T, e *velox.FakeExec, sql string, args ...any) {
		t.Helper()
		if len(e.Log) != 1 || e.Log[0] != sql {
			t.Errorf("sent %q,\nwant %q", e.Log, sql)
		}
		if !reflect.DeepEqual(e.Args, args) {
			t.Errorf("args %v, want %v", e.Args, args)
		}
	}
	t.Run("insert", func(t *testing.T) {
		e := &velox.FakeExec{}
		if err := velox.OpenFake(velox.Postgres, e).Insert(&bnodeFriend{1, 2}).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		sent(t, e, `INSERT INTO "bnode_friends" ("node_id", "friend_id") VALUES ($1, $2), ($3, $4)`, int64(1), int64(2), int64(2), int64(1))
	})
	t.Run("self-link", func(t *testing.T) {
		e := &velox.FakeExec{}
		if err := velox.OpenFake(velox.Postgres, e).Insert(&bnodeFriend{3, 3}).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		sent(t, e, `INSERT INTO "bnode_friends" ("node_id", "friend_id") VALUES ($1, $2)`, int64(3), int64(3))
	})
	t.Run("insert all, do nothing", func(t *testing.T) {
		e := &velox.FakeExec{}
		err := velox.OpenFake(velox.Postgres, e).InsertAll([]bnodeFriend{{1, 2}, {1, 3}}).OnConflict(F.NodeID, F.FriendID).DoNothing().Exec(ctx)
		if err != nil {
			t.Fatal(err)
		}
		sent(t, e, `INSERT INTO "bnode_friends" ("node_id", "friend_id") VALUES ($1, $2), ($3, $4), ($5, $6), ($7, $8) ON CONFLICT ("node_id", "friend_id") DO NOTHING`,
			int64(1), int64(2), int64(2), int64(1), int64(1), int64(3), int64(3), int64(1))
	})
	t.Run("delete", func(t *testing.T) {
		e := &velox.FakeExec{}
		if _, err := velox.OpenFake(velox.Postgres, e).Delete[bnodeFriend]().Where(F.NodeID.EQ(1), F.FriendID.In(2, 3)).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		sent(t, e, `DELETE FROM "bnode_friends" WHERE (("node_id" = $1 AND "friend_id" IN ($2, $3)) OR ("friend_id" = $4 AND "node_id" IN ($5, $6)))`,
			int64(1), int64(2), int64(3), int64(1), int64(2), int64(3))
	})
	t.Run("delete, nested", func(t *testing.T) {
		e := &velox.FakeExec{}
		w := velox.Or(F.NodeID.EQ(1), velox.Not(F.FriendID.EQ(2)))
		if _, err := velox.OpenFake(velox.Postgres, e).Delete[bnodeFriend]().Where(w).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		sent(t, e, `DELETE FROM "bnode_friends" WHERE (("node_id" = $1 OR NOT ("friend_id" = $2)) OR ("friend_id" = $3 OR NOT ("node_id" = $4)))`,
			int64(1), int64(2), int64(1), int64(2))
	})
	t.Run("delete all", func(t *testing.T) {
		e := &velox.FakeExec{}
		if _, err := velox.OpenFake(velox.Postgres, e).Delete[bnodeFriend]().All().Exec(ctx); err != nil {
			t.Fatal(err)
		}
		sent(t, e, `DELETE FROM "bnode_friends"`)
	})
	t.Run("load", func(t *testing.T) {
		// Both rows are there, so a load reads the owner's column alone.
		e := &velox.FakeExec{Results: [][][]any{{{int64(1)}, {int64(2)}}, {{int64(1), int64(2)}, {int64(2), int64(1)}}, {{int64(1)}, {int64(2)}}}}
		ns, err := velox.OpenFake(velox.Postgres, e).Query[bnode]().With(velox.Rel[bnode, bnode]{I: 0}).Limit(2).All(ctx)
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
		const want = "velox: velox_test.bnodeFriend: the join entity of a bidirectional edge cannot be updated; delete the link and insert it again"
		e := &velox.FakeExec{}
		db := velox.OpenFake(velox.Postgres, e)
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
		e := &velox.FakeExec{}
		db := velox.OpenFake(velox.Postgres, e)
		if err := db.InsertAll([]bnodeKeyed{{bkey("x"), bkey("x")}, {bkey("x"), bkey("y")}}).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		sent(t, e, `INSERT INTO "keyed_links" ("a", "b") VALUES ($1, $2), ($3, $4), ($5, $6)`,
			bkey("x"), bkey("x"), bkey("x"), bkey("y"), bkey("y"), bkey("x"))
	})
	t.Run("a refused update runs no hook", func(t *testing.T) {
		bidiSeen.calls = 0
		e := &velox.FakeExec{}
		db := velox.OpenFake(velox.Postgres, e)
		_, errUpdate := db.Update[bnodeFriendP]().Set(velox.NumberCol[bnodeFriendP, int64]{I: 1}.Set(5)).All().Exec(ctx)
		errSave := db.Save(&bnodeFriendP{1, 2}, velox.NumberCol[bnodeFriendP, int64]{I: 1}).Exec(ctx)
		if errUpdate == nil || errSave == nil || bidiSeen.calls != 0 || len(e.Log) != 0 {
			t.Errorf("update %v, save %v, %d hook calls, sent %q", errUpdate, errSave, bidiSeen.calls, e.Log)
		}
	})
	t.Run("policy and hooks see the caller's rows", func(t *testing.T) {
		bidiSeen.inserted, bidiSeen.hookRows, bidiSeen.affected = nil, 0, 0
		e := &velox.FakeExec{Affected: 2}
		if err := velox.OpenFake(velox.Postgres, e).Insert(&bnodeFriendP{1, 2}).Exec(ctx); err != nil {
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
