package gqlgraph

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
	"github.com/syssam/velox/untyped"
)

// The edges batch: the first edge field resolved on any row of a
// group (the rows one statement read, or veloxgql.Batch was given) reads
// that edge for every row of the group, in one statement, and the rows it
// reads are a group in turn. So a nested query sends one statement per edge
// per level, whatever its rows.
//
// A caller batches only from the edge's own field: the field of ctx (as the
// adapter's selection source tells it) must be the edge's ("Post.likes": its owner's
// Name and its Edges entry for the relation). A call from any other field,
// an application's resolver calling x.Children, reads alone: one statement
// for its row, by its own arguments.
//
// A batch is keyed by its group, the edge's field occurrence (so two aliases
// are two batches, and so are two selections of one field), the edge (its name, the relation and the
// target type), the edge's DB (its row's, Edge.db), EdgeList's MaxList, the
// callers' row scope (velox.RowScopeFromContext: the scope values, the WithoutPolicy
// reason and the WithShard shard, of the context each passes
// to the edge function), the read of the target that velox would send under
// that context (its SQL and arguments, after the Read policy, the
// interceptors and the Rewrite hooks), and the value of
// gqlgenx.WithBatchScope's function, if the extension has one. A row whose resolver's context a field middleware or a
// directive scopes otherwise is in a batch of its own scope; a key that cannot
// be compared (reflect.Value.Comparable) reads alone. A connection's batch
// keeps its first caller's arguments, those its paginate passed to Paginate,
// and a caller whose arguments differ (reflect.DeepEqual: a resolver that
// computes them per row) reads alone.
//
// The first caller reads for all, under the context it passed to the edge
// function, on the DB of its query; the others wait on the batch's
// sync.Once, inside their source's Wait, never for one another, so a
// mutation's resolvers, which the gqlgen extension runs one at a time, batch
// too. A context value that only an
// Around hook reads is the first caller's for every row: it must be the
// same for every row of the field, or be in WithBatchScope's value. A
// caller whose read fails (a policy denies it) reads alone. A waiter does not watch its
// own context's cancellation, only the load's: every resolver of a response
// has the request's. A batch's error is every row's.

// An Edge is a relation of one row: the owner's type, the row and the
// relation's index on the owner. Type.Edge makes one, for EdgeOne, EdgeList
// and Paginate. Its GraphQL field is the owner's Name and its Edges entry
// for the relation ("Post.author").
type Edge struct {
	owner *Type
	x     any
	rel   int
}

// Edge is the relation rel of x, a *E of c's entity E. For generated code,
// whose edge methods pass it to EdgeOne, EdgeList or a type's paginate.
func (c *Type) Edge(x any, rel int) Edge { return Edge{owner: c, x: rowOf(x), rel: rel} }

// field is the edge's GraphQL field: its owner's type and the field's name,
// "" when the owner's Edges names none for the relation.
func (e Edge) field() (typ, name string) {
	if e.rel >= 0 && e.rel < len(e.owner.Edges) {
		name = e.owner.Edges[e.rel]
	}
	return e.owner.Name, name
}

// fieldName is the edge's field as an error names it, "Post.reviews".
func (e Edge) fieldName() string {
	typ, name := e.field()
	return typ + "." + name
}

// paginate is Paginate of the edge: a connection of every node of c's type,
// on the edge's DB (Edge.db), that the edge leads to.
// When the owner's row is in a group and ctx is the edge's field, its page
// is read with the pages of every row of the group whose arguments are the
// same (edgeArgs), in one statement (TableQuery.LoadEach, of the page size
// + 1 per owner, in the connection's order and after its cursor), and its
// totalCount with theirs (TableQuery.CountEach), on the first call of
// either for any row, under the context Paginate was given; the rows read
// become a group. Otherwise its queries are filtered by the relation of the
// row.
func (e Edge) paginate(ctx context.Context, c *Type, args Args, order []Order) (*Connection, error) {
	db, err := e.db(ctx)
	if err != nil {
		return nil, err
	}
	conn, err := paginate(ctx, untyped.Query(db, c.table()), c, args, order)
	if err != nil {
		return nil, err
	}
	return edgeConnection(ctx, conn, e, edgeArgs{args.After, args.Before, args.First, args.Last, args.Where.input, order}), nil
}

