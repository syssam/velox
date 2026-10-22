package gqlimpl

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/99designs/gqlgen/graphql"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/contrib/graphql/veloxgql"
	"github.com/syssam/velox2/contrib/graphql/veloxgql/internal/shared"
)

// The edges batch: the first edge field gqlgen resolves on any row of a
// group (the rows one statement read, or veloxgql.Batch was given) reads
// that edge for every row of the group, in one statement, and the rows it
// reads are a group in turn. So a nested query sends one statement per edge
// per level, whatever its rows.
//
// A batch is keyed by its group, the edge's field
// (graphql.GetFieldContext(ctx).Field.Field) and its callers' row scope
// (velox.RowScopeOf: the tenant, and the AllTenants and SkipPolicy reasons,
// of the context each passes to the edge function). So its arguments are
// the same for every row (one field, one set of variables), two aliases are
// two batches, and a row whose resolver's context a field middleware or a
// directive scopes otherwise is in a batch of its own scope. The first
// caller reads for all, under the context it passed to the edge function,
// on the DB of its query (the one veloxgql.DBFrom reads); the others wait
// on the batch's sync.Once, never for one another, so a mutation's
// resolvers, which the extension runs one at a time, batch too. A waiter
// does not watch its own context's cancellation, only the load's: every
// resolver of a response has the request's. A batch's error is every row's.

// EdgeConnection returns c, a connection over a query of every T that
// XConnectionOf made with the edge's arguments, as the connection of x's
// edge r. When x is in a group, its page is read with the pages of every
// row of the group in one statement (velox.LoadEach, with LimitEach of the
// page size + 1 and c's order and cursor), and its totalCount with theirs
// (velox.CountEach), on the first call of either for any row, under the
// context its EdgeConnection was given; the rows read become a group.
// Otherwise its queries are c's, filtered by r.Of(x). A LoadedConnection,
// which runs no query, is returned as it is.
func EdgeConnection[E, T any](ctx context.Context, c *Connection[T], x *E, r velox.Rel[E, T]) *Connection[T] {
	if c.isLoaded {
		return c
	}
	b, owners, i := batchOf(ctx, x)
	if b == nil {
		c.query = c.query.Where(r.Of(x))
		return c
	}
	// The batch reads under the context of the edge's field, whose row scope
	// keys it.
	n := c.n
	c.edge = &edge[T]{
		rows: func(q velox.Query[T]) ([]T, error) {
			// The page size and the cursor are the field's: the same in the
			// connection of every row of the group.
			parts, err := loadBatch(ctx, b, q.Limit(0).MaxRows(0), r.LimitEach(n+1), owners, func(rows []T) int { return min(len(rows), n) })
			if err != nil {
				return nil, err
			}
			return parts[i], nil
		},
		count: func(q velox.Query[T]) (int, error) {
			counts, err := b.Counts(func() ([]int64, error) { return velox.CountEach(ctx, q.Limit(0), r, owners) })
			if err != nil {
				return 0, err
			}
			return int(counts[i]), nil
		},
	}
	return c
}

// EdgeOne returns x's node of the to-one edge r, or nil: read with the
// other rows of x's group in one statement (LoadEach, LimitEach(1)), or
// alone with q.Where(r.Of(x)).First. q selects the target's columns.
//
// The node read alone is a group of its own, and so are the nodes of a
// batch: their edges batch.
func EdgeOne[E, T any](ctx context.Context, q velox.Query[T], x *E, r velox.Rel[E, T]) (*T, error) {
	b, owners, i := batchOf(ctx, x)
	if b == nil {
		v, err := q.Where(r.Of(x)).First(ctx)
		if errors.Is(err, velox.ErrNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		shared.Add(ctx, []*T{v})
		return v, nil
	}
	parts, err := loadBatch(ctx, b, q, r.LimitEach(1), owners, func(rows []T) int { return len(rows) })
	if err != nil {
		return nil, err
	}
	if len(parts[i]) == 0 {
		return nil, nil
	}
	return &parts[i][0], nil
}

// EdgeList returns x's nodes of the list edge r, in key order, batched as
// EdgeOne is, with LimitEach of MaxList + 1: an owner with more than
// MaxList fails with velox.ErrTooManyRows, in an error naming edge
// ("Post.reviews"), and the others do not.
//
// MaxList is that of veloxgql.LimitsFrom(ctx). Read alone, x's nodes are
// one LoadEach of x, a group of their own.
func EdgeList[E, T any](ctx context.Context, q velox.Query[T], x *E, r velox.Rel[E, T], edge string) ([]*T, error) {
	most := veloxgql.LimitsFrom(ctx).MaxList
	rel := r.LimitEach(most + 1)
	keep := func(rows []T) int {
		if len(rows) > most {
			return 0
		}
		return len(rows)
	}
	b, owners, i := batchOf(ctx, x)
	if b == nil {
		b, owners, i = new(shared.Batch), []*E{x}, 0
	}
	parts, err := loadBatch(ctx, b, q, rel, owners, keep)
	if err != nil {
		return nil, fmt.Errorf("veloxgql: %s: %w", edge, err)
	}
	rows := parts[i]
	if len(rows) > most {
		return nil, fmt.Errorf("veloxgql: %s: %w: more than %d, the MaxList of the limits", edge, velox.ErrTooManyRows, most)
	}
	out := make([]*T, len(rows))
	for k := range rows {
		out[k] = &rows[k]
	}
	return out, nil
}

// edge is a Connection's edge mode: its page and count are its part of a
// batch.
type edge[T any] struct {
	// rows returns the connection's rows, q being its query in its order,
	// after its cursor.
	rows func(q velox.Query[T]) ([]T, error)
	// count returns the connection's totalCount, q being its query.
	count func(q velox.Query[T]) (int, error)
}

// batchOf returns the batch of the edge field of ctx, for x's group and
// ctx's row scope, the group's rows and x's index in them; a nil batch when
// ctx is in no gqlgen field or x is in no group of ctx's table.
func batchOf[E any](ctx context.Context, x *E) (*shared.Batch, []*E, int) {
	fc := graphql.GetFieldContext(ctx)
	if fc == nil || fc.Field.Field == nil {
		return nil, nil, 0
	}
	g, rows, i, ok := shared.Find(shared.TableFrom(ctx), x)
	if !ok {
		return nil, nil, 0
	}
	scope := velox.RowScopeOf(ctx)
	if scope.Tenant != nil && !reflect.TypeOf(scope.Tenant).Comparable() {
		// Never equal to another's: a batch of its own.
		scope.Tenant = new(byte)
	}
	return g.Batch(fc.Field.Field, fc.Field.Selections, scope), rows, i
}

// loadBatch returns b's rows: each owner's, read by velox.LoadEach(ctx, q,
// r, owners) on the first call. The first keep(rows) rows of each owner
// become one group.
func loadBatch[E, T any](ctx context.Context, b *shared.Batch, q velox.Query[T], r velox.Rel[E, T], owners []*E, keep func([]T) int) ([][]T, error) {
	v, err := b.Rows(func() (any, error) {
		parts, err := velox.LoadEach(ctx, q, r, owners)
		if err != nil {
			return nil, err
		}
		// Owners with one key may get one slice: a row is added once.
		seen := map[*T]bool{}
		var group []*T
		for _, p := range parts {
			for k := range keep(p) {
				if x := &p[k]; !seen[x] {
					seen[x] = true
					group = append(group, x)
				}
			}
		}
		shared.Add(ctx, group)
		return parts, nil
	})
	if err != nil {
		return nil, err
	}
	return v.([][]T), nil
}
