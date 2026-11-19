package gqlgraph_test

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/untyped"
)

// vWhere is a where input of person as compat=veloxe generates it: its type
// has Veloxe, or with def not, the same input under velox's semantics.
type vWhere struct {
	def bool // velox's own semantics, for the contrast

	Not *vWhere
	And []*vWhere
	Or  []*vWhere

	ID      *int64
	IDIn    []int64
	IDNotIn []int64
}

func (w *vWhere) predicate(ctx context.Context) (velox.Predicate, error) {
	typ := gqlgraph.TableType[person]("Person")
	typ.Veloxe = w == nil || !w.def
	f := gqlgraph.NewFilter(ctx, typ)
	if w == nil {
		return f.Pred()
	}
	gqlgraph.Not(f, "not", w.Not, (*vWhere).predicate)
	gqlgraph.And(f, "and", w.And, (*vWhere).predicate)
	gqlgraph.Or(f, "or", w.Or, (*vWhere).predicate)
	gqlgraph.IDField(f, "id", 0, gqlgraph.Ops[int64]{EQ: w.ID, In: w.IDIn, NotIn: w.IDNotIn})
	return f.Pred()
}

// vPeople returns the keys of the people w selects, in key order, or Pred's
// error.
func vPeople(t *testing.T, db *velox.DB, w *vWhere) ([]int64, error) {
	t.Helper()
	ctx := context.Background()
	p, err := twin[person](t, ctx, w.predicate, w.Predicate)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query[person]().Where(velox.Pred[person](p)).OrderBy(people.ID.Asc()).AllowUnbounded().All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]int64, len(rows))
	for i, r := range rows {
		out[i] = r.ID
	}
	return out, nil
}

// span is the keys from..to.
func span(from, to int64) []int64 {
	var out []int64
	for k := from; k <= to; k++ {
		out = append(out, k)
	}
	return out
}

// TestVeloxeWhere: under Type.Veloxe a where input means what veloxe's
// generated P() meant: an empty In or NotIn, an empty or and a null entry
// add nothing; an and leaves out an entry that sets nothing, and a not or an
// or with one fails; no width or node cap applies. Each case also holds what
// velox's own semantics give, so the default is shown unchanged.
func TestVeloxeWhere(t *testing.T) {
	db, _ := peopleDB(t)
	wide := span(1, 150) // over MaxFilterWidth
	var many []*vWhere   // over MaxFilterNodes, in width 100 each
	for range 6 {
		and := make([]*vWhere, 100)
		for i := range and {
			and[i] = &vWhere{IDNotIn: []int64{99}}
		}
		many = append(many, &vWhere{And: and})
	}
	for _, c := range []struct {
		name    string
		w       vWhere
		want    []int64 // under Veloxe; nil with wantErr
		wantErr string  // under Veloxe
		def     []int64 // under velox's own; nil with defErr
		defErr  error
	}{
		{name: "nothing", want: all, def: all},
		{name: "idIn []", w: vWhere{IDIn: []int64{}}, want: all, def: []int64{}},
		{name: "idNotIn []", w: vWhere{IDNotIn: []int64{}}, want: all, def: all},
		{name: "or []", w: vWhere{Or: []*vWhere{}}, want: all, def: []int64{}},
		{name: "or [null]", w: vWhere{Or: []*vWhere{nil}}, want: all, def: all},
		{name: "or [null, id 3]", w: vWhere{Or: []*vWhere{nil, {ID: new(int64(3))}}}, want: []int64{3}, def: all},
		{name: "and [{}]", w: vWhere{And: []*vWhere{{}}}, want: all, def: all},
		{name: "and [{idIn: []}, id 4]", w: vWhere{And: []*vWhere{{IDIn: []int64{}}, {ID: new(int64(4))}}}, want: []int64{4}, def: []int64{}},
		{name: "not {}", w: vWhere{Not: &vWhere{}}, wantErr: "PersonWhereInput.not: empty predicate PersonWhereInput", def: []int64{}},
		{name: "not {idIn: []}", w: vWhere{Not: &vWhere{IDIn: []int64{}}}, wantErr: "empty predicate", def: all},
		{name: "or [{}]", w: vWhere{Or: []*vWhere{{}}}, wantErr: "PersonWhereInput.or[0]: empty predicate", def: all},
		{name: "and [{not: {}}]", w: vWhere{And: []*vWhere{{Not: &vWhere{}}}}, wantErr: "empty predicate", def: []int64{}},
		{name: "idIn of 150", w: vWhere{IDIn: wide}, want: all, defErr: veloxgql.ErrFilterTooWide},
		{name: "1200 nodes", w: vWhere{And: many}, want: all, defErr: veloxgql.ErrFilterTooLarge},
	} {
		got, err := vPeople(t, db, &c.w)
		switch {
		case c.wantErr != "":
			if err == nil || !strings.Contains(err.Error(), c.wantErr) || codeOf(err) != veloxgql.CodeBadUserInput {
				t.Errorf("%s: %v, %v; want BAD_USER_INPUT %q", c.name, got, err, c.wantErr)
			}
		case err != nil || !slices.Equal(got, c.want):
			t.Errorf("%s: %v, %v; want %v", c.name, got, err, c.want)
		}

		d := c.w
		setDef(&d)
		got, err = vPeople(t, db, &d)
		switch {
		case c.defErr != nil:
			if !errors.Is(err, c.defErr) {
				t.Errorf("%s, by default: %v, %v; want %v", c.name, got, err, c.defErr)
			}
		case err != nil || !slices.Equal(got, c.def):
			t.Errorf("%s, by default: %v, %v; want %v", c.name, got, err, c.def)
		}
	}
}

