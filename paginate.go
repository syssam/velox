package velox

import "context"

// Paged is the constraint of a query's Paginate: a pointer to entity E whose
// PaginateQuery pages a query of E. velox-graphql generates PaginateQuery for
// each GraphQL type with a connection; K is its cursor, O its option and C
// its connection. Paginate calls PaginateQuery on a nil pointer.
type Paged[E, K, O, C any] interface {
	*E
	PaginateQuery(ctx context.Context, q Query[E], after *K, first *int, before *K, last *int, opts []O) (C, error)
}

// Paginate is the connection to the page of q's rows that after, first,
// before and last select, shaped by opts: E's PaginateQuery, which
// velox-graphql generates. An order by fields is an option. q may be
// ordered by expressions, such as a Calc, and by E's own columns, in any
// order, each with its Nulls: they come before the option's order and the
// key, and the cursors carry their values (decision 0166). A column that q
// orders by and the option names too is refused, as is an ordering of q by
// a relation's aggregate or a related row's column. Mark a Calc with Text,
// Integer, Time or Bool so that a cursor's value of it is checked.
//
//	db.Query[veloxgen.Post]().Paginate(ctx, after, first, before, last,
//		veloxgen.WithPostOrder(orderBy), veloxgen.WithPostFilter(where))
//
// It does not compile for an entity with no PaginateQuery.
func (q Query[E]) Paginate[P Paged[E, K, O, C], K, O, C any](ctx context.Context, after *K, first *int, before *K, last *int, opts ...O) (C, error) {
	return P(nil).PaginateQuery(ctx, q, after, first, before, last, opts)
}
