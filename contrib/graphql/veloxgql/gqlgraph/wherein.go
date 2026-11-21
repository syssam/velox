package gqlgraph

import (
	"context"
	"fmt"
	"math/bits"
	"reflect"

	"github.com/syssam/velox"
	"github.com/syssam/velox/untyped"
)

// Input is a generated where input: its Predicate is its condition,
// untyped, and its Pred the same condition typed. Predicate calls that of
// an input nested in another, in a not, an and, an or or a has…With.
type Input interface {
	Predicate(ctx context.Context) (velox.Predicate, error)
}

// WhereOp is a set of the predicates a where input offers on one field. Its
// bits are those of package graphql's WhereOp, and so is the order in which
// the input's struct holds the predicates' fields.
type WhereOp uint32

// The predicates, in the order of their fields in a where input.
const (
	OpEQ WhereOp = 1 << iota
	OpNEQ
	OpIn
	OpNotIn
	OpGT
	OpGTE
	OpLT
	OpLTE
	OpContains
	OpHasPrefix
	OpHasSuffix
	OpIsNil
	OpNotNil
	OpEqualFold
	OpContainsFold
	OpHas
	OpHasSome
	OpHasEvery
	OpIsEmpty

	// OpsEquality is EQ, NEQ, In and NotIn; OpsOrder GT, GTE, LT and LTE;
	// OpsText Contains, HasPrefix, HasSuffix, EqualFold and ContainsFold;
	// OpsNull IsNil and NotNil.
	OpsEquality = OpEQ | OpNEQ | OpIn | OpNotIn
	OpsOrder    = OpGT | OpGTE | OpLT | OpLTE
	OpsText     = OpContains | OpHasPrefix | OpHasSuffix | OpEqualFold | OpContainsFold
	OpsNull     = OpIsNil | OpNotNil
)

// fieldOrder is the order of the predicates' fields in a where input, as
// velox-graphql writes them: that of Field's, ArrayField's and IDField's
// predicates.
var fieldOrder = [...]WhereOp{
	OpEQ, OpNEQ, OpIn, OpNotIn, OpGT, OpGTE, OpLT, OpLTE,
	OpContains, OpHasPrefix, OpHasSuffix, OpEqualFold, OpContainsFold,
	OpHas, OpHasSome, OpHasEvery, OpIsEmpty, OpIsNil, OpNotNil,
}

// WhereKind is what a WhereField is about.
type WhereKind uint8

// The kinds of WhereField.
const (
	// WhereColumn is the predicates on a column, Field's.
	WhereColumn WhereKind = iota
	// WhereID is the predicates on the type's key, IDField's.
	WhereID
	// WhereEdgeID is the predicates on a foreign key, by the IDs of the
	// edge's nodes, EdgeIDField's.
	WhereEdgeID
	// WhereArray is the predicates on a JSON array column, ArrayField's.
	WhereArray
	// WhereNot, WhereAnd and WhereOr are the input's not, and and or.
	WhereNot
	WhereAnd
	WhereOr
	// WhereHas and WhereHasWith are has<Edge> and has<Edge>With.
	WhereHas
	WhereHasWith
)

// WhereField describes to Predicate the fields of a where input that one helper
// takes: a not, an and, an or or an edge's field alone, or the run of fields
// that hold the predicates on one column, one field for each of Ops, in the
// order of their bits.
type WhereField struct {
	// Name is the input field's GraphQL name, or for predicates the column's
	// field's, which their names begin with ("title" for "titleNEQ").
	Name string
	Kind WhereKind
	// Ops is the predicates of a run, for WhereColumn, WhereID, WhereEdgeID
	// and WhereArray.
	Ops WhereOp
	// Col is the column's index, and Edge the relation's, as velox-gen
	// numbers them; Target is the relation's target, for WhereEdgeID,
	// WhereHas and WhereHasWith.
	Col, Edge int
	Target    func() untyped.Table
}

// fields is the number of the input's fields that d describes.
func (d *WhereField) fields() int {
	switch d.Kind {
	case WhereColumn, WhereID, WhereEdgeID, WhereArray:
		return bits.OnesCount32(uint32(d.Ops))
	}
	return 1
}

