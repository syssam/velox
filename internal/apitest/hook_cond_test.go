package apitest

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

// hComb declares its hooks with the combinators, by name: a stamp hook on
// writes that assign title, unless they clear note, and a Reject of
// deletes.
type hComb struct {
	ID    int64
	Title string
	Note  *string
	Stamp int64
}

func (*hComb) Hooks() []velox.Hook {
	stamp := velox.Before(func(_ context.Context, m *velox.Mutation) error {
		return m.Set("stamp", int64(7))
	}, velox.OpInsert, velox.OpUpdate)
	return []velox.Hook{
		velox.Unless(velox.If(stamp, velox.HasColumns("title")), velox.HasCleared("note")),
		velox.Reject(velox.OpDelete),
	}
}

var hCombMeta velox.MetaOnce[hComb]

func (*hComb) VeloxMeta() *velox.Meta[hComb] {
	return hCombMeta.Load(func() velox.Descriptor[hComb] {
		return velox.Descriptor[hComb]{
			Schema: (*hComb)(nil),
			Table:  "combs",
			Columns: []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Title", Name: "title"},
				{Field: "Note", Name: "note"}, {Field: "Stamp", Name: "stamp"}},
			PrimaryKey: []int{0},
			FieldPtr: func(x *hComb, i int) any {
				switch i {
				case 0:
					return &x.ID
				case 1:
					return &x.Title
				case 2:
					return &x.Note
				}
				return &x.Stamp
			},
		}
	})
}

var (
	hkID    = velox.NumberCol[hComb, int64]{I: 0}
	hkTitle = velox.StringCol[hComb, string]{I: 1}
	hkNote  = velox.NullStringCol[hComb, string]{I: 2}
)

// TestHookCombinatorsInTheSchema: If, Unless, HasColumns, HasCleared and
// Reject work on a schema's hooks, which name columns by name.
func TestHookCombinatorsInTheSchema(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.SQLite, e)
	sql := func(u velox.UpdateQuery[hComb]) string {
		t.Helper()
		got, _, err := u.SQL(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got, want := sql(db.Update[hComb]().Set(hkTitle.Set("t")).Where(hkID.EQ(1))), `UPDATE "combs" SET "title" = ?, "stamp" = ? WHERE "id" = ?`; got != want {
		t.Errorf("title:\n got %s\nwant %s", got, want)
	}
	if got, want := sql(db.Update[hComb]().Set(hkTitle.Set("t"), hkNote.SetNull()).Where(hkID.EQ(1))), `UPDATE "combs" SET "title" = ?, "note" = ? WHERE "id" = ?`; got != want {
		t.Errorf("title and a cleared note:\n got %s\nwant %s", got, want)
	}
	if got, want := sql(db.Update[hComb]().Set(hkNote.Set("n")).Where(hkID.EQ(1))), `UPDATE "combs" SET "note" = ? WHERE "id" = ?`; got != want {
		t.Errorf("no title:\n got %s\nwant %s", got, want)
	}
	x := hComb{ID: 1, Title: "t"}
	if _, args, err := db.Insert(&x).SQL(ctx); err != nil || x.Stamp != 7 || !slices.Contains(args, any(int64(7))) {
		t.Errorf("insert with a title: %v, stamp %d, args %v", err, x.Stamp, args)
	}
	y := hComb{ID: 2}
	if _, _, err := db.Insert(&y).SQL(ctx); err != nil || y.Stamp != 0 {
		t.Errorf("insert without a title: %v, stamp %d", err, y.Stamp)
	}
	_, err := db.Delete[hComb]().Where(hkID.EQ(1)).Exec(ctx)
	if !errors.Is(err, velox.ErrDenied) || e.Calls != 0 {
		t.Errorf("Reject: %v, calls %d", err, e.Calls)
	}
}

// TestMutationIntrospection: a run-time hook (WithHooks) reads the columns
// a write assigns, the ones it clears and its edge setters.
func TestMutationIntrospection(t *testing.T) {
	type seen struct {
		op      velox.Op
		cols    []string
		cleared bool
	}
	var got []seen
	look := func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m *velox.Mutation) error {
			got = append(got, seen{m.Op(), m.Columns(), m.Cleared("paid_at")})
			return next.Mutate(ctx, m)
		})
	}
	var edges [][]velox.EdgeChange
	lookEdges := func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m *velox.Mutation) error {
			edges = append(edges, m.Edges())
			return next.Mutate(ctx, m)
		})
	}
	db := openFake(velox.Postgres, &fakeconn.Exec{},
		velox.WithHooks[testmodel.Invoice](velox.If(look, velox.HasOp(velox.OpUpdate, velox.OpInsert))),
		velox.WithHooks[testmodel.Paper](lookEdges))

	if _, _, err := db.Update[testmodel.Invoice]().Set(I.Total.Add(1), I.PaidAt.SetNull()).Where(I.ID.EQ(1)).SQL(ctx); err != nil {
		t.Fatal(err)
	}
	inv := testmodel.Invoice{OrgID: 1, Number: "N"}
	if _, _, err := db.Insert(&inv).SQL(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.Delete[testmodel.Invoice]().Where(I.ID.EQ(1)).SQL(ctx); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("hook ran %d times, want 2 (HasOp skips the delete): %+v", len(got), got)
	}
	if u := got[0]; !slices.Equal(u.cols, []string{"total", "paid_at"}) || !u.cleared {
		t.Errorf("update: columns %v, cleared %v", u.cols, u.cleared)
	}
	if i := got[1]; !slices.Equal(i.cols, []string{"created_at", "updated_at", "org_id", "number"}) || i.cleared {
		t.Errorf("insert: columns %v, cleared %v", i.cols, i.cleared)
	}

	P := testmodel.Papers
	if _, _, err := db.Update[testmodel.Paper]().Set(P.Title.Set("t"), P.Topics.Add("a", "b"), P.Topics.Remove("c")).Where(P.Title.EQ("old")).SQL(ctx); err != nil {
		t.Fatal(err)
	}
	p := testmodel.Paper{Title: "t"}
	if _, _, err := db.Insert(&p).Set(P.Topics.Add("d")).SQL(ctx); err != nil {
		t.Fatal(err)
	}
	if len(edges) != 2 {
		t.Fatalf("paper hook ran %d times, want 2", len(edges))
	}
	u := edges[0]
	if len(u) != 2 || u[0].Name != "topics" || u[0].Op != velox.EdgeAdd || !slices.Equal(u[0].Keys, []any{"a", "b"}) || u[1].Op != velox.EdgeRemove {
		t.Errorf("update edges: %+v", u)
	}
	if i := edges[1]; len(i) != 1 || i[0].Op != velox.EdgeAdd || !slices.Equal(i[0].Keys, []any{"d"}) {
		t.Errorf("insert edges: %+v", i)
	}
}
