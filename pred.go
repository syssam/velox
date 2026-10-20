package velox

import (
	"slices"
	"strings"
	"sync"
)

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
	opAllow   // matches every row and renders to nothing in a policy
	opExists  // col is the relation index, v its relRef, or its link once resolved, kids the target's conditions
	opInvalid // v is the error
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

// Setter is an assignment to a column of entity E, made by a handle's Set,
// SetNull, Add or Sub.
type Setter[E any] struct{ a Assignment }

// ColumnEQ returns the condition column = v, for hooks that add conditions;
// column is an index into the statement's EntityInfo.Columns.
func ColumnEQ(column int, v any) Predicate {
	return Predicate{node{op: opEQ, col: int32(column), v: v}}
}

// Allow is the condition that matches every row: what a policy's Read or
// Write returns for a caller it does not restrict. In a policy it adds nothing
// to the SQL; in a Where it is 1 = 1, which an Update or Delete's guard does
// not count as a condition. A policy that returns the zero Pred[E] instead
// fails the statement, so that no restriction is said, never implied.
//
// Allow composes as its meaning says: And drops it (And(Allow(), x) is x),
// Or(Allow(), x) matches every row (see Or), and Not(Allow()) matches no row.
func Allow[E any]() Pred[E] { return Pred[E]{node{op: opAllow}} }

// And is true when every p is. And of nothing is true. An Allow among the ps
// adds nothing: And of only Allows is Allow, and And(Allow(), x) is x.
func And[E any](ps ...Pred[E]) Pred[E] {
	if !hasAllow(ps) {
		return Pred[E]{node{op: opAnd, kids: nodes(ps)}}
	}
	kept := make([]node, 0, len(ps))
	for _, p := range ps {
		if p.n.op != opAllow {
			kept = append(kept, p.n)
		}
	}
	switch len(kept) {
	case 0:
		return Allow[E]()
	case 1:
		return Pred[E]{kept[0]}
	}
	return Pred[E]{node{op: opAnd, kids: kept}}
}

// Or is true when any p is. Or of nothing is false. Or of any Allow is Allow,
// unless another p is or holds the zero Pred, an invalid condition or a Has
// (whose relation resolves only when the statement runs): that Or keeps every
// p, so the statement still fails on a bad one, and matches every row.
func Or[E any](ps ...Pred[E]) Pred[E] {
	if hasAllow(ps) && allSound(ps) {
		return Allow[E]()
	}
	return Pred[E]{node{op: opOr, kids: nodes(ps)}}
}

// Not is true when p is false. Not(Allow()) is true for no row.
func Not[E any](p Pred[E]) Pred[E] { return Pred[E]{node{op: opNot, kids: []node{p.n}}} }

// allSound reports whether no p is or holds the zero Pred, an invalid
// condition or an EXISTS, which an Or with an Allow would otherwise drop with
// its error. An EXISTS's relation is not resolved here: that reads metadata,
// which a Policy body may not wait on.
func allSound[E any](ps []Pred[E]) bool {
	for i := range ps {
		if !sound(ps[i].n) {
			return false
		}
	}
	return true
}

func sound(n node) bool {
	switch n.op {
	case 0, opInvalid, opExists:
		return false
	}
	for _, k := range n.kids {
		if !sound(k) {
			return false
		}
	}
	return true
}

func hasAllow[E any](ps []Pred[E]) bool {
	for i := range ps {
		if ps[i].n.op == opAllow {
			return true
		}
	}
	return false
}

// predErr returns the error a condition carries: a relation that could not be
// resolved when the condition was made.
func predErr(n node) error {
	if n.op == opInvalid {
		return n.v.(error)
	}
	if n.op == opExists {
		// An unresolved relRef is valid: the statement resolves it, and
		// fails with the relation's error. Its kids were checked when
		// HasWhere made it.
		return nil
	}
	for _, k := range n.kids {
		if err := predErr(k); err != nil {
			return err
		}
	}
	return nil
}

// existsLink returns the link of the opExists node n: its own once resolved,
// else the relRef's, which resolves the relation.
func existsLink(n node) (link, error) {
	if r, ok := n.v.(relRef); ok {
		return r.link()
	}
	return n.v.(link), nil
}

// resolveNode returns n with every opExists in it resolved, and whether that
// changed anything. It never edits n: a changed node is a copy, with a new
// kids slice when a kid changed, and unchanged subtrees are shared with n,
// which may be in a Pred other goroutines hold (scoper.node's rule).
func resolveNode(n node) (node, bool, error) {
	switch n.op {
	case opAnd, opOr, opNot, opExists:
	default:
		return n, false, nil
	}
	var kids []node // nil until a kid changes
	for i, k := range n.kids {
		nk, changed, err := resolveNode(k)
		if err != nil {
			return n, false, err
		}
		if !changed {
			continue
		}
		if kids == nil {
			kids = slices.Clone(n.kids)
		}
		kids[i] = nk
	}
	changed := kids != nil
	if r, ok := n.v.(relRef); ok && n.op == opExists {
		l, err := r.link()
		if err != nil {
			return n, false, err
		}
		n.v, changed = l, true // n is a copy
	}
	if kids != nil {
		n.kids = kids
	}
	return n, changed, nil
}

// resolveErr returns the error resolving n would fail with, without
// allocating.
func resolveErr(n node) error {
	if r, ok := n.v.(relRef); ok && n.op == opExists {
		if _, err := r.link(); err != nil {
			return err
		}
	}
	for _, k := range n.kids {
		if err := resolveErr(k); err != nil {
			return err
		}
	}
	return nil
}

// resolvePreds returns ps with every relation resolved, on a copy when any
// was not: ps may be the query's, shared with other runs.
func resolvePreds(ps []Predicate) ([]Predicate, error) {
	out := ps
	for i, p := range ps {
		n, changed, err := resolveNode(p.n)
		if err != nil {
			return nil, err
		}
		if !changed {
			continue
		}
		if &out[0] == &ps[0] {
			out = slices.Clone(ps)
		}
		out[i] = Predicate{n}
	}
	return out, nil
}

// resolve resolves the relations in st's conditions, before anything else
// reads them: the guard, the Rewrite hooks, the scope and rendering see only
// links. A relation that cannot be resolved fails the statement.
func (st *Statement) resolve() error {
	where, err := resolvePreds(st.Where)
	if err != nil {
		return err
	}
	st.Where = where
	if cf := st.Conflict; cf != nil && len(cf.Where) > 0 {
		cw, err := resolvePreds(cf.Where)
		if err != nil {
			return err
		}
		if &cw[0] != &cf.Where[0] {
			c := *cf // the caller's clause may be shared with other runs
			c.Where = cw
			st.Conflict = &c
		}
	}
	return nil
}

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

// likeEscaper is built on first use, not at program start.
var likeEscaper = sync.OnceValue(func() *strings.Replacer {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
})

// escapeLike makes s match itself in a LIKE pattern whose escape is \.
func escapeLike(s string) string { return likeEscaper().Replace(s) }
