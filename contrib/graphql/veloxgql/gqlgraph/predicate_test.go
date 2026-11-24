package gqlgraph_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
	"github.com/syssam/velox/untyped"
)

// The where inputs of the tests have Predicate too, gqlgraph.Predicate of a
// descriptor, as velox-graphql writes it, beside their hand-written
// predicate. twin checks that the two give the same SQL, or the same error,
// for every input a test runs.

var personType = func() *gqlgraph.Type {
	c := gqlgraph.TableType[person]("Person")
	c.WhereFields = []gqlgraph.WhereField{
		{Name: "not", Kind: gqlgraph.WhereNot},
		{Name: "and", Kind: gqlgraph.WhereAnd},
		{Name: "or", Kind: gqlgraph.WhereOr},
		{Name: "id", Kind: gqlgraph.WhereID, Ops: gqlgraph.OpsEquality | gqlgraph.OpGT, Col: 0},
		{Name: "name", Ops: gqlgraph.OpsEquality | gqlgraph.OpGT | gqlgraph.OpContains | gqlgraph.OpHasPrefix | gqlgraph.OpHasSuffix, Col: 1},
		{Name: "label", Ops: gqlgraph.OpEQ | gqlgraph.OpNotIn, Col: 1},
		{Name: "age", Ops: gqlgraph.OpsEquality | gqlgraph.OpsOrder, Col: 2},
	}
	return c
}()

func (w *personWhere) Predicate(ctx context.Context) (velox.Predicate, error) {
	return gqlgraph.Predicate(ctx, personType, w)
}

var noteType = func() *gqlgraph.Type {
	c := gqlgraph.TableType[note]("Note")
	c.WhereFields = []gqlgraph.WhereField{
		{Name: "not", Kind: gqlgraph.WhereNot},
		{Name: "tags", Kind: gqlgraph.WhereArray, Ops: gqlgraph.OpHas | gqlgraph.OpHasSome | gqlgraph.OpHasEvery | gqlgraph.OpIsEmpty | gqlgraph.OpsNull, Col: 1},
	}
	return c
}()

func (w *noteWhere) Predicate(ctx context.Context) (velox.Predicate, error) {
	return gqlgraph.Predicate(ctx, noteType, w)
}

var vNoteType = func() *gqlgraph.Type {
	c := gqlgraph.TableType[note]("Note")
	c.LenientFilters = true
	c.WhereFields = []gqlgraph.WhereField{
		{Name: "tags", Kind: gqlgraph.WhereArray, Ops: gqlgraph.OpHasSome | gqlgraph.OpHasEvery | gqlgraph.OpIsEmpty | gqlgraph.OpsNull, Col: 1},
	}
	return c
}()

func (w *vNoteWhere) Predicate(ctx context.Context) (velox.Predicate, error) {
	return gqlgraph.Predicate(ctx, vNoteType, w)
}

// vIn is vWhere without its def, which a generated input has not: the
// struct Predicate reads, of the lenient type for M lenientM and of the
// strict one for M strictM.
type vIn[M any] struct {
	Not *vIn[M]
	And []*vIn[M]
	Or  []*vIn[M]

	ID      *int64
	IDIn    []int64
	IDNotIn []int64
}

type (
	lenientM struct{}
	strictM  struct{}
)

var vFields = []gqlgraph.WhereField{
	{Name: "not", Kind: gqlgraph.WhereNot},
	{Name: "and", Kind: gqlgraph.WhereAnd},
	{Name: "or", Kind: gqlgraph.WhereOr},
	{Name: "id", Kind: gqlgraph.WhereID, Ops: gqlgraph.OpEQ | gqlgraph.OpIn | gqlgraph.OpNotIn, Col: 0},
}

// vTypes are vIn's types: lenient, and strict.
var vTypes = func() (m [2]*gqlgraph.Type) {
	for i := range m {
		c := gqlgraph.TableType[person]("Person")
		c.LenientFilters = i == 0
		c.WhereFields = vFields
		m[i] = c
	}
	return m
}()

func (w *vIn[M]) Predicate(ctx context.Context) (velox.Predicate, error) {
	var m M
	if _, ok := any(m).(strictM); ok {
		return gqlgraph.Predicate(ctx, vTypes[1], w)
	}
	return gqlgraph.Predicate(ctx, vTypes[0], w)
}

// vInOf is w as a vIn tree; a nil w is nil.
func vInOf[M any](w *vWhere) *vIn[M] {
	if w == nil {
		return nil
	}
	list := func(ws []*vWhere) []*vIn[M] {
		if ws == nil {
			return nil
		}
		out := make([]*vIn[M], len(ws))
		for i, x := range ws {
			out[i] = vInOf[M](x)
		}
		return out
	}
	return &vIn[M]{Not: vInOf[M](w.Not), And: list(w.And), Or: list(w.Or), ID: w.ID, IDIn: w.IDIn, IDNotIn: w.IDNotIn}
}