// edgeArgs are the arguments of an edge's connection, which the callers of
// its batch share: Args, with the where input but not its condition, which
// may hold a function that reflect.DeepEqual never finds equal.
type edgeArgs struct {
	after, before *veloxgql.Cursor
	first, last   *int
	where         any
	order         []Order
}

// EdgeOne returns the node of the to-one edge e as a *T of target's entity
// T in an any, or nil. It reads, on the edge's DB (Edge.db), only the
// columns the field's selection of target's type needs (selected, narrow,
// target.columns): with the other rows of the owner's
// group in one statement (LoadEach, of one per owner) when ctx is the edge's
// field, or alone. The node read alone is a group of its own, and so are the
// nodes of a batch: their edges batch.
func EdgeOne(ctx context.Context, target *Type, e Edge) (any, error) {
	q, err := edgeQuery(ctx, target, e)
	if err != nil {
		return nil, err
	}
	p, err := edgeOne(ctx, q, e)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// EdgeList returns the nodes of the list edge e as a []*T of target's entity
// T in an any, in key order. It reads them as EdgeOne reads its node,
// batched, with at most MaxList + 1 per owner, whatever the DB's
// WithMaxRows: an owner with more than MaxList fails with
// velox.ErrTooManyRows, in an error naming the edge's field, and the others
// do not. MaxList is that of veloxgql.LimitsFromContext(ctx), or maxList, the
// edge's graphql.MaxList, when that is lower and not 0. Read alone, the row's
// nodes are one LoadEach of it, a group of their own.
func EdgeList(ctx context.Context, target *Type, e Edge, maxList int) (any, error) {
	q, err := edgeQuery(ctx, target, e)
	if err != nil {
		return nil, err
	}
	ps, err := edgeList(ctx, q, e, maxList)
	if err != nil {
		return nil, err
	}
	return nodesOf(target, ps), nil
}

// db is the DB the edge e reads on: the one its row was read or written
// through (untyped.SourceDB), which is never one WithoutPolicy, and, for a
// row no statement made, the DB veloxgql.DBFromContext reads from ctx.
func (e Edge) db(ctx context.Context) (*velox.DB, error) {
	if db := untyped.SourceDB(e.owner.table(), e.x); db != nil {
		return db, nil
	}
	db, err := veloxgql.DBFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("veloxgql: %s: %w, and %w", e.fieldName(), velox.ErrNoSource, err)
	}
	return db, nil
}

// edgeQuery is the query of target's rows that the edge e reads, on its DB
// (Edge.db), reading only the columns the selection of the field in ctx
// needs.
func edgeQuery(ctx context.Context, target *Type, e Edge) (untyped.TableQuery, error) {
	db, err := e.db(ctx)
	if err != nil {
		return untyped.TableQuery{}, err
	}
	q := untyped.Query(db, target.table())
	if fields, ok := selected(ctx, target.Name); ok {
		q = narrow(q, target.columns(fields, nil))
	}
	return q, nil
}

// edgeConnection returns c, a connection over a query of every node of the
// edge's target, as the connection of e, read with the arguments args:
// batched as Edge's paginate says, or, unbatched, with c's queries filtered
// by the relation of e's row. A base query with a lock (ForUpdate,
// ForShare) is refused when batched: a per-owner read locks nothing.
func edgeConnection(ctx context.Context, c *Connection, e Edge, args any) *Connection {
	owner := e.owner.table()
	b, ptrs, i := batchOf(ctx, c.query.Table(), e, 0, args)
	if b == nil {
		c.query = c.query.Where(owner.Of(e.rel, c.query.Table(), e.x))
		return c
	}
	// The batch reads under the context of the edge's field, whose row scope
	// keys it.
	n := c.n
	c.edge = &edgeMode{
		rows: func(q untyped.TableQuery) (untyped.Rows, error) {
			// The page size and the cursor are the batch's arguments: the
			// same in the connection of every caller of the batch.
			parts, err := loadBatch(ctx, b, q.Limit(0).MaxRows(0), owner, e.rel, n+1, ptrs, func(r untyped.Rows) int { return min(r.Len(), n) })
			if err != nil {
				return untyped.Rows{}, err
			}
			return parts[i], nil
		},
		count: func(q untyped.TableQuery) (int, error) {
			counts, err := b.Counts(ctx, func() ([]int64, error) { return q.Limit(0).CountEach(ctx, owner, e.rel, ptrs) })
			if err != nil {
				return 0, err
			}
			return int(counts[i]), nil
		},
	}
	return c
}