// setDef sets def on w and every input nested in it, which it copies.
func setDef(w *vWhere) {
	w.def = true
	if w.Not != nil {
		n := *w.Not
		setDef(&n)
		w.Not = &n
	}
	for _, l := range []*[]*vWhere{&w.And, &w.Or} {
		c := slices.Clone(*l)
		for i, x := range c {
			if x != nil {
				n := *x
				setDef(&n)
				c[i] = &n
			}
		}
		*l = c
	}
}

// TestVeloxeDepth: MaxFilterDepth applies under Veloxe too, as veloxe's
// depth 5 did.
func TestVeloxeDepth(t *testing.T) {
	db, _ := peopleDB(t)
	w := &vWhere{ID: new(int64(1))}
	for range 6 {
		w = &vWhere{Not: w}
	}
	if _, err := vPeople(t, db, w); !errors.Is(err, veloxgql.ErrFilterTooDeep) {
		t.Errorf("nested 6 deep: %v, want ErrFilterTooDeep", err)
	}
}

// vNoteWhere is noteWhere under Veloxe.
type vNoteWhere struct {
	TagsHasSome  []string
	TagsHasEvery []string
	TagsIsEmpty  *bool
	TagsIsNil    *bool
	TagsNotNil   *bool
}

func (w *vNoteWhere) predicate(ctx context.Context) (velox.Predicate, error) {
	typ := gqlgraph.TableType[note]("Note")
	typ.Veloxe = true
	f := gqlgraph.NewFilter(ctx, typ)
	gqlgraph.ArrayField(f, "tags", 1, gqlgraph.ArrayOps[string]{
		HasSome: w.TagsHasSome, HasEvery: w.TagsHasEvery, IsEmpty: w.TagsIsEmpty, IsNil: w.TagsIsNil, NotNil: w.TagsNotNil,
	})
	return f.Pred()
}

