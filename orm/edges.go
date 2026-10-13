package orm

//go:generate go run ../internal/colgen/cmd columns.go columns_calls_test.go

// ToOne is an edge of an entity to at most one neighbor, as a generated
// entity package declares it,
//
//	Author = &orm.ToOne[predicate.Post, predicate.User, int]{Meta: &authorEdge}
//
// and what its methods build: post.Author.Has() is a condition of a query of
// Post, post.Author.Set(id) an assignment of a mutation of it. E is the
// predicate type of the entity the edge is of, N that of the neighbor, and ID
// the type of the neighbor's id. The methods have a type parameter of their
// own for what the methods of the column types have theirs for: see Lazy.
// The one ordinary method, loadOf, is what lets With take the edge itself.
type ToOne[E, N Predicate, ID any] struct{ Meta *EdgeMeta }

// Has matches the rows that have a neighbor on the edge.
func (e *ToOne[E, N, ID]) Has[_ Lazy[ID]]() E { return E(P{Op: OpHasEdge, Edge: e.Meta}) }

// HasWith matches the rows that have a neighbor on the edge satisfying the
// predicates.
func (e *ToOne[E, N, ID]) HasWith[_ Lazy[ID]](ps ...N) E {
	return E(P{Op: OpHasEdge, Edge: e.Meta, Subs: plain(ps)})
}

// Set connects the row to the neighbor with the given id.
func (e *ToOne[E, N, ID]) Set[_ Lazy[ID]](id ID) SetterOf[E] {
	return SetterOf[E]{Kind: EdgeAdd, Edge: e.Meta, IDs: []any{id}}
}

// Clear disconnects the row from its neighbor.
func (e *ToOne[E, N, ID]) Clear[_ Lazy[ID]]() SetterOf[E] {
	return SetterOf[E]{Kind: EdgeClear, Edge: e.Meta}
}

// Load is the edge with a query of its neighbor that describes how the edge
// is eager-loaded, for With: the query's conditions and its nested loads.
func (e *ToOne[E, N, ID]) Load[PT any](q *QueryOf[PT, N, ID]) LoadOf[E] {
	if q == nil {
		return LoadOf[E]{edge: e.Meta}
	}
	return LoadOf[E]{edge: e.Meta, load: &q.c}
}

// loadOf makes the edge a Loader: With takes it as it is.
func (e *ToOne[E, N, ID]) loadOf() LoadOf[E] { return LoadOf[E]{edge: e.Meta} }

// ToMany is an edge of an entity to any number of neighbors: see ToOne.
type ToMany[E, N Predicate, ID any] struct{ Meta *EdgeMeta }

// Has matches the rows that have at least one neighbor on the edge.
func (e *ToMany[E, N, ID]) Has[_ Lazy[ID]]() E { return E(P{Op: OpHasEdge, Edge: e.Meta}) }

// HasWith matches the rows that have a neighbor on the edge satisfying the
// predicates.
func (e *ToMany[E, N, ID]) HasWith[_ Lazy[ID]](ps ...N) E {
	return E(P{Op: OpHasEdge, Edge: e.Meta, Subs: plain(ps)})
}

// Add connects the row to the neighbors with the given ids.
func (e *ToMany[E, N, ID]) Add[_ Lazy[ID]](ids ...ID) SetterOf[E] {
	return SetterOf[E]{Kind: EdgeAdd, Edge: e.Meta, IDs: operands(ids)}
}

// Remove disconnects the row from the neighbors with the given ids.
func (e *ToMany[E, N, ID]) Remove[_ Lazy[ID]](ids ...ID) SetterOf[E] {
	return SetterOf[E]{Kind: EdgeRemove, Edge: e.Meta, IDs: operands(ids)}
}

// Clear disconnects the row from every neighbor on the edge.
func (e *ToMany[E, N, ID]) Clear[_ Lazy[ID]]() SetterOf[E] {
	return SetterOf[E]{Kind: EdgeClear, Edge: e.Meta}
}

// Load is the edge with a query of its neighbors that describes how the edge
// is eager-loaded, for With: the query's conditions, order, nested loads and
// its limit, which applies per row.
func (e *ToMany[E, N, ID]) Load[PT any](q *QueryOf[PT, N, ID]) LoadOf[E] {
	if q == nil {
		return LoadOf[E]{edge: e.Meta}
	}
	return LoadOf[E]{edge: e.Meta, load: &q.c}
}

// loadOf makes the edge a Loader: With takes it as it is.
func (e *ToMany[E, N, ID]) loadOf() LoadOf[E] { return LoadOf[E]{edge: e.Meta} }

// plain returns the predicates of an entity as the engine holds them.
func plain[E Predicate](ps []E) []P {
	subs := make([]P, len(ps))
	for i, p := range ps {
		subs[i] = P(p)
	}
	return subs
}
