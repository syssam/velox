package gqlimpl

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"unsafe"

	"github.com/99designs/gqlgen/graphql"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
)

// The edges batch: the first edge field gqlgen resolves on any row of a
// group (the rows one statement read, or veloxgql.Batch was given) reads
// that edge for every row of the group, in one statement, and the rows it
// reads are a group in turn. So a nested query sends one statement per edge
// per level, whatever its rows.
//
// A caller batches only from the edge's own field: the field of ctx
// (graphql.GetFieldContext) must be the edge the generated code names
// ("Post.likes": its object and its field's name). A call from any other
// field, an application's resolver calling x.ChildrenGQL, reads alone: one
// statement for its row, by its own arguments.
//
// A batch is keyed by its group, the edge's field (Field.Field, so two aliases
// are two batches) and its selections, the edge (its name, the relation and the
// target type), the DB veloxgql.DBFromContext reads, EdgeList's MaxList, the
// callers' row scope (velox.RowScopeFromContext: the scope values, the WithoutPolicy
// reason and the WithShard shard, of the context each passes
// to the edge function) and the value of veloxgql.WithBatchScope's function, if
// the extension has one. A row whose resolver's context a field middleware or a
// directive scopes otherwise is in a batch of its own scope; a key that cannot
// be compared (reflect.Value.Comparable) reads alone. The batch keeps its first
// caller's arguments, and a caller whose arguments differ (reflect.DeepEqual: a
// resolver that computes them per row) reads alone.
//
// The first caller reads for all, under the context it passed to the edge
// function, on the DB of its query; the others wait on the batch's
// sync.Once, never for one another, so a mutation's resolvers, which the
// extension runs one at a time, batch too. Any other value of the context
// that a Read policy or a Rewrite or Around hook reads (the viewer, a role)
// is the first caller's for every row: it must be the same for every row of
// the field, or be in WithBatchScope's value. A waiter does not watch its
// own context's cancellation, only the load's: every resolver of a response
// has the request's. A batch's error is every row's.

// EdgeConnection returns c, a connection over a query of every node of the
// edge's target that XConnectionOf made with args, the edge's arguments as
// one value, as the connection of the edge of x, a *E of owner's entity E,
// through owner's relation edge, whose GraphQL field is field
// ("User.posts"). When x is in a group and ctx is that field's, its page is
// read with the pages of every row of the group whose arguments are args, in
// one statement (TableQuery.LoadEach, of the page size + 1 per owner, in c's
// order and after its cursor), and its totalCount with theirs
// (TableQuery.CountEach), on the first call of either for any row, under the
// context its EdgeConnection was given; the rows read become a group.
// Otherwise its queries are c's, filtered by the relation of x. A base query
// with a lock (ForUpdate, ForShare) is refused when batched: a per-owner read
// locks nothing.
func EdgeConnection(ctx context.Context, c *Connection, owner *Conn, x any, edge int, field string, args any) *Connection {
	return edgeConnection(ctx, c, owner.table(), rowOf(owner, x), edge, field, args)
}

// EdgeOne returns the node of the to-one edge of x, a *E of owner's entity E,
// through owner's relation edge, whose GraphQL field is field
// ("Post.author"), as a *T of target's entity T in an any, or nil. It reads,
// on the DB veloxgql.DBFromContext reads from ctx, only the columns the
// field's selection of target's type needs (Selected, Narrow,
// target.Columns): with the other rows of x's group in one statement
// (LoadEach, of one per owner) when ctx is that field's, or alone. The node
// read alone is a group of its own, and so are the nodes of a batch: their
// edges batch.
func EdgeOne(ctx context.Context, target, owner *Conn, x any, edge int, field string) (any, error) {
	q, err := edgeQuery(ctx, target)
	if err != nil {
		return nil, err
	}
	p, err := edgeOne(ctx, q, owner.table(), rowOf(owner, x), edge, field)
	if err != nil {
		return nil, err
	}
	return nodeOf(target, p), nil
}

