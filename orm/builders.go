package orm

import (
	"context"
	"fmt"
	"iter"
	"slices"

	"github.com/syssam/velox"
	"github.com/syssam/velox/dialect/sql"
)

// The builders. A generated tree names them per entity,
//
//	type PostQuery = orm.QueryOf[*Post, predicate.Post, int]   // package entity
//	var Post = orm.ClientOf[*entity.Post, predicate.Post, int]{Meta: &entity.PostMeta}
//
// and an application writes velox.Post.Query(conn).Where(...).All(ctx) against
// them. PT is the pointer to the entity's struct, E its predicate type and ID
// the type of its id: what a builder returns is the entity's, and a
// predicate, a setter, an order term or an eager load of another entity does
// not compile.
//
// They are types of this package and not generated per entity for what the
// column types are (columns.go): every method has the type parameter Lazy,
// so it is compiled in the package that calls it, once for every entity that
// package uses (PT is a pointer, and all pointers are one shape to the
// compiler), and a package that only names a builder compiles nothing of it.
// Generated, the builders were the largest package of a tree and what every
// package of an application read: on the schema of 450 entities of the
// benchmarks, package entity took 13.0 CPU-s and 1.6 GB to compile and the
// 312 packages using it 92 CPU-s; with these, 3.7 CPU-s and 0.6 GB, and 78.
//
// The methods are //go:noinline: inlined, each is compiled again at every
// call (the same packages took 88 CPU-s).

// as returns what the engine read as the type a builder gives it back as: a
// row as the pointer to the entity's struct, an id as the type of the id.
// They are that by construction (the builder's Meta makes the rows); a tree
// whose client names another entity's Meta than its types is the one way to
// be here with something else.
func as[T any](v any) T {
	t, ok := v.(T)
	if !ok {
		mismatch(v, t)
	}
	return t
}

//go:noinline
func mismatch(got, want any) {
	panic(fmt.Sprintf("velox: the engine read a %T where the builder's type is %T: generate the code again", got, want))
}

// conditions adds the predicates of an entity to those a builder holds, as
// the engine holds them.
func conditions[E Predicate](held []P, ps []E) []P {
	held = slices.Grow(held, len(ps))
	for _, p := range ps {
		held = append(held, P(p))
	}
	return held
}

// assignments adds the setters of an entity to those a builder holds.
func assignments[E Predicate](held []S, ss []SetterOf[E]) []S {
	held = slices.Grow(held, len(ss))
	for _, s := range ss {
		held = append(held, S(s))
	}
	return held
}

// QueryOf is the query of an entity.
type QueryOf[PT any, E Predicate, ID any] struct{ c QueryCore }

// Neighbors returns the query of the rows an entity has on one of its
// edges: the generated QueryPosts of an entity is it.
func Neighbors[PT any, E Predicate, ID any](edge *EdgeMeta, of any, ex velox.Executor) *QueryOf[PT, E, ID] {
	return &QueryOf[PT, E, ID]{c: QueryCore{M: edge.Target, Ex: ex, Preds: []P{NeighborOf(edge, of)}}}
}

// Where adds predicates; all must hold.
//
//go:noinline
func (q *QueryOf[PT, E, ID]) Where[_ Lazy[ID]](ps ...E) *QueryOf[PT, E, ID] {
	q.c.Preds = conditions(q.c.Preds, ps)
	return q
}

// OrderBy adds order terms.
//
//go:noinline
func (q *QueryOf[PT, E, ID]) OrderBy[_ Lazy[ID]](os ...OrderOf[E]) *QueryOf[PT, E, ID] {
	for _, o := range os {
		q.c.Orders = append(q.c.Orders, O(o))
	}
	return q
}

// Limit caps the number of rows.
//
//go:noinline
func (q *QueryOf[PT, E, ID]) Limit[_ Lazy[ID]](n int) *QueryOf[PT, E, ID] { q.c.Limit = n; return q }

