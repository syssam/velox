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
	c.Veloxe = true
	c.WhereFields = []gqlgraph.WhereField{
		{Name: "tags", Kind: gqlgraph.WhereArray, Ops: gqlgraph.OpHasSome | gqlgraph.OpHasEvery | gqlgraph.OpIsEmpty | gqlgraph.OpsNull, Col: 1},
	}
	return c
}()

func (w *vNoteWhere) Predicate(ctx context.Context) (velox.Predicate, error) {
	return gqlgraph.Predicate(ctx, vNoteType, w)
}

// vIn is vWhere without its def, which a generated input has not: the
// struct Predicate reads, of veloxe's type for M veloxeM and of velox's for
// M veloxM.
type vIn[M any] struct {
	Not *vIn[M]
	And []*vIn[M]
	Or  []*vIn[M]

	ID      *int64
	IDIn    []int64
	IDNotIn []int64
}

type (
	veloxeM struct{}
	veloxM  struct{}
)

var vFields = []gqlgraph.WhereField{
	{Name: "not", Kind: gqlgraph.WhereNot},
	{Name: "and", Kind: gqlgraph.WhereAnd},
	{Name: "or", Kind: gqlgraph.WhereOr},
	{Name: "id", Kind: gqlgraph.WhereID, Ops: gqlgraph.OpEQ | gqlgraph.OpIn | gqlgraph.OpNotIn, Col: 0},
}

// vTypes are vIn's types: under veloxe's semantics, and velox's.
var vTypes = func() (m [2]*gqlgraph.Type) {
	for i := range m {
		c := gqlgraph.TableType[person]("Person")
		c.Veloxe = i == 0
		c.WhereFields = vFields
		m[i] = c
	}
	return m
}()

func (w *vIn[M]) Predicate(ctx context.Context) (velox.Predicate, error) {
	var m M
	if _, ok := any(m).(veloxM); ok {
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

// Predicate of vWhere is vIn's: its fields hold the same, under veloxe's
// semantics unless w.def.
func (w *vWhere) Predicate(ctx context.Context) (velox.Predicate, error) {
	if w != nil && w.def {
		return vInOf[veloxM](w).Predicate(ctx)
	}
	return vInOf[veloxeM](w).Predicate(ctx)
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
