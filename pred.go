package velox

import "strings"

type op uint8

const (
	opEQ op = iota + 1
	opNEQ
	opIn
	opNotIn
	opGT
	opGTE
	opLT
	opLTE
	opBetween
	opLike
	opFold
	opIsNull
	opNotNull
	opAnd
	opOr
	opNot
)

// node is a condition, typed by nothing: the generic API wraps it.
type node struct {
	op   op
	col  int32
	v, w any
	kids []node
}

// Pred is a condition on entity E, made by a column handle or by And, Or and
// Not.
type Pred[E any] struct{ n node }

// Predicate is a condition in a Statement: the form hooks see and add.
type Predicate struct{ n node }

// Order is an ordering by one column of entity E, made by a handle's Asc or
// Desc.
type Order[E any] struct{ t OrderTerm }

// OrderTerm is an ordering by one column of a Statement's entity.
type OrderTerm struct {
	Column int // index into EntityInfo.Columns
	Desc   bool
}

// Column is any column handle of entity E, for Columns and Omit.
type Column[E any] interface{ column(*E) int32 }

// ColumnEQ returns the condition column = v, for hooks that add conditions;
// column is an index into the statement's EntityInfo.Columns.
func ColumnEQ(column int, v any) Predicate {
	return Predicate{node{op: opEQ, col: int32(column), v: v}}
}

// And is true when every p is. And of nothing is true.
func And[E any](ps ...Pred[E]) Pred[E] { return Pred[E]{node{op: opAnd, kids: nodes(ps)}} }

// Or is true when any p is. Or of nothing is false.
func Or[E any](ps ...Pred[E]) Pred[E] { return Pred[E]{node{op: opOr, kids: nodes(ps)}} }

// Not is true when p is false.
func Not[E any](p Pred[E]) Pred[E] { return Pred[E]{node{op: opNot, kids: []node{p.n}}} }

func nodes[E any](ps []Pred[E]) []node {
	out := make([]node, len(ps))
	for i := range ps {
		out[i] = ps[i].n
	}
	return out
}

func list[V any](vs []V) []any {
	out := make([]any, len(vs))
	for i := range vs {
		out[i] = vs[i]
	}
	return out
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// escapeLike makes s match itself in a LIKE pattern whose escape is \.
func escapeLike(s string) string { return likeEscaper.Replace(s) }