// Offset skips the first n rows.
//
//go:noinline
func (q *QueryOf[PT, E, ID]) Offset[_ Lazy[ID]](n int) *QueryOf[PT, E, ID] { q.c.Offset = n; return q }

// Distinct removes duplicate rows.
//
//go:noinline
func (q *QueryOf[PT, E, ID]) Distinct[_ Lazy[ID]]() *QueryOf[PT, E, ID] {
	q.c.Distinct = true
	return q
}

// Columns reads only the named columns into the entities, for a list that
// shows a few columns of a wide table; every other field of an entity is
// left at its zero value, so such an entity is for reading, not for saving
// back. The id is always read, and so is what the query needs itself: the
// columns it is ordered by and the keys of the edges it loads.
//
//go:noinline
func (q *QueryOf[PT, E, ID]) Columns[_ Lazy[ID]](columns ...string) *QueryOf[PT, E, ID] {
	q.c.Columns(columns...)
	return q
}

// ForUpdate locks the rows read against concurrent changes until the
// transaction ends. It needs a transaction to mean anything.
//
//go:noinline
func (q *QueryOf[PT, E, ID]) ForUpdate[_ Lazy[ID]](opts ...LockOption) *QueryOf[PT, E, ID] {
	q.c.SetLock(LockUpdate, opts)
	return q
}

// ForShare locks the rows read against concurrent changes, and lets others
// read them, until the transaction ends.
//
//go:noinline
func (q *QueryOf[PT, E, ID]) ForShare[_ Lazy[ID]](opts ...LockOption) *QueryOf[PT, E, ID] {
	q.c.SetLock(LockShare, opts)
	return q
}

// Select reads the given columns of the matching rows, and aggregates over
// them, instead of entities:
// Select().GroupBy("status").Aggregate(orm.Count()).Scan(ctx, &rows).
//
//go:noinline
func (q *QueryOf[PT, E, ID]) Select[_ Lazy[ID]](columns ...string) *Selection {
	return q.c.Select(columns...)
}

// Aggregate reads aggregates over the matching rows instead of entities:
//
//	n, err := q.Aggregate(orm.Sum(order.ColumnAmount)).Int(ctx)
//
// With columns to group by it is Select().GroupBy(...).Aggregate(...).
//
//go:noinline
func (q *QueryOf[PT, E, ID]) Aggregate[_ Lazy[ID]](aggs ...Agg) *Selection {
	return q.c.Select().Aggregate(aggs...)
}

// SQL returns the statement the query runs for All and its arguments,
// without running it: for a log, a test, or a look at the plan.
//
//go:noinline
func (q *QueryOf[PT, E, ID]) SQL[_ Lazy[ID]](ctx context.Context) (query string, args []any, err error) {
	return q.c.Select().SQL(ctx)
}

// Clone returns an independent copy of the query.
//
//go:noinline
func (q *QueryOf[PT, E, ID]) Clone[_ Lazy[ID]]() *QueryOf[PT, E, ID] {
	c := *q
	c.c.Preds = append([]P(nil), q.c.Preds...)
	c.c.Orders = append([]O(nil), q.c.Orders...)
	c.c.With = append([]*EdgeLoad(nil), q.c.With...)
	return &c
}

// Loader is an edge to eager-load, as With takes it: an edge of the entity
// as it is (post.Author), or with how it is loaded
// (post.Comments.Where(...).Limit(10)).
type Loader[E Predicate] interface {
	// loadOf has the entity in its signature so that an edge of another
	// entity is not a Loader of this one.
	loadOf(E) load
}

// load is an edge and, when there is one, the description of its load.
type load struct {
	edge *EdgeMeta
	as   *QueryCore
}

// LoadOf is an edge of the entity whose predicate type is E, to the entity
// whose predicate type is N, with how it is eager-loaded: which neighbors,
// in what order, how many for each row, which of their columns and which of
// their own edges. It starts at the edge (user.Posts.Where(...),
// user.Posts.Limit(3)) and is given to With.
type LoadOf[E, N Predicate] struct {
	edge *EdgeMeta
	as   QueryCore
}