// Predicate of vWhere is vIn's: its fields hold the same, under lenient
// semantics unless w.def.
func (w *vWhere) Predicate(ctx context.Context) (velox.Predicate, error) {
	if w != nil && w.def {
		return vInOf[strictM](w).Predicate(ctx)
	}
	return vInOf[lenientM](w).Predicate(ctx)
}

// twin is hand(ctx), after a check that refl(ctx) gives the same: the same
// SQL on E's table, or an error with the same text.
func twin[E any](t *testing.T, ctx context.Context, hand, refl func(context.Context) (velox.Predicate, error)) (velox.Predicate, error) {
	t.Helper()
	p, err := hand(ctx)
	q, qerr := refl(ctx)
	if (err == nil) != (qerr == nil) || err != nil && err.Error() != qerr.Error() {
		t.Errorf("Predicate's error %v, predicate's %v", qerr, err)
		return p, err
	}
	if err != nil {
		if codeOf(qerr) != codeOf(err) {
			t.Errorf("Predicate's error has code %q, predicate's %q", codeOf(qerr), codeOf(err))
		}
		return p, err
	}
	a, b := sqlOf[E](t, p), sqlOf[E](t, q)
	if a != b {
		t.Errorf("Predicate's SQL\n%s\npredicate's\n%s", b, a)
	}
	return p, err
}

// sqlOf is the SQL and arguments of a read of E filtered by p.
func sqlOf[E any](t *testing.T, p velox.Predicate) string {
	t.Helper()
	db := velox.Open(velox.SQLite, nil)
	text, args, err := untyped.Query(db, untyped.TableOf[E]()).Where(p).AllowUnbounded().SQL(context.Background())
	if err != nil {
		return "error: " + err.Error()
	}
	return fmt.Sprintf("%s %v", text, args)
}

// TestPredicateRefusesAnotherStruct: a descriptor that does not describe
// the input's struct is a bug, refused, not read past the struct's end.
func TestPredicateRefusesAnotherStruct(t *testing.T) {
	_, err := gqlgraph.Predicate(context.Background(), noteType, &personWhere{})
	if err == nil || !strings.Contains(err.Error(), "is not the struct WhereFields describes") {
		t.Errorf("got %v", err)
	}
	// A nil input sets nothing.
	if _, err := gqlgraph.Predicate(context.Background(), personType, (*personWhere)(nil)); err != nil {
		t.Errorf("nil: %v", err)
	}
}

// TestWhereOpBits: gqlgraph's WhereOp is package graphql's, bit for bit, so
// that velox-graphql writes a set of either as the other.
func TestWhereOpBits(t *testing.T) {
	for _, c := range []struct {
		a gqlgraph.WhereOp
		b graphql.WhereOp
	}{
		{gqlgraph.OpEQ, graphql.OpEQ}, {gqlgraph.OpNEQ, graphql.OpNEQ}, {gqlgraph.OpIn, graphql.OpIn}, {gqlgraph.OpNotIn, graphql.OpNotIn},
		{gqlgraph.OpGT, graphql.OpGT}, {gqlgraph.OpGTE, graphql.OpGTE}, {gqlgraph.OpLT, graphql.OpLT}, {gqlgraph.OpLTE, graphql.OpLTE},
		{gqlgraph.OpContains, graphql.OpContains}, {gqlgraph.OpHasPrefix, graphql.OpHasPrefix}, {gqlgraph.OpHasSuffix, graphql.OpHasSuffix},
		{gqlgraph.OpIsNil, graphql.OpIsNil}, {gqlgraph.OpNotNil, graphql.OpNotNil},
		{gqlgraph.OpEqualFold, graphql.OpEqualFold}, {gqlgraph.OpContainsFold, graphql.OpContainsFold},
		{gqlgraph.OpHas, graphql.OpHas}, {gqlgraph.OpHasSome, graphql.OpHasSome}, {gqlgraph.OpHasEvery, graphql.OpHasEvery}, {gqlgraph.OpIsEmpty, graphql.OpIsEmpty},
		{gqlgraph.OpsEquality, graphql.OpsEquality},
	} {
		if uint32(c.a) != uint32(c.b) {
			t.Errorf("%#x is %#x in package graphql", uint32(c.a), uint32(c.b))
		}
	}
}

// ownerIn and childIn are where inputs of owner and child with every kind of
// WhereField that the other tests' inputs lack: has<Edge>, has<Edge>With, a
// foreign key's IDs, and the case-folding and null predicates.
type ownerIn struct {
	HasChildren     *bool
	HasChildrenWith []*childIn
}

type childIn struct {
	Title             *string
	TitleEqualFold    *string
	TitleContainsFold *string

	OwnerID       *int64
	OwnerIDIn     []int64
	OwnerIDIsNil  *bool
	OwnerIDNotNil *bool

	EditorIDIsNil  *bool
	EditorIDNotNil *bool
}