// EdgeList returns the nodes of the list edge of x, a *E of owner's entity
// E, through owner's relation edge, whose GraphQL field is field
// ("Post.reviews"), as a []*T of target's entity T in an any, in key order.
// It reads them as EdgeOne reads its node, batched, with at most MaxList + 1
// per owner, whatever the DB's WithMaxRows: an owner with more than MaxList
// fails with velox.ErrTooManyRows, in an error naming field, and the others
// do not. MaxList is that of veloxgql.LimitsFromContext(ctx), or maxList, the
// edge's graphql.MaxList, when that is lower and not 0. Read alone, x's nodes
// are one LoadEach of x, a group of their own.
func EdgeList(ctx context.Context, target, owner *Conn, x any, edge int, field string, maxList int) (any, error) {
	q, err := edgeQuery(ctx, target)
	if err != nil {
		return nil, err
	}
	ps, err := edgeList(ctx, q, owner.table(), rowOf(owner, x), edge, field, maxList)
	if err != nil {
		return nil, err
	}
	return nodesOf(target, ps), nil
}

// edgeQuery is the query of target's rows that an edge reads, on the DB in
// ctx, reading only the columns the selection of the field in ctx needs.
func edgeQuery(ctx context.Context, target *Conn) (velox.TableQuery, error) {
	db, err := veloxgql.DBFromContext(ctx)
	if err != nil {
		return velox.TableQuery{}, err
	}
	q := db.QueryTable(target.table())
	if fields, ok := Selected(ctx, target.Type); ok {
		q = Narrow(q, target.Columns(fields))
	}
	return q, nil
}

// edgeConnection is EdgeConnection of x, a row of owner: unbatched, its
// queries are c's, filtered by owner.Of(edge, …, x).
func edgeConnection(ctx context.Context, c *Connection, owner velox.Table, x unsafe.Pointer, edge int, field string, args any) *Connection {
	b, ptrs, i := batchOf(ctx, c.query.Table(), x, edge, field, 0, args)
	if b == nil {
		c.query = c.query.Where(owner.Of(edge, c.query.Table(), x))
		return c
	}
	// The batch reads under the context of the edge's field, whose row scope
	// keys it.
	n := c.n
	c.edge = &edgeMode{
		rows: func(q velox.TableQuery) (velox.TableRows, error) {
			// The page size and the cursor are the batch's arguments: the
			// same in the connection of every caller of the batch.
			parts, err := loadBatch(ctx, b, q.Limit(0).MaxRows(0), owner, edge, n+1, ptrs, func(r velox.TableRows) int { return min(r.Len(), n) })
			if err != nil {
				return velox.TableRows{}, err
			}
			return parts[i], nil
		},
		count: func(q velox.TableQuery) (int, error) {
			counts, err := b.Counts(func() ([]int64, error) { return q.Limit(0).CountEach(ctx, owner, edge, ptrs) })
			if err != nil {
				return 0, err
			}
			return int(counts[i]), nil
		},
	}
	return c
}