func (l *LoadOf[E, N]) loadOf(E) load { return load{edge: l.edge, as: &l.as} }

// loadAs is an edge loaded as a query of its neighbors says.
func loadAs[E, N Predicate, PT, ID any](edge *EdgeMeta, q *QueryOf[PT, N, ID]) *LoadOf[E, N] {
	l := &LoadOf[E, N]{edge: edge}
	if q != nil {
		l.as = q.c
	}
	return l
}

// Where loads only the neighbors that match; all predicates must hold.
func (l *LoadOf[E, N]) Where[_ Lazy[E]](ps ...N) *LoadOf[E, N] {
	l.as.Preds = conditions(l.as.Preds, ps)
	return l
}

// OrderBy loads each row's neighbors in the given order.
func (l *LoadOf[E, N]) OrderBy[_ Lazy[E]](os ...OrderOf[N]) *LoadOf[E, N] {
	for _, o := range os {
		l.as.Orders = append(l.as.Orders, O(o))
	}
	return l
}

// Limit loads at most n neighbors for each row.
func (l *LoadOf[E, N]) Limit[_ Lazy[E]](n int) *LoadOf[E, N] {
	l.as.Limit = n
	return l
}

// Columns reads only the named columns into the neighbors, with the ones
// the load cannot do without.
func (l *LoadOf[E, N]) Columns[_ Lazy[E]](columns ...string) *LoadOf[E, N] {
	l.as.Columns(columns...)
	return l
}

// With loads edges of the neighbors too.
func (l *LoadOf[E, N]) With[_ Lazy[E]](edges ...Loader[N]) *LoadOf[E, N] {
	var of N
	for _, e := range edges {
		x := e.loadOf(of)
		l.as.Load(x.edge, x.as)
	}
	return l
}

// With eager-loads edges: one more query per edge, whatever the number of
// rows.
//
//	velox.Post.Query(conn).With(post.Author, post.Comments.Where(comment.Hidden.EQ(false)).Limit(10))
//
// An edge is loaded as it is, every neighbor of every row. What says
// otherwise starts at the edge: its Where, OrderBy, Limit (for each row),
// Columns, and With for the neighbors' own edges. The load is what was said
// when With is called.
//
//go:noinline
func (q *QueryOf[PT, E, ID]) With[_ Lazy[ID]](edges ...Loader[E]) *QueryOf[PT, E, ID] {
	var of E
	for _, e := range edges {
		x := e.loadOf(of)
		q.c.Load(x.edge, x.as)
	}
	return q
}

// All returns every matching row.
//
//go:noinline
func (q *QueryOf[PT, E, ID]) All[_ Lazy[ID]](ctx context.Context) ([]PT, error) {
	rows, err := q.c.All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]PT, len(rows))
	for i, r := range rows {
		out[i] = as[PT](r)
	}
	return out, nil
}

// First returns the first matching row, or velox.ErrNotFound.
//
//go:noinline
func (q *QueryOf[PT, E, ID]) First[_ Lazy[ID]](ctx context.Context) (PT, error) {
	r, err := q.c.First(ctx)
	if err != nil {
		var none PT
		return none, err
	}
	return as[PT](r), nil
}

// Only returns the single matching row: velox.ErrNotFound for none,
// velox.ErrNotSingular for more.
//
//go:noinline
func (q *QueryOf[PT, E, ID]) Only[_ Lazy[ID]](ctx context.Context) (PT, error) {
	r, err := q.c.Only(ctx)
	if err != nil {
		var none PT
		return none, err
	}
	return as[PT](r), nil
}

// Iter returns the matching rows one at a time, without loading them all:
//
//	for u, err := range q.Iter(ctx) { ... }
//
// Every loop over the result runs the query again. A failure is delivered as
// the last pair. The rows stay open during the loop, so on a transaction
// nothing else can run until it ends; eager loads are refused.
//
//go:noinline
func (q *QueryOf[PT, E, ID]) Iter[_ Lazy[ID]](ctx context.Context) iter.Seq2[PT, error] {
	return func(yield func(PT, error) bool) {
		err := q.c.Each(ctx, func(e any) bool { return yield(as[PT](e), nil) })
		if err != nil {
			var none PT
			yield(none, err)
		}
	}
}