var ownerInType = func() *gqlgraph.Type {
	c := gqlgraph.TableType[owner]("Owner")
	c.WhereFields = []gqlgraph.WhereField{
		{Name: "hasChildren", Kind: gqlgraph.WhereHas, Edge: int(owners.Children.I), Target: untyped.TableOf[child]},
		{Name: "hasChildrenWith", Kind: gqlgraph.WhereHasWith, Edge: int(owners.Children.I), Target: untyped.TableOf[child]},
	}
	return c
}()

var childInType = func() *gqlgraph.Type {
	c := gqlgraph.TableType[child]("Child")
	c.WhereFields = []gqlgraph.WhereField{
		{Name: "title", Ops: gqlgraph.OpEQ | gqlgraph.OpEqualFold | gqlgraph.OpContainsFold, Col: int(children.Title.I)},
		{Name: "ownerID", Kind: gqlgraph.WhereEdgeID, Ops: gqlgraph.OpEQ | gqlgraph.OpIn | gqlgraph.OpsNull, Col: int(children.OwnerID.I), Edge: int(children.Owner.I), Target: untyped.TableOf[owner]},
		{Name: "editorID", Ops: gqlgraph.OpsNull, Col: int(children.EditorID.I)},
	}
	return c
}()

func (w *ownerIn) Predicate(ctx context.Context) (velox.Predicate, error) {
	return gqlgraph.Predicate(ctx, ownerInType, w)
}

func (w *childIn) Predicate(ctx context.Context) (velox.Predicate, error) {
	return gqlgraph.Predicate(ctx, childInType, w)
}

// The same inputs, by hand, with the helpers.
func (w *ownerIn) predicate(ctx context.Context) (velox.Predicate, error) {
	f := gqlgraph.NewFilter(ctx, ownerInType)
	if w == nil {
		return f.Pred()
	}
	gqlgraph.Has(f, "hasChildren", w.HasChildren, int(owners.Children.I), untyped.TableOf[child]())
	gqlgraph.HasWith(f, "hasChildrenWith", w.HasChildrenWith, (*childIn).predicate, int(owners.Children.I), untyped.TableOf[child]())
	return f.Pred()
}

func (w *childIn) predicate(ctx context.Context) (velox.Predicate, error) {
	f := gqlgraph.NewFilter(ctx, childInType)
	if w == nil {
		return f.Pred()
	}
	gqlgraph.Field(f, "title", int(children.Title.I), gqlgraph.Ops[string]{EQ: w.Title, EqualFold: w.TitleEqualFold, ContainsFold: w.TitleContainsFold})
	gqlgraph.EdgeIDField(f, "ownerID", int(children.OwnerID.I), int(children.Owner.I), gqlgraph.TableType[owner]("Owner"), gqlgraph.Ops[int64]{
		EQ: w.OwnerID, In: w.OwnerIDIn, IsNil: w.OwnerIDIsNil, NotNil: w.OwnerIDNotNil,
	})
	gqlgraph.Field(f, "editorID", int(children.EditorID.I), gqlgraph.Ops[int64]{IsNil: w.EditorIDIsNil, NotNil: w.EditorIDNotNil})
	return f.Pred()
}

// TestPredicateEdges: has<Edge>, has<Edge>With, a foreign key's IDs and the
// folding and null predicates give the SQL the helpers give.
func TestPredicateEdges(t *testing.T) {
	ctx := context.Background()
	tru, fls := new(true), new(false)
	for _, w := range []*ownerIn{
		nil, {},
		{HasChildren: tru}, {HasChildren: fls},
		{HasChildrenWith: []*childIn{}},
		{HasChildrenWith: []*childIn{{Title: new("a")}, nil}},
		{HasChildrenWith: []*childIn{{TitleEqualFold: new("A"), TitleContainsFold: new("b")}}},
		{HasChildrenWith: []*childIn{{OwnerID: new(int64(1)), OwnerIDIn: []int64{1, 2}}}},
		{HasChildrenWith: []*childIn{{OwnerIDIsNil: tru, EditorIDNotNil: fls}}},
		{HasChildrenWith: []*childIn{{OwnerIDNotNil: fls, EditorIDIsNil: tru}}},
		{HasChildren: tru, HasChildrenWith: []*childIn{{Title: new("x")}, {OwnerIDIn: []int64{}}}},
	} {
		_, _ = twin[owner](t, ctx, w.predicate, w.Predicate)
	}
	for _, w := range []*childIn{
		{OwnerID: new(int64(1)), OwnerIDIsNil: fls, OwnerIDNotNil: tru, EditorIDIsNil: fls},
		{TitleEqualFold: new("Q"), OwnerIDIn: []int64{3}},
	} {
		_, _ = twin[child](t, ctx, w.predicate, w.Predicate)
	}
}
