package orm

//go:generate go run ../internal/colgen/cmd columns.go columns_calls_test.go

// ToOne is an edge of an entity to at most one neighbor, as a generated
// entity package declares it,
//
//	Author = &orm.ToOne[predicate.Post, predicate.User, int]{Meta: &authorMeta}
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

// Where is the edge, eager-loaded with only the neighbor that matches: what
// With takes. Columns and With (the neighbor's own edges) continue it.
func (e *ToOne[E, N, ID]) Where[_ Lazy[ID]](ps ...N) *LoadOf[E, N] {
	return (&LoadOf[E, N]{edge: e.Meta}).Where(ps...)
}

// Columns is the edge, eager-loaded with only the named columns read into
// the neighbor.
func (e *ToOne[E, N, ID]) Columns[_ Lazy[ID]](columns ...string) *LoadOf[E, N] {
	return (&LoadOf[E, N]{edge: e.Meta}).Columns(columns...)
}

// With is the edge, eager-loaded together with edges of the neighbor.
func (e *ToOne[E, N, ID]) With[_ Lazy[ID]](edges ...Loader[N]) *LoadOf[E, N] {
	return (&LoadOf[E, N]{edge: e.Meta}).With(edges...)
}

// Load is the edge, eager-loaded as a query of its neighbor says, for a
// query that is already there: one built by other code, or kept from a
// request. Written in place, a load is the edge's Where, Columns and With.
func (e *ToOne[E, N, ID]) Load[PT any](q *QueryOf[PT, N, ID]) *LoadOf[E, N] {
	return loadAs[E, N](e.Meta, q)
}

// loadOf makes the edge a Loader: With takes it as it is.
func (e *ToOne[E, N, ID]) loadOf(E) load { return load{edge: e.Meta} }

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

// Where is the edge, eager-loaded with only the neighbors that match: what
// With takes.
//
//	velox.User.Query(conn).With(user.Posts.Where(post.Title.NEQ("draft")).OrderBy(post.Title.Asc()).Limit(10))
//
// OrderBy, Limit (per row), Columns and With (the neighbors' own edges)
// start a load too, and continue one.
func (e *ToMany[E, N, ID]) Where[_ Lazy[ID]](ps ...N) *LoadOf[E, N] {
	return (&LoadOf[E, N]{edge: e.Meta}).Where(ps...)
}

// OrderBy is the edge, eager-loaded with each row's neighbors in the given
// order.
func (e *ToMany[E, N, ID]) OrderBy[_ Lazy[ID]](os ...OrderOf[N]) *LoadOf[E, N] {
	return (&LoadOf[E, N]{edge: e.Meta}).OrderBy(os...)
}

// Limit is the edge, eager-loaded with at most n neighbors for each row.
func (e *ToMany[E, N, ID]) Limit[_ Lazy[ID]](n int) *LoadOf[E, N] {
	return (&LoadOf[E, N]{edge: e.Meta}).Limit(n)
}

// Columns is the edge, eager-loaded with only the named columns read into
// the neighbors.
func (e *ToMany[E, N, ID]) Columns[_ Lazy[ID]](columns ...string) *LoadOf[E, N] {
	return (&LoadOf[E, N]{edge: e.Meta}).Columns(columns...)
}

// With is the edge, eager-loaded together with edges of the neighbors.
func (e *ToMany[E, N, ID]) With[_ Lazy[ID]](edges ...Loader[N]) *LoadOf[E, N] {
	return (&LoadOf[E, N]{edge: e.Meta}).With(edges...)
}

// Load is the edge, eager-loaded as a query of its neighbors says, for a
// query that is already there: one built by other code, or kept from a
// request. Written in place, a load is the edge's Where, OrderBy, Limit,
// Columns and With.
func (e *ToMany[E, N, ID]) Load[PT any](q *QueryOf[PT, N, ID]) *LoadOf[E, N] {
	return loadAs[E, N](e.Meta, q)
}

// loadOf makes the edge a Loader: With takes it as it is.
func (e *ToMany[E, N, ID]) loadOf(E) load { return load{edge: e.Meta} }

// Count is the number of neighbors a row has on the edge, to order by:
//
//	velox.User.Query(conn).OrderBy(user.Posts.Count().Desc())
//
// A query ordered by it cannot be paged with Paginate.
func (e *ToMany[E, N, ID]) Count[_ Lazy[ID]]() EdgeCount[E] { return EdgeCount[E]{edge: e.Meta} }

// EdgeCount is the number of neighbors on an edge of the entity whose
// predicate type is E, as something to order by: what an edge's Count
// returns.
type EdgeCount[E Predicate] struct{ edge *EdgeMeta }

// Asc orders by the count, the rows with fewest neighbors first.
func (c EdgeCount[E]) Asc[_ Lazy[E]]() OrderOf[E] { return OrderOf[E](countOrder(c.edge, false)) }

// Desc orders by the count, the rows with most neighbors first.
func (c EdgeCount[E]) Desc[_ Lazy[E]]() OrderOf[E] { return OrderOf[E](countOrder(c.edge, true)) }

// plain returns the predicates of an entity as the engine holds them.
func plain[E Predicate](ps []E) []P {
	subs := make([]P, len(ps))
	for i, p := range ps {
		subs[i] = P(p)
	}
	return subs
}