// PageEdge is a row of a page, with its cursor. A generated tree names it
// per entity, entity.PostEdge.
type PageEdge[PT any] struct {
	Node   PT           `json:"node"`
	Cursor velox.Cursor `json:"cursor"`
}

// Connection is one page of an entity: what Paginate returns. The shape and
// the field names are Relay's, which is also what a GraphQL engine binds an
// entity's connection type to. A generated tree names it per entity,
// entity.PostConnection.
type Connection[PT any] struct {
	Edges    []*PageEdge[PT] `json:"edges"`
	PageInfo velox.PageInfo  `json:"pageInfo"`
	// TotalCount is the number of rows the query matches, or -1 when it
	// was not counted (orm.WithoutTotal).
	TotalCount int `json:"totalCount"`
}

// Paginate returns one page of the matching rows, in the query's order, with
// a cursor per row: the first rows after the cursor after, or the last rows
// before the cursor before. It is keyset pagination in the shape of a Relay
// connection, for any kind of list endpoint.
//
//go:noinline
func (q *QueryOf[PT, E, ID]) Paginate[_ Lazy[ID]](ctx context.Context, after *velox.Cursor, first *int, before *velox.Cursor, last *int) (*Connection[PT], error) {
	p, err := q.c.Paginate(ctx, after, first, before, last)
	if err != nil {
		return nil, err
	}
	conn := &Connection[PT]{Edges: make([]*PageEdge[PT], len(p.Rows)), PageInfo: p.Info, TotalCount: p.Total}
	for i, r := range p.Rows {
		conn.Edges[i] = &PageEdge[PT]{Node: as[PT](r), Cursor: p.Cursors[i]}
	}
	return conn, nil
}

// FirstID returns the id of the first matching row, or velox.ErrNotFound.
//
//go:noinline
func (q *QueryOf[PT, E, ID]) FirstID[_ Lazy[ID]](ctx context.Context) (ID, error) {
	return q.id(ctx, 1)
}

// OnlyID returns the id of the single matching row: velox.ErrNotFound for
// none, velox.ErrNotSingular for more.
//
//go:noinline
func (q *QueryOf[PT, E, ID]) OnlyID[_ Lazy[ID]](ctx context.Context) (ID, error) {
	return q.id(ctx, 2)
}

// id reads at most limit ids and returns the one there is.
//
//go:noinline
func (q *QueryOf[PT, E, ID]) id[_ Lazy[ID]](ctx context.Context, limit int) (ID, error) {
	var none ID
	c := q.c
	c.Limit = limit
	vs, err := c.IDs(ctx)
	switch {
	case err != nil:
		return none, err
	case len(vs) == 0:
		return none, q.c.M.NotFound()
	case len(vs) > 1:
		return none, q.c.M.notSingular()
	}
	return as[ID](vs[0]), nil
}

// Count returns the number of matching rows.
//
//go:noinline
func (q *QueryOf[PT, E, ID]) Count[_ Lazy[ID]](ctx context.Context) (int, error) {
	return q.c.Count(ctx)
}

// Exists reports whether any row matches.
//
//go:noinline
func (q *QueryOf[PT, E, ID]) Exists[_ Lazy[ID]](ctx context.Context) (bool, error) {
	return q.c.Exists(ctx)
}

// IDs returns the ids of the matching rows.
//
//go:noinline
func (q *QueryOf[PT, E, ID]) IDs[_ Lazy[ID]](ctx context.Context) ([]ID, error) {
	vs, err := q.c.IDs(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]ID, len(vs))
	for i, v := range vs {
		ids[i] = as[ID](v)
	}
	return ids, nil
}

// CreateOf creates one row of an entity.
type CreateOf[PT any, E Predicate] struct{ c MutCore }

