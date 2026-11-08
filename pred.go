package velox

import (
	"github.com/syssam/velox/internal/engine"
)

// Order is an ordering by one column of entity E, made by a handle's Asc or
// Descriptor. It is the typed form of OrderTerm, as Pred is of Predicate.
type Order[E any] struct{ t OrderTerm }

// Column is any column handle of entity E, for Columns and Omit.
type Column[E any] interface{ column(*E) int32 }

// Setter is an assignment to a column of entity E, made by a column handle's
// Set, SetNull, Add or Sub, or an edge setter, made by a relation handle's
// Add, Remove or Clear.
type Setter[E any] struct{ a Assignment }

// Input is a value that builds setters of entity E, such as a generated
// GraphQL input, for UpdateQuery.SetInput and InsertQuery.SetInput.
type Input[E any] interface {
	Setters() ([]Setter[E], error)
}

// Column is the column s assigns, as Assignment.Column numbers it: two setters
// of one column have the same Column, which an UPDATE refuses to assign
// twice. A relation handle's Set assigns its foreign key's column; it is -1
// for any other edge setter.
func (s Setter[E]) Column[_ Lazy[E]]() int {
	if e := engine.EdgeOf(s.a); e != nil {
		return e.Column()
	}
	return s.a.Column
}

// And is true when every p is. And of nothing is true. An Allow among the ps
// adds nothing: And of only Allows is Allow, and And(Allow(), x) is x.
func And[E any](ps ...Pred[E]) Pred[E] {
	if !hasAllow(ps) {
		return Pred[E](engine.NewPredicate(engine.Group(engine.OpAnd, nodes(ps))))
	}
	kept := make([]engine.Node, 0, len(ps))
	for _, p := range ps {
		if engine.PredNode(Predicate(p)).Op != engine.OpAllow {
			kept = append(kept, engine.PredNode(Predicate(p)))
		}
	}
	switch len(kept) {
	case 0:
		return Pred[E](engine.NewPredicate(engine.Node{Op: engine.OpAllow}))
	case 1:
		return Pred[E](engine.NewPredicate(kept[0]))
	}
	return Pred[E](engine.NewPredicate(engine.Group(engine.OpAnd, kept)))
}

// Or is true when any p is. Or of nothing is false. Or of any Allow is Allow,
// unless another p is or holds the zero Pred, an invalid condition or a Has
// (whose relation resolves only when the statement runs): that Or keeps every
// p, so the statement still fails on a bad one, and matches every row.
func Or[E any](ps ...Pred[E]) Pred[E] {
	if hasAllow(ps) && allSound(ps) {
		return Pred[E](engine.NewPredicate(engine.Node{Op: engine.OpAllow}))
	}
	return Pred[E](engine.NewPredicate(engine.Group(engine.OpOr, nodes(ps))))
}

// Not is true when p is false. Not(Allow()) is true for no row.
func Not[E any](p Pred[E]) Pred[E] {
	return Pred[E](engine.NewPredicate(engine.Group(engine.OpNot, []engine.Node{engine.PredNode(Predicate(p))})))
}

// allSound reports whether no p is or holds the zero Pred, an invalid
// condition or an EXISTS, which an Or with an Allow would otherwise drop with
// its error. An EXISTS's relation is not resolved here: that reads metadata,
// which a Policy body may not wait on.
func allSound[E any](ps []Pred[E]) bool {
	for i := range ps {
		if !engine.Sound(engine.PredNode(Predicate(ps[i]))) {
			return false
		}
	}
	return true
}

func hasAllow[E any](ps []Pred[E]) bool {
	for i := range ps {
		if engine.PredNode(Predicate(ps[i])).Op == engine.OpAllow {
			return true
		}
	}
	return false
}

func nodes[E any](ps []Pred[E]) []engine.Node {
	out := make([]engine.Node, len(ps))
	for i := range ps {
		out[i] = engine.PredNode(Predicate(ps[i]))
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
