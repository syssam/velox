package engine

import (
	"slices"
	"strings"
	"sync"
)

type op uint8

// OpEQ and the rest are the operators of a condition's Node.
const (
	opZero op = iota // the zero Pred[E]{}: no condition was given
	OpEQ
	OpNEQ
	OpIn
	OpNotIn
	OpGT
	OpGTE
	OpLT
	OpLTE
	OpBetween
	OpLike
	OpFold
	OpLikeFold // LOWER(col) LIKE LOWER(v) ESCAPE '!'
	OpIsNull
	OpNotNull
	OpAnd
	OpOr
	OpNot
	OpAllow   // matches every row and renders to nothing in a policy
	OpExists  // col is the relation index (-1 for a ColRef), v its RelRef or ColRef, or its link once resolved, kids the target's conditions
	OpInvalid // v is the error
	OpThrough // col is the target's column, v the join *EntityInfo, w the join's column equal to it, kids the join's conditions
	OpJoined  // col is the relation index, kids the conditions on the target of the join Query.Join made for it
	// opNamed is a condition on a column named by its name, not yet resolved:
	// col's low byte is its PredOp, v its value, and w and the rest of col
	// the name (nameData). The statement's preparation resolves it against
	// the entity, to the leaf Table.Pred makes.
	opNamed
	// opNamedEdge is an EXISTS on a relation named by its name, not yet
	// resolved: w and col are the name, as opNamed's, kids the conditions on
	// its target, by name.
	opNamedEdge
	// opNamedSub is an EXISTS on another entity, correlated by two columns,
	// all by name and not yet resolved: v is its *namedSub, kids the
	// conditions on the target, by name.
	opNamedSub
	// OpJSON is a condition on a value inside a JSON column: col is the
	// column, v its JSONPath, and its one kid the operator and values, whose
	// own column is ignored. JSONNode makes it.
	OpJSON
	// OpExpr is a condition written as text: v is its *exprData, ExprNode
	// makes it.
	OpExpr
)

// Node is a condition, typed by nothing: the generic API wraps it.
type Node struct {
	Op     op
	Deep   bool // an OpExists or OpThrough is among the kids, at any depth; set by group
	ByName bool // a condition by name is among the kids, at any depth; set by group
	Col    int32
	V, W   any
	Kids   []Node
}

// JoinedNode is the conjunction of kids, conditions on the target of relation
// rel, which the statement must join: all must hold on the joined row.
func JoinedNode(rel int32, kids []Node) Node {
	n := Group(OpJoined, kids)
	n.Col = rel
	return n
}

// Group is the And, Or or Not (o) of kids. It marks the node deep when an
// EXISTS or a Through is among the kids, at any depth: the scope and ResolveNode
// skip a node that is not. And, Or, Not and every And or Or velox makes are made by
// it, and node is unexported, so a Rewrite hook's conditions are too.
func Group(o op, kids []Node) Node {
	n := Node{Op: o, Kids: kids}
	for _, k := range kids {
		if k.Op == OpExists || k.Op == OpThrough || k.Deep {
			n.Deep = true
		}
		if named(k) {
			n.ByName = true
		}
	}
	return n
}

// named reports whether n is or holds a condition by name, which the
// statement resolves against its entity.
func named(n Node) bool {
	return n.ByName || n.Op == opNamed || n.Op == opNamedEdge || n.Op == opNamedSub
}

// AnyEntity is the entity of a Predicate: none, since an untyped condition
// names no entity's type.
type AnyEntity struct{}

// Predicate is an untyped condition: the form a Statement holds, and the one
// a policy, an interceptor and a hook use. It is the instance of Pred of no
// entity, so And, Or and Not combine it as they combine a Pred of one
// entity. Its zero value fails the statement that holds it, as the zero Pred
// does.
type Predicate = Pred[AnyEntity]

// ColumnEQ returns the condition column = v, for hooks that add conditions;
// column is an index into the statement's EntityInfo.Columns.
func ColumnEQ(column int, v any) Predicate {
	return Predicate{Node{Op: OpEQ, Col: int32(column), V: v}}
}