// Set adds field values and edges of the new row.
//
//go:noinline
func (b *CreateOf[PT, E]) Set[_ Lazy[PT]](ss ...SetterOf[E]) *CreateOf[PT, E] {
	b.c.Sets = assignments(b.c.Sets, ss)
	return b
}

// Fill takes what in gives: a struct whose fields are named like those of
// the entity, such as the input of an endpoint. A nil pointer gives nothing,
// an optional (a type with ValueOK) gives nothing when absent, anything else
// is written. A field that names nothing of the entity is an error unless
// listed in skip. A later Set wins.
//
//go:noinline
func (b *CreateOf[PT, E]) Fill[_ Lazy[PT]](in any, skip ...string) *CreateOf[PT, E] {
	b.c.Fill(in, false, skip)
	return b
}

// Save inserts the row and returns it.
//
//go:noinline
func (b *CreateOf[PT, E]) Save[_ Lazy[PT]](ctx context.Context) (PT, error) {
	e, err := b.c.Insert(ctx)
	if err != nil {
		var none PT
		return none, err
	}
	return as[PT](e), nil
}

// Exec inserts the row.
//
//go:noinline
func (b *CreateOf[PT, E]) Exec[_ Lazy[PT]](ctx context.Context) error {
	_, err := b.c.Insert(ctx)
	return err
}

// CreateBulkOf creates several rows of an entity with one statement.
type CreateBulkOf[PT any, E Predicate] struct {
	m    *Meta
	ex   velox.Executor
	bs   []*CreateOf[PT, E]
	rows [][]S // the rows given to Row, after those of bs
	n    int
}

//go:noinline
func (b *CreateBulkOf[PT, E]) sets[_ Lazy[PT]]() [][]S {
	rows := make([][]S, 0, len(b.bs)+len(b.rows))
	for _, c := range b.bs {
		rows = append(rows, c.c.Sets)
	}
	return append(rows, b.rows...)
}

// Row adds a row, by what it sets:
//
//	velox.Tag.CreateBulk(conn).Row(tag.Name.Set("go")).Row(tag.Name.Set("sql")).Save(ctx)
//
// It is the short form for rows written in place or added in a loop. Rows
// given as builders to the client's CreateBulk come first.
//
//go:noinline
func (b *CreateBulkOf[PT, E]) Row[_ Lazy[PT]](ss ...SetterOf[E]) *CreateBulkOf[PT, E] {
	b.rows = append(b.rows, assignments(nil, ss))
	return b
}

// BatchSize makes Save and Exec insert the rows n at a time, with a statement
// for every n of them where they would use one for all: a statement has a
// limit on its parameters, and a row takes one for each of its columns. The
// statements are one transaction only when the handle is one. On a database
// handle, one that fails leaves the rows of those before it in the table, and
// Save returns them with the error.
//
//go:noinline
func (b *CreateBulkOf[PT, E]) BatchSize[_ Lazy[PT]](n int) *CreateBulkOf[PT, E] {
	b.n = n
	return b
}

// Save inserts the rows and returns them in order.
//
//go:noinline
func (b *CreateBulkOf[PT, E]) Save[_ Lazy[PT]](ctx context.Context) ([]PT, error) {
	rows, err := InsertBulk(ctx, b.ex, b.m, b.sets(), b.n)
	if err != nil && len(rows) == 0 {
		return nil, err
	}
	out := make([]PT, len(rows))
	for i, r := range rows {
		out[i] = as[PT](r)
	}
	return out, err
}

// Exec inserts the rows.
//
//go:noinline
func (b *CreateBulkOf[PT, E]) Exec[_ Lazy[PT]](ctx context.Context) error {
	_, err := InsertBulk(ctx, b.ex, b.m, b.sets(), b.n)
	return err
}

// UpdateOf updates rows of an entity.
type UpdateOf[PT any, E Predicate] struct{ c MutCore }

// Set adds assignments and edge changes.
//
//go:noinline
func (b *UpdateOf[PT, E]) Set[_ Lazy[PT]](ss ...SetterOf[E]) *UpdateOf[PT, E] {
	b.c.Sets = assignments(b.c.Sets, ss)
	return b
}

