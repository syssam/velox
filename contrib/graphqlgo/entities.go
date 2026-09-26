package graphqlgo

import (
	"context"
	"strconv"

	"github.com/syssam/graphql-go/fed"
)

// Entities resolves a federated entity type from velox for a graphql-go
// subgraph: every representation of typename the router sends in one
// _entities call is answered by one load, however many there are.
//
//	fed.Subgraph(sdl,
//		graphqlgo.Entities("Product", graphqlgo.IntKey("id"),
//			func(ctx context.Context, ids []int) ([]*entity.Product, error) {
//				q, err := client.Product.Query().Where(product.IDIn(ids...)).CollectFields(ctx, "Product")
//				if err != nil {
//					return nil, err
//				}
//				return q.All(ctx)
//			},
//			func(p *entity.Product) int { return p.ID }),
//	)
//
// A router batches every reference it collected from other subgraphs into
// one request, so a resolver per representation is one query per row of the
// page that referred to them. load receives each key once, in the order first
// seen; its rows are matched back to the representations by keyOf, and one it
// does not return -- absent, or filtered out by a read rule of the client
// load uses -- is null. A representation whose key does not parse is null
// too, not an error, so one bad reference does not fail the others.
//
// CollectFields(ctx, typename) inside load plans the query from what the
// router selected on typename: _entities returns a union, and naming the
// type is what picks its fragment's fields.
func Entities[E any, K comparable](
	typename string,
	key func(fed.Representation) (K, bool),
	load func(ctx context.Context, keys []K) ([]*E, error),
	keyOf func(*E) K,
) fed.Entity {
	return fed.BatchResolver(typename, func(ctx context.Context, reps []fed.Representation) ([]*E, error) {
		out := make([]*E, len(reps))
		keys := make([]K, len(reps))
		valid := make([]bool, len(reps))
		unique := make([]K, 0, len(reps))
		seen := make(map[K]bool, len(reps))
		for i, r := range reps {
			k, ok := key(r)
			if !ok {
				continue
			}
			keys[i], valid[i] = k, true
			if !seen[k] {
				seen[k] = true
				unique = append(unique, k)
			}
		}
		if len(unique) == 0 {
			return out, nil
		}
		rows, err := load(ctx, unique)
		if err != nil {
			return nil, err
		}
		byKey := make(map[K]*E, len(rows))
		for _, row := range rows {
			if row != nil {
				byKey[keyOf(row)] = row
			}
		}
		for i := range reps {
			if valid[i] {
				out[i] = byKey[keys[i]]
			}
		}
		return out, nil
	})
}

// IntKey reads an integer key field. velox keys rows by int, and a router
// sends a key declared ID as a string ("42"), so both forms are read.
func IntKey(field string) func(fed.Representation) (int, bool) {
	return func(r fed.Representation) (int, bool) {
		if n, ok := r.Int(field); ok {
			return int(n), true
		}
		if id, ok := r.ID(field); ok {
			n, err := strconv.Atoi(string(id))
			return n, err == nil
		}
		return 0, false
	}
}