// Predicate is the condition of w, a generated where input of c's type, a
// pointer to its struct: its Predicate. For generated code. It reads w's
// fields as c.WhereFields describes them, and adds each that is set with
// the helper of its kind, as a hand-written predicate method would (see
// Filter): Field, IDField, EdgeIDField, ArrayField, Not, And, Or, Has and
// HasWith, with the same predicates, errors and limits. A nested input's
// condition is its Predicate. A nil w is an input that sets nothing.
func Predicate(ctx context.Context, c *Type, w any) (velox.Predicate, error) {
	f := NewFilter(ctx, c)
	v := reflect.ValueOf(w)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return f.Pred()
	}
	s := v.Elem()
	n := 0
	for i := range c.WhereFields {
		n += c.WhereFields[i].fields()
	}
	if s.Kind() != reflect.Struct || s.NumField() != n {
		return velox.Predicate{}, fmt.Errorf("veloxgql: %sWhereInput: %s is not the struct WhereFields describes, which is a bug", c.Name, s.Type())
	}
	i := 0
	for k := range c.WhereFields {
		d := &c.WhereFields[k]
		switch d.Kind {
		case WhereNot:
			Not(f, d.Name, input(s.Field(i)), cond)
		case WhereAnd:
			And(f, d.Name, inputs(s.Field(i)), cond)
		case WhereOr:
			Or(f, d.Name, inputs(s.Field(i)), cond)
		case WhereHas:
			Has(f, d.Name, s.Field(i).Interface().(*bool), d.Edge, d.Target())
		case WhereHasWith:
			HasWith(f, d.Name, inputs(s.Field(i)), cond, d.Edge, d.Target())
		case WhereArray:
			ArrayField(f, d.Name, d.Col, arrayOps(s, i, d.Ops))
		case WhereID:
			IDField(f, d.Name, d.Col, ops(s, i, d.Ops))
		case WhereEdgeID:
			edgeIDs(f, d.Name, d.Col, d.Edge, d.Target(), ops(s, i, d.Ops))
		default:
			Field(f, d.Name, d.Col, ops(s, i, d.Ops))
		}
		if f.err != nil {
			break
		}
		i += d.fields()
	}
	return f.Pred()
}

// cond is the condition of a nested input, its Predicate.
func cond(w any, ctx context.Context) (velox.Predicate, error) { //nolint:revive // a Cond, whose context follows the input as in a method expression
	return w.(Input).Predicate(ctx)
}

// input is the nested input in x, a pointer field, or nil.
func input(x reflect.Value) any {
	if x.IsNil() {
		return nil
	}
	return x.Interface()
}

// inputs is the nested inputs in x, a slice field: nil for a nil slice, and
// nil for each nil entry.
func inputs(x reflect.Value) []any {
	if x.IsNil() {
		return nil
	}
	ws := make([]any, x.Len())
	for i := range ws {
		ws[i] = input(x.Index(i))
	}
	return ws
}

// ops is the Ops of the run of fields of s from i that hold the predicates
// set: each value as an any.
func ops(s reflect.Value, i int, set WhereOp) Ops[any] {
	var o Ops[any]
	for _, op := range fieldOrder {
		if set&op == 0 {
			continue
		}
		x := s.Field(i)
		i++
		switch op {
		case OpEQ:
			o.EQ = one1(x)
		case OpNEQ:
			o.NEQ = one1(x)
		case OpIn:
			o.In = list1(x)
		case OpNotIn:
			o.NotIn = list1(x)
		case OpGT:
			o.GT = one1(x)
		case OpGTE:
			o.GTE = one1(x)
		case OpLT:
			o.LT = one1(x)
		case OpLTE:
			o.LTE = one1(x)
		case OpContains:
			o.Contains = one1(x)
		case OpHasPrefix:
			o.HasPrefix = one1(x)
		case OpHasSuffix:
			o.HasSuffix = one1(x)
		case OpEqualFold:
			o.EqualFold = one1(x)
		case OpContainsFold:
			o.ContainsFold = one1(x)
		case OpIsNil:
			o.IsNil = x.Interface().(*bool)
		case OpNotNil:
			o.NotNil = x.Interface().(*bool)
		}
	}
	return o
}

// arrayOps is ops of a JSON array's predicates.
func arrayOps(s reflect.Value, i int, set WhereOp) ArrayOps[any] {
	var o ArrayOps[any]
	for _, op := range fieldOrder {
		if set&op == 0 {
			continue
		}
		x := s.Field(i)
		i++
		switch op {
		case OpHas:
			o.Has = one1(x)
		case OpHasSome:
			o.HasSome = list1(x)
		case OpHasEvery:
			o.HasEvery = list1(x)
		case OpIsEmpty:
			o.IsEmpty = x.Interface().(*bool)
		case OpIsNil:
			o.IsNil = x.Interface().(*bool)
		case OpNotNil:
			o.NotNil = x.Interface().(*bool)
		}
	}
	return o
}

// one1 is the value x, a pointer field, points at, or nil.
func one1(x reflect.Value) *any {
	if x.IsNil() {
		return nil
	}
	v := x.Elem().Interface()
	return &v
}

// list1 is the values of x, a slice field, or nil for a nil slice.
func list1(x reflect.Value) []any {
	if x.IsNil() {
		return nil
	}
	vs := make([]any, x.Len())
	for i := range vs {
		vs[i] = x.Index(i).Interface()
	}
	return vs
}