// Where restricts the rows updated; all predicates must hold.
//
//go:noinline
func (b *UpdateOf[PT, E]) Where[_ Lazy[PT]](ps ...E) *UpdateOf[PT, E] {
	b.c.Preds = conditions(b.c.Preds, ps)
	return b
}

// Fill takes what in gives, as on a create; here an optional that is present
// and null, and a true Clear<Name> field, set a column that can be NULL to
// NULL. What in does not give is left alone.
//
//go:noinline
func (b *UpdateOf[PT, E]) Fill[_ Lazy[PT]](in any, skip ...string) *UpdateOf[PT, E] {
	b.c.Fill(in, true, skip)
	return b
}

// Exec updates the matching rows and returns how many: with nothing to set,
// how many match.
//
//go:noinline
func (b *UpdateOf[PT, E]) Exec[_ Lazy[PT]](ctx context.Context) (int, error) { return b.c.Update(ctx) }

// ExecOne updates the single matching row: velox.ErrNotFound when no row
// matches, velox.ErrNotSingular (and nothing written) when several do. It is
// SaveOne for a caller that has no use for the row.
//
//go:noinline
func (b *UpdateOf[PT, E]) ExecOne[_ Lazy[PT]](ctx context.Context) error { return b.c.ExecOne(ctx) }

// SaveOne updates the single matching row and returns it: velox.ErrNotFound
// when no row matches, velox.ErrNotSingular (and nothing written) when several
// do. Updating any number of rows is Exec.
//
//go:noinline
func (b *UpdateOf[PT, E]) SaveOne[_ Lazy[PT]](ctx context.Context) (PT, error) {
	e, err := b.c.UpdateOne(ctx)
	if err != nil {
		var none PT
		return none, err
	}
	return as[PT](e), nil
}

// DeleteOf deletes rows of an entity.
type DeleteOf[E Predicate] struct{ c MutCore }

// Where restricts the rows deleted; all predicates must hold.
//
//go:noinline
func (b *DeleteOf[E]) Where[_ Lazy[E]](ps ...E) *DeleteOf[E] {
	b.c.Preds = conditions(b.c.Preds, ps)
	return b
}

// Exec deletes every matching row and returns how many.
//
//go:noinline
func (b *DeleteOf[E]) Exec[_ Lazy[E]](ctx context.Context) (int, error) { return b.c.Delete(ctx) }

// ExecOne deletes the single matching row: velox.ErrNotFound when no row
// matches, velox.ErrNotSingular (and nothing deleted) when several do. After
// DeleteByID a further Where is a guard, and a row that no longer meets it is
// reported as not found:
//
//	err := velox.Todo.DeleteByID(tx, id).Where(todo.Version.EQ(seen)).ExecOne(ctx)
//
//go:noinline
func (b *DeleteOf[E]) ExecOne[_ Lazy[E]](ctx context.Context) error { return b.c.DeleteOne(ctx) }

// ClientOf is where the queries and mutations of an entity start: the
// variable of the entity in the root package of a generated tree,
// velox.Post. A program that uses package entity without the root package
// declares one itself, from the entity's Meta.
type ClientOf[PT any, E Predicate, ID any] struct{ Meta *Meta }

// Query returns a query of every row of the entity, to run on ex: a database
// handle or a transaction.
func (c *ClientOf[PT, E, ID]) Query[_ Lazy[ID]](ex velox.Executor) *QueryOf[PT, E, ID] {
	return &QueryOf[PT, E, ID]{c: QueryCore{M: c.Meta, Ex: ex}}
}

// Create returns a builder that creates a row.
func (c *ClientOf[PT, E, ID]) Create[_ Lazy[ID]](ex velox.Executor) *CreateOf[PT, E] {
	return &CreateOf[PT, E]{c: MutCore{M: c.Meta, Ex: ex}}
}