// TestVeloxeNullTests: under Veloxe a false IsNil, NotNil or IsEmpty and an
// empty HasSome or HasEvery add nothing, as veloxe's did; a true one tests.
func TestVeloxeNullTests(t *testing.T) {
	db := notesDB(t)
	no, yes := false, true
	every := []int64{1, 2, 3, 4, 5}
	for _, c := range []struct {
		name string
		w    vNoteWhere
		want []int64
	}{
		{"tagsIsNil: false", vNoteWhere{TagsIsNil: &no}, every},
		{"tagsNotNil: false", vNoteWhere{TagsNotNil: &no}, every},
		{"tagsIsEmpty: false", vNoteWhere{TagsIsEmpty: &no}, every},
		{"tagsHasSome: []", vNoteWhere{TagsHasSome: []string{}}, every},
		{"tagsHasEvery: []", vNoteWhere{TagsHasEvery: []string{}}, every},
		{"tagsIsNil: true", vNoteWhere{TagsIsNil: &yes}, []int64{3}},
		{"tagsNotNil: true", vNoteWhere{TagsNotNil: &yes}, []int64{1, 2, 4, 5}},
	} {
		p, err := twin[note](t, context.Background(), c.w.predicate, c.w.Predicate)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		rows, err := untyped.Query(db, untyped.TableOf[note]()).Columns(0).Where(p).OrderBy(untyped.OrderByTerm(untyped.Col(0), false)).AllowUnbounded().All(context.Background())
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		var got []int64
		for _, n := range untyped.RowsOf[note](rows) {
			got = append(got, n.ID)
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// badge has a Read policy that hides badge 21, and vault one that denies
// every read, for the Node tests.
type (
	badge struct{ ID int64 }
	vault struct{ ID int64 }
)

var (
	badgeMeta velox.MetaOnce[badge]
	vaultMeta velox.MetaOnce[vault]
)

func (*badge) VeloxMeta() *velox.Meta[badge] {
	return badgeMeta.Load(func() velox.Descriptor[badge] {
		return velox.Descriptor[badge]{
			Table: "badges", Columns: []velox.ColumnDescriptor{{Field: "ID", Name: "id"}}, PrimaryKey: []int{0},
			Schema: fixture.Fns{Policy: []func() velox.Policy{func() velox.Policy {
				return velox.Policy{Read: func(context.Context) (velox.Predicate, error) {
					return untyped.TableOf[badge]().Pred(0, untyped.PredNEQ, int64(21)), nil
				}}
			}}},
			FieldPtr: func(x *badge, _ int) any { return &x.ID },
		}
	})
}

func (*vault) VeloxMeta() *velox.Meta[vault] {
	return vaultMeta.Load(func() velox.Descriptor[vault] {
		return velox.Descriptor[vault]{
			Table: "vaults", Columns: []velox.ColumnDescriptor{{Field: "ID", Name: "id"}}, PrimaryKey: []int{0},
			Schema: fixture.Fns{Policy: []func() velox.Policy{func() velox.Policy {
				return velox.Policy{Read: func(context.Context) (velox.Predicate, error) {
					return velox.Predicate{}, velox.Deny("vaults are closed")
				}}
			}}},
			FieldPtr: func(x *vault, _ int) any { return &x.ID },
		}
	})
}

// nodeDB holds people 1..3, badges 3, 20 and 21, and vault 30: 3 is a person
// and a badge.
func nodeDB(t *testing.T) (*velox.DB, []*gqlgraph.Type) {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	sqlDB.SetMaxOpenConns(1)
	for _, s := range []string{
		"CREATE TABLE people (id INTEGER PRIMARY KEY, name TEXT NOT NULL, age INTEGER NOT NULL)",
		"CREATE TABLE badges (id INTEGER PRIMARY KEY)",
		"CREATE TABLE vaults (id INTEGER PRIMARY KEY)",
		"INSERT INTO people (id, name, age) VALUES (1, 'a', 1), (2, 'b', 2), (3, 'c', 3)",
		"INSERT INTO badges (id) VALUES (3), (20), (21)",
		"INSERT INTO vaults (id) VALUES (30)",
	} {
		if _, err := sqlDB.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	key := []gqlgraph.Column{{Name: "id", Index: 0}}
	types := []*gqlgraph.Type{
		{Name: "Badge", Table: untyped.TableOf[badge], Key: key},
		{Name: "Person", Table: untyped.TableOf[person], Key: key},
		{Name: "Vault", Table: untyped.TableOf[vault], Key: key},
	}
	return velox.Open(velox.SQLite, sqlDB), types
}

// TestResolveNode: the one row of the ID, through each type's Read policy:
// a row it hides and a type whose policy denies count as none, which is
// NOT_FOUND; an ID of two types is ErrAmbiguousNodeID.
func TestResolveNode(t *testing.T) {
	db, types := nodeDB(t)
	ctx := context.Background()
	n, err := gqlgraph.ResolveNode(ctx, db, types, int64(2))
	if p, ok := n.(*person); err != nil || !ok || p.Name != "b" {
		t.Errorf("person 2: %#v, %v", n, err)
	}
	n, err = gqlgraph.ResolveNode(ctx, db, types, int64(20))
	if b, ok := n.(*badge); err != nil || !ok || b.ID != 20 {
		t.Errorf("badge 20: %#v, %v", n, err)
	}
	for _, id := range []int64{21, 30, 99} {
		n, err := gqlgraph.ResolveNode(ctx, db, types, id)
		if n != nil || !errors.Is(err, gqlgraph.ErrNodeNotFound) || codeOf(err) != veloxgql.CodeNotFound {
			t.Errorf("%d: %#v, %v; want NOT_FOUND ErrNodeNotFound", id, n, err)
		}
	}
	n, err = gqlgraph.ResolveNode(ctx, db, types, int64(3))
	if n != nil || !errors.Is(err, gqlgraph.ErrAmbiguousNodeID) || !strings.Contains(err.Error(), "Badge and a Person") {
		t.Errorf("3: %#v, %v; want ErrAmbiguousNodeID", n, err)
	}
	n, err = gqlgraph.LoadNode(ctx, db, types[0], int64(21))
	if n != nil || err != nil {
		t.Errorf("LoadNode of hidden badge 21: %#v, %v; want nil, nil", n, err)
	}
	if _, err := gqlgraph.LoadNode(ctx, db, &gqlgraph.Type{Name: "Pair", Table: untyped.TableOf[person]}, int64(1)); err == nil {
		t.Error("LoadNode of a type with no key: no error")
	}
}

// TestResolveNodes: one entry per ID, in order, nil for an ID no type has
// that the caller may read; an ambiguous ID fails the whole list.
func TestResolveNodes(t *testing.T) {
	db, types := nodeDB(t)
	ctx := context.Background()
	ns, err := gqlgraph.ResolveNodes(ctx, db, types, []int64{20, 99, 1, 21, 30})
	if err != nil || len(ns) != 5 || ns[1] != nil || ns[3] != nil || ns[4] != nil {
		t.Fatalf("got %#v, %v", ns, err)
	}
	if b, ok := ns[0].(*badge); !ok || b.ID != 20 {
		t.Errorf("[0]: %#v", ns[0])
	}
	if p, ok := ns[2].(*person); !ok || p.ID != 1 {
		t.Errorf("[2]: %#v", ns[2])
	}
	if ns, err := gqlgraph.ResolveNodes(ctx, db, types, []int64{1, 3}); ns != nil || !errors.Is(err, gqlgraph.ErrAmbiguousNodeID) {
		t.Errorf("with 3: %#v, %v; want ErrAmbiguousNodeID", ns, err)
	}
}