// edgeOne returns the node of the to-one edge e, or nil: read with the
// other rows of the owner's group in one statement (LoadEach, of one per
// owner) when ctx is the edge's field, or alone with
// q.Where(owner.Of(rel, …, x)).First. q, a query of the edge's target,
// selects the target's columns.
//
// The node read alone is a group of its own, and so are the nodes of a
// batch: their edges batch.
func edgeOne(ctx context.Context, q untyped.TableQuery, e Edge) (any, error) {
	owner := e.owner.table()
	b, owners, i := batchOf(ctx, q.Table(), e, 0, nil)
	if b == nil {
		v, err := q.Where(owner.Of(e.rel, q.Table(), e.x)).First(ctx)
		if velox.IsNotFound(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		gqlcore.Add(ctx, []any{v})
		return v, nil
	}
	parts, err := loadBatch(ctx, b, q, owner, e.rel, 1, owners, untyped.Rows.Len)
	if err != nil {
		return nil, err
	}
	if parts[i].Len() == 0 {
		return nil, nil
	}
	return parts[i].At(0), nil
}

// edgeList returns the nodes of the list edge e, in key order, batched as
// edgeOne is, with at most MaxList + 1 per owner, whatever the DB's
// WithMaxRows: an owner with more than MaxList fails with
// velox.ErrTooManyRows, in an error naming the edge's field, and the others
// do not.
//
// MaxList is that of veloxgql.LimitsFromContext(ctx), or maxList, the edge's
// graphql.MaxList, when that is lower and not 0. Read alone, the row's nodes
// are one LoadEach of it, a group of their own.
func edgeList(ctx context.Context, q untyped.TableQuery, e Edge, maxList int) ([]any, error) {
	most := veloxgql.LimitsFromContext(ctx).MaxList
	if maxList > 0 {
		most = min(most, maxList)
	}
	keep := func(rows untyped.Rows) int {
		if rows.Len() > most {
			return 0
		}
		return rows.Len()
	}
	b, owners, i := batchOf(ctx, q.Table(), e, most, nil)
	if b == nil {
		b, owners, i = new(gqlcore.Batch), []any{e.x}, 0
	}
	parts, err := loadBatch(ctx, b, q, e.owner.table(), e.rel, most+1, owners, keep)
	if err != nil {
		return nil, fmt.Errorf("veloxgql: %s: %w", e.fieldName(), err)
	}
	rows := parts[i]
	if rows.Len() > most {
		return nil, fmt.Errorf("veloxgql: %s: %w: more than %d, the edge's MaxList", e.fieldName(), velox.ErrTooManyRows, most)
	}
	out := make([]any, rows.Len())
	for k := range out {
		out[k] = rows.At(k)
	}
	return out, nil
}

// edgeMode is a Connection's edge mode: its page and count are its part of a
// batch.
type edgeMode struct {
	// rows returns the connection's rows, q being its query in its order,
	// after its cursor.
	rows func(q untyped.TableQuery) (untyped.Rows, error)
	// count returns the connection's totalCount, q being its query.
	count func(q untyped.TableQuery) (int, error)
}

// batchKey is what the callers of a batch share, besides its group, its
// field and its selections: see the edges batch above.
type batchKey struct {
	typ, field string            // the edge's GraphQL field: "Post" and "likes"
	rel        int               // the relation's index on the owner
	target     *velox.EntityInfo // the relation's target
	db         *velox.DB         // the edge's (Edge.db): its row's
	most       int               // EdgeList's MaxList; 0 for the others
	scope      velox.RowScope
	app        any // gqlgenx.WithBatchScope's value
	// read is the SQL, with its arguments, of a read of target under the
	// caller's context: two callers whose Read policies, interceptors,
	// Rewrite hooks or policy bypass make different reads share no batch,
	// whatever context values those read.
	read string
}

// batchOf returns the batch of the edge e, to target, read with the
// arguments args (most being EdgeList's MaxList), for the group of e's row
// and ctx's key, the group's rows and the row's index in them. The batch is
// nil, and the row reads alone, when ctx is in no field its source knows,
// or not in e's, when the row is in no group of ctx's table, when the key
// cannot be compared, or when the batch was made with other arguments.
func batchOf(ctx context.Context, target untyped.Table, e Edge, most int, args any) (*gqlcore.Batch, []any, int) {
	f, ok := gqlcore.FieldOf(ctx)
	if !ok || f.Key == nil {
		return nil, nil, 0
	}
	typ, name := e.field()
	if name == "" || f.Object != typ || f.Name != name {
		return nil, nil, 0
	}
	t := f.Table
	g, ps, i, ok := gqlcore.Find(t, e.x)
	if !ok {
		return nil, nil, 0
	}
	db, _ := e.db(ctx)
	if db == nil {
		return nil, nil, 0
	}
	key := batchKey{typ: typ, field: name, rel: e.rel, target: target.Info(), db: db, most: most, scope: velox.RowScopeFromContext(ctx), app: t.Scope(ctx)}
	if !canCompare(key.app) {
		// A batch scope == would panic on: a read of its own.
		return nil, nil, 0
	}
	read, ok := readOf(ctx, db, target)
	if !ok {
		return nil, nil, 0
	}
	key.read = read
	b := g.Batch(f.Key, key, args)
	if b == nil {
		return nil, nil, 0
	}
	return b, ps, i
}

// readOf returns the SQL and arguments of a read of target on db under ctx,
// as one string, and false when there is none (the read fails): the caller
// then reads alone. It is what the caller may see of target, as velox would
// send it, after the Read policy, the interceptors and the Rewrite hooks.
// It is a dry run, with the rendering cached: about 1.4 µs and 3
// allocations a caller on SQLite (Apple M3 Max), where reading alone
// costs each caller a statement.
func readOf(ctx context.Context, db *velox.DB, target untyped.Table) (string, bool) {
	text, args, err := untyped.Query(db, target).Limit(1).SQL(ctx)
	if err != nil {
		return "", false
	}
	var b strings.Builder // LIMIT is a parameter, so args is never empty
	b.WriteString(text)
	for _, a := range args {
		fmt.Fprintf(&b, "\x00%T:%v", a, a)
	}
	return b.String(), true
}

// canCompare reports whether == on v cannot panic: what
// reflect.ValueOf(v).Comparable() reports, which checks the dynamic value,
// since a struct or an array of a comparable type may hold an interface
// whose value is a slice or a map. It is checked per value, not on the whole
// key, and through the type for any kind but a struct or an array, where
// both say the same: Value.Comparable allocates.
func canCompare(v any) bool {
	t := reflect.TypeOf(v)
	switch {
	case t == nil:
		return true
	case t.Kind() == reflect.Struct || t.Kind() == reflect.Array:
		return reflect.ValueOf(v).Comparable()
	}
	return t.Comparable()
}

// loadBatch returns b's rows: each owner's, rows of owner, read by
// q.LoadEach(ctx, owner, edge, each, owners) on the first call. The first
// keep(rows) rows of each owner become one group.
func loadBatch(ctx context.Context, b *gqlcore.Batch, q untyped.TableQuery, owner untyped.Table, edge, each int, owners []any, keep func(untyped.Rows) int) ([]untyped.Rows, error) {
	v, err := b.Rows(ctx, func() (any, error) {
		parts, err := q.LoadEach(ctx, owner, edge, each, owners)
		if err != nil {
			return nil, err
		}
		// Owners with one key may get one run of rows: a row is added once.
		seen := map[any]bool{}
		var group []any
		for _, p := range parts {
			for k := range keep(p) {
				if x := p.At(k); !seen[x] {
					seen[x] = true
					group = append(group, x)
				}
			}
		}
		gqlcore.Add(ctx, group)
		return parts, nil
	})
	if err != nil {
		return nil, err
	}
	return v.([]untyped.Rows), nil
}