// CreateBulk returns a builder that creates several rows with one statement,
// on ex: one per call of the builder's Row, and one per given builder, each
// as it is when the rows are inserted (what is set on a builder after this
// call is part of its row). Of a given builder only what it sets is used:
// the rows run on ex, whatever the builder was made with.
func (c *ClientOf[PT, E, ID]) CreateBulk[_ Lazy[ID]](ex velox.Executor, bs ...*CreateOf[PT, E]) *CreateBulkOf[PT, E] {
	return &CreateBulkOf[PT, E]{m: c.Meta, ex: ex, bs: bs}
}

// Update returns a builder that updates the rows its conditions match.
func (c *ClientOf[PT, E, ID]) Update[_ Lazy[ID]](ex velox.Executor) *UpdateOf[PT, E] {
	return &UpdateOf[PT, E]{c: MutCore{M: c.Meta, Ex: ex}}
}

// Delete returns a builder that deletes the rows its conditions match.
func (c *ClientOf[PT, E, ID]) Delete[_ Lazy[ID]](ex velox.Executor) *DeleteOf[E] {
	return &DeleteOf[E]{c: MutCore{M: c.Meta, Ex: ex}}
}

// Raw returns a condition of the entity written against the SQL builder, for
// what the columns' methods do not say:
//
//	velox.User.Raw(func(s *sql.Selector) { s.Where(sql.ExprP("lower(name) = ?", name)) })
//
//go:noinline
func (c *ClientOf[PT, E, ID]) Raw[_ Lazy[ID]](f func(*sql.Selector)) E { return E(Raw(f)) }

// RawOrder returns an order term of the entity written against the SQL
// builder: NULLS LAST, an expression. A query ordered this way cannot be
// paged with Paginate.
//
//go:noinline
func (c *ClientOf[PT, E, ID]) RawOrder[_ Lazy[ID]](f func(*sql.Selector)) OrderOf[E] {
	return OrderOf[E](RawOrder(f))
}

// FieldPredicate returns the condition on a field named at run time, a
// filter of a list endpoint: the field by its name in the schema, an
// operator, a value of the field's type. A field that is not there, or one
// the operator does not fit, is an error.
//
//go:noinline
func (c *ClientOf[PT, E, ID]) FieldPredicate[_ Lazy[ID]](field string, op Op, value any) (E, error) {
	p, err := FieldPredicate(c.Meta, field, op, value)
	return E(p), err
}

// FieldOrder returns the order by a field named at run time. A field that
// is not there, or a sensitive one, is an error.
//
//go:noinline
func (c *ClientOf[PT, E, ID]) FieldOrder[_ Lazy[ID]](field string, desc bool) (OrderOf[E], error) {
	o, err := FieldOrder(c.Meta, field, desc)
	return OrderOf[E](o), err
}

//go:noinline
func (c *ClientOf[PT, E, ID]) byID[_ Lazy[ID]](id ID) E {
	return E(P{Op: OpEQ, Col: c.Meta.id().Column, Val: id})
}

// Get returns the row with the given id, or velox.ErrNotFound.
//
//go:noinline
func (c *ClientOf[PT, E, ID]) Get[_ Lazy[ID]](ctx context.Context, ex velox.Executor, id ID) (PT, error) {
	return c.Query(ex).Where(c.byID(id)).Only(ctx)
}

// UpdateByID returns a builder that updates the row with the given id. End
// it with SaveOne or ExecOne, which report a missing row; Exec returns 0 for
// one and no error.
//
//go:noinline
func (c *ClientOf[PT, E, ID]) UpdateByID[_ Lazy[ID]](ex velox.Executor, id ID) *UpdateOf[PT, E] {
	return c.Update(ex).Where(c.byID(id))
}

// DeleteByID returns a builder that deletes the row with the given id. End
// it with ExecOne, which reports a missing row; Exec returns 0 for one and no
// error.
//
//go:noinline
func (c *ClientOf[PT, E, ID]) DeleteByID[_ Lazy[ID]](ex velox.Executor, id ID) *DeleteOf[E] {
	return c.Delete(ex).Where(c.byID(id))
}