// Sound reports whether n and its children hold no zero or invalid operator and
// no EXISTS: a condition that a rewrite hook may build with the public API.
func Sound(n Node) bool {
	switch n.Op {
	case opZero, OpInvalid, OpExists, OpJoined, opNamed, opNamedEdge, opNamedSub:
		return false
	}
	for _, k := range n.Kids {
		if !Sound(k) {
			return false
		}
	}
	return true
}

// PredErr returns the error a condition carries: a relation that could not be
// resolved when the condition was made.
func PredErr(n Node) error {
	if n.Op == OpInvalid {
		return n.V.(error)
	}
	if n.Op == OpExists {
		// An unresolved RelRef or ColRef is valid: the statement resolves it, and
		// fails with the relation's error. Its kids were checked when
		// HasWith made it.
		return nil
	}
	for _, k := range n.Kids {
		if err := PredErr(k); err != nil {
			return err
		}
	}
	return nil
}

// existsLink returns the link of the OpExists node n: its own once resolved,
// else the RelRef's or ColRef's, which resolves it.
func existsLink(n Node) (Link, error) {
	if r, ok := n.V.(linkRef); ok {
		return r.Link()
	}
	return n.V.(Link), nil
}

// ResolveNode returns n with every OpExists in it resolved, and whether that
// changed anything. It never edits n: a changed node is a copy, with a new
// kids slice when a kid changed, and unchanged subtrees are shared with n,
// which may be in a Pred other goroutines hold (scoper.node's rule).
func ResolveNode(n Node) (Node, bool, error) {
	switch n.Op {
	case OpAnd, OpOr, OpNot, OpJoined:
		if !n.Deep {
			return n, false, nil // no EXISTS in it
		}
	case OpExists:
	default: // an OpThrough is resolved already: Of resolves its relation
		return n, false, nil
	}
	var kids []Node // nil until a kid changes
	for i, k := range n.Kids {
		nk, changed, err := ResolveNode(k)
		if err != nil {
			return n, false, err
		}
		if !changed {
			continue
		}
		if kids == nil {
			kids = slices.Clone(n.Kids)
		}
		kids[i] = nk
	}
	changed := kids != nil
	if r, ok := n.V.(linkRef); ok && n.Op == OpExists {
		l, err := r.Link()
		if err != nil {
			return n, false, err
		}
		n.V, changed = l, true // n is a copy
	}
	if kids != nil {
		n.Kids = kids
	}
	return n, changed, nil
}

// ResolveErr returns the error resolving n would fail with, without
// allocating.
func ResolveErr(n Node) error {
	if r, ok := n.V.(linkRef); ok && n.Op == OpExists {
		if _, err := r.Link(); err != nil {
			return err
		}
	}
	for _, k := range n.Kids {
		if err := ResolveErr(k); err != nil {
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
		n, changed, err := ResolveNode(p.n)
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

// ResolveStatement resolves the relations in st's conditions, before anything else
// reads them: the guard, the Rewrite hooks, the scope and rendering see only
// links. A relation that cannot be resolved fails the statement.
func ResolveStatement(st *Statement) error {
	if err := resolveJoins(st); err != nil {
		return err
	}
	if err := resolveStatementNames(st); err != nil {
		return err
	}
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

// likeEscaper is built on first use, not at program start.
var likeEscaper = sync.OnceValue(func() *strings.Replacer {
	return strings.NewReplacer(`!`, `!!`, `%`, `!%`, `_`, `!_`)
})

// EscapeLike makes s match itself in a LIKE pattern whose escape is !.
func EscapeLike(s string) string { return likeEscaper().Replace(s) }

// PredNode is the tree of p.
func PredNode(p Predicate) Node { return p.n }

// NewPredicate is the predicate whose tree is n.
func NewPredicate(n Node) Predicate { return Predicate{n} }

// Pred is a condition on entity E, made by a column handle or by And, Or and
// Not. Predicate is its untyped instance, and Predicate(p) converts p.
type Pred[E any] struct {
	n Node
}
