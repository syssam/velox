package engine

// AndPredicates, OrPredicates and NotPredicate are velox's And, Or and Not
// of untyped conditions, with the same nodes: the typed ones are generic and
// compiled once per entity, these once.

// AndPredicates is true when every p is, as velox.And.
func AndPredicates(ps ...Predicate) Predicate {
	allow := false
	for i := range ps {
		if ps[i].n.Op == OpAllow {
			allow = true
			break
		}
	}
	if !allow {
		return NewPredicate(Group(OpAnd, predNodes(ps)))
	}
	kept := make([]Node, 0, len(ps))
	for _, p := range ps {
		if p.n.Op != OpAllow {
			kept = append(kept, p.n)
		}
	}
	switch len(kept) {
	case 0:
		return NewPredicate(Node{Op: OpAllow})
	case 1:
		return NewPredicate(kept[0])
	}
	return NewPredicate(Group(OpAnd, kept))
}

// OrPredicates is true when any p is, as velox.Or.
func OrPredicates(ps ...Predicate) Predicate {
	allow, sound := false, true
	for i := range ps {
		allow = allow || ps[i].n.Op == OpAllow
		sound = sound && Sound(ps[i].n)
	}
	if allow && sound {
		return NewPredicate(Node{Op: OpAllow})
	}
	return NewPredicate(Group(OpOr, predNodes(ps)))
}

// NotPredicate is true when p is false, as velox.Not. It is not inlined:
// velox's own wrapper would then carry NewPredicate's body in velox's export
// data, and every package of entities would inline NewPredicate (see
// internal/repocheck's compile-cost test).
//
//go:noinline
func NotPredicate(p Predicate) Predicate {
	return NewPredicate(Group(OpNot, []Node{p.n}))
}

func predNodes(ps []Predicate) []Node {
	out := make([]Node, len(ps))
	for i := range ps {
		out[i] = ps[i].n
	}
	return out
}