// edgeOne returns the node of the to-one edge of x, a row of owner, through
// owner's relation edge, whose GraphQL field is field ("Post.author"), or
// nil: read with the other rows of x's group in one statement (LoadEach, of
// one per owner) when ctx is that field's, or alone with
// q.Where(owner.Of(edge, …, x)).First. q, a query of the edge's target,
// selects the target's columns.
//
// The node read alone is a group of its own, and so are the nodes of a
// batch: their edges batch.
func edgeOne(ctx context.Context, q velox.TableQuery, owner velox.Table, x unsafe.Pointer, edge int, field string) (unsafe.Pointer, error) {
	b, owners, i := batchOf(ctx, q.Table(), x, edge, field, 0, nil)
	if b == nil {
		v, err := q.Where(owner.Of(edge, q.Table(), x)).First(ctx)
		if velox.IsNotFound(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		gqlcore.Add(ctx, []unsafe.Pointer{v})
		return v, nil
	}
	parts, err := loadBatch(ctx, b, q, owner, edge, 1, owners, velox.TableRows.Len)
	if err != nil {
		return nil, err
	}
	if parts[i].Len() == 0 {
		return nil, nil
	}
	return parts[i].At(0), nil
}

// edgeList returns the nodes of the list edge of x, a row of owner, through
// owner's relation edge, whose GraphQL field is field ("Post.reviews"), in
// key order, batched as edgeOne is, with at most MaxList + 1 per owner,
// whatever the DB's WithMaxRows: an owner with more than MaxList fails with
// velox.ErrTooManyRows, in an error naming field, and the others do not.
//
// MaxList is that of veloxgql.LimitsFromContext(ctx), or maxList, the edge's
// graphql.MaxList, when that is lower and not 0. Read alone, x's nodes are one
// LoadEach of x, a group of their own.
func edgeList(ctx context.Context, q velox.TableQuery, owner velox.Table, x unsafe.Pointer, edge int, field string, maxList int) ([]unsafe.Pointer, error) {
	most := veloxgql.LimitsFromContext(ctx).MaxList
	if maxList > 0 {
		most = min(most, maxList)
	}
	keep := func(rows velox.TableRows) int {
		if rows.Len() > most {
			return 0
		}
		return rows.Len()
	}
	b, owners, i := batchOf(ctx, q.Table(), x, edge, field, most, nil)
	if b == nil {
		b, owners, i = new(gqlcore.Batch), []unsafe.Pointer{x}, 0
	}
	parts, err := loadBatch(ctx, b, q, owner, edge, most+1, owners, keep)
	if err != nil {
		return nil, fmt.Errorf("veloxgql: %s: %w", field, err)
	}
	rows := parts[i]
	if rows.Len() > most {
		return nil, fmt.Errorf("veloxgql: %s: %w: more than %d, the edge's MaxList", field, velox.ErrTooManyRows, most)
	}
	out := make([]unsafe.Pointer, rows.Len())
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
	rows func(q velox.TableQuery) (velox.TableRows, error)
	// count returns the connection's totalCount, q being its query.
	count func(q velox.TableQuery) (int, error)
}

// batchKey is what the callers of a batch share, besides its group, its
// field and its selections: see the edges batch above.
type batchKey struct {
	field  string            // the edge's GraphQL field, "Post.likes"
	rel    int               // the relation's index on the owner
	target *velox.EntityInfo // the relation's target
	db     *velox.DB         // veloxgql.DBFromContext's
	most   int               // EdgeList's MaxList; 0 for the others
	scope  velox.RowScope
	app    any // veloxgql.WithBatchScope's value
}

// batchOf returns the batch of x's edge, through the relation edge to
// target, whose GraphQL field is field, read with the arguments args (most
// being EdgeList's MaxList), for x's group and ctx's key, the group's rows
// and x's index in them. The batch is nil, and x reads alone, when ctx is in
// no gqlgen field or not in field, when x is in no group of ctx's table,
// when the key cannot be compared, or when the batch was made with other
// arguments.
func batchOf(ctx context.Context, target velox.Table, x unsafe.Pointer, edge int, field string, most int, args any) (*gqlcore.Batch, []unsafe.Pointer, int) {
	fc := graphql.GetFieldContext(ctx)
	if fc == nil || fc.Field.Field == nil {
		return nil, nil, 0
	}
	if typ, name, _ := strings.Cut(field, "."); fc.Object != typ || fc.Field.Name != name {
		return nil, nil, 0
	}
	t := gqlcore.TableFromContext(ctx)
	g, ps, i, ok := gqlcore.Find(t, x)
	if !ok {
		return nil, nil, 0
	}
	db, _ := veloxgql.DBFromContext(ctx)
	key := batchKey{field: field, rel: edge, target: target.Info(), db: db, most: most, scope: velox.RowScopeFromContext(ctx), app: t.Scope(ctx)}
	if !canCompare(key.app) {
		// A batch scope == would panic on: a read of its own.
		return nil, nil, 0
	}
	b := g.Batch(fc.Field.Field, fc.Field.Selections, key, args)
	if b == nil {
		return nil, nil, 0
	}
	return b, ps, i
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
func loadBatch(ctx context.Context, b *gqlcore.Batch, q velox.TableQuery, owner velox.Table, edge, each int, owners []unsafe.Pointer, keep func(velox.TableRows) int) ([]velox.TableRows, error) {
	v, err := b.Rows(func() (any, error) {
		parts, err := q.LoadEach(ctx, owner, edge, each, owners)
		if err != nil {
			return nil, err
		}
		// Owners with one key may get one run of rows: a row is added once.
		seen := map[unsafe.Pointer]bool{}
		var group []unsafe.Pointer
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
	return v.([]velox.TableRows), nil
}
