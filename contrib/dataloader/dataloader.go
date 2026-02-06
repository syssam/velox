// Package dataloader provides generic DataLoader utilities for batch loading entities.
//
// This package is designed to work with any DataLoader implementation such as:
//   - github.com/graph-gophers/dataloader/v7
//   - github.com/vikstrous/dataloadgen
//
// # Basic Usage
//
// Define a loader function for your entity:
//
//	func userBatchFn(ctx context.Context, ids []int) ([]*ent.User, []error) {
//	    users, err := client.User.Query().Where(user.IDIn(ids...)).All(ctx)
//	    if err != nil {
//	        return nil, []error{err}
//	    }
//	    return dataloader.OrderByKeys(ids, users, func(u *ent.User) int { return u.ID })
//	}
//
// # With graph-gophers/dataloader
//
//	loader := dataloader.NewBatchedLoader(userBatchFn)
//	user, err := loader.Load(ctx, userID)()
//
// # Key Extraction
//
// Use KeyFunc to extract IDs from entities:
//
//	type HasID interface {
//	    GetID() int
//	}
//	keyFn := func(u *ent.User) int { return u.ID }
//	ordered := dataloader.OrderByKeys(ids, users, keyFn)
package dataloader

import (
	"context"
	"errors"
)

// ErrNotFound is returned when an entity is not found in a batch result.
var ErrNotFound = errors.New("dataloader: entity not found")

// KeyFunc extracts a key from an entity.
type KeyFunc[K comparable, V any] func(V) K

// BatchFunc is a function that loads a batch of entities by their keys.
type BatchFunc[K comparable, V any] func(ctx context.Context, keys []K) ([]V, []error)

// OrderByKeys reorders entities to match the order of requested keys.
// Missing entities are represented as zero values with corresponding errors.
//
// This is essential for DataLoader because the result slice must:
//   - Have the same length as the input keys
//   - Have results in the same order as the input keys
//
// Example:
//
//	users, _ := client.User.Query().Where(user.IDIn(ids...)).All(ctx)
//	ordered, errs := OrderByKeys(ids, users, func(u *ent.User) int { return u.ID })
func OrderByKeys[K comparable, V any](keys []K, values []V, keyFn KeyFunc[K, V]) ([]V, []error) {
	// Build lookup map
	lookup := make(map[K]V, len(values))
	for _, v := range values {
		lookup[keyFn(v)] = v
	}

	// Build ordered result
	result := make([]V, len(keys))
	errs := make([]error, len(keys))
	for i, key := range keys {
		if v, ok := lookup[key]; ok {
			result[i] = v
		} else {
			errs[i] = ErrNotFound
		}
	}
	return result, errs
}

// OrderByKeysNoError reorders entities to match the order of requested keys.
// Returns zero values for missing entities without errors.
// Use this when missing entities are acceptable (e.g., optional relationships).
func OrderByKeysNoError[K comparable, V any](keys []K, values []V, keyFn KeyFunc[K, V]) []V {
	result, _ := OrderByKeys(keys, values, keyFn)
	return result
}

// GroupByKey groups entities by a key function.
// Useful for one-to-many relationships where multiple entities share the same foreign key.
//
// Example:
//
//	// Load all posts for multiple users
//	posts, _ := client.Post.Query().Where(post.UserIDIn(userIDs...)).All(ctx)
//	grouped := GroupByKey(posts, func(p *ent.Post) int { return p.UserID })
//	// grouped[userID] contains all posts for that user
func GroupByKey[K comparable, V any](values []V, keyFn KeyFunc[K, V]) map[K][]V {
	result := make(map[K][]V)
	for _, v := range values {
		key := keyFn(v)
		result[key] = append(result[key], v)
	}
	return result
}

// OrderGroupsByKeys reorders grouped entities to match the order of requested keys.
// Returns a slice of slices where each inner slice contains entities for that key.
//
// Example:
//
//	posts, _ := client.Post.Query().Where(post.UserIDIn(userIDs...)).All(ctx)
//	grouped := GroupByKey(posts, func(p *ent.Post) int { return p.UserID })
//	ordered := OrderGroupsByKeys(userIDs, grouped)
//	// ordered[i] contains all posts for userIDs[i]
func OrderGroupsByKeys[K comparable, V any](keys []K, groups map[K][]V) [][]V {
	result := make([][]V, len(keys))
	for i, key := range keys {
		result[i] = groups[key]
	}
	return result
}

// PrimeCache primes a DataLoader cache with known values.
// This is useful after mutations to update the cache.
type CachePrimer[K comparable, V any] interface {
	Prime(key K, value V)
}

// PrimeMany primes multiple values into a cache.
func PrimeMany[K comparable, V any](cache CachePrimer[K, V], values []V, keyFn KeyFunc[K, V]) {
	for _, v := range values {
		cache.Prime(keyFn(v), v)
	}
}

// CacheClearer clears values from a DataLoader cache.
type CacheClearer[K comparable] interface {
	Clear(key K)
}

// ClearMany clears multiple keys from a cache.
func ClearMany[K comparable](cache CacheClearer[K], keys []K) {
	for _, key := range keys {
		cache.Clear(key)
	}
}

// ctxKey is the context key for storing DataLoaders.
type ctxKey struct{}

// WithLoaders injects DataLoaders into the context.
// This is useful for GraphQL resolvers or any context-based request handling.
//
// Example:
//
//	ctx := dataloader.WithLoaders(ctx, &Loaders{
//	    UserLoader: NewUserLoader(client),
//	    PostLoader: NewPostLoader(client),
//	})
//
// For HTTP middleware integration (chi, gorilla/mux, net/http):
//
//	func DataLoaderMiddleware(client *ent.Client) func(http.Handler) http.Handler {
//	    return func(next http.Handler) http.Handler {
//	        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
//	            loaders := &Loaders{
//	                UserLoader: NewUserLoader(client),
//	                PostLoader: NewPostLoader(client),
//	            }
//	            ctx := dataloader.WithLoaders(r.Context(), loaders)
//	            next.ServeHTTP(w, r.WithContext(ctx))
//	        })
//	    }
//	}
func WithLoaders[T any](ctx context.Context, loaders T) context.Context {
	return context.WithValue(ctx, ctxKey{}, loaders)
}

// For extracts DataLoaders from context.
//
// Example:
//
//	loaders := dataloader.For[*Loaders](ctx)
//	user, err := loaders.UserLoader.Load(ctx, userID)()
func For[T any](ctx context.Context) T {
	v, _ := ctx.Value(ctxKey{}).(T)
	return v
}

// BatchResult represents the result of a batch load operation.
type BatchResult[V any] struct {
	Value V
	Error error
}

// NewBatchResult creates a new BatchResult.
func NewBatchResult[V any](value V, err error) BatchResult[V] {
	return BatchResult[V]{Value: value, Error: err}
}

// Results converts separate value and error slices into BatchResult slice.
func Results[V any](values []V, errs []error) []BatchResult[V] {
	results := make([]BatchResult[V], len(values))
	for i := range values {
		var err error
		if i < len(errs) {
			err = errs[i]
		}
		results[i] = BatchResult[V]{Value: values[i], Error: err}
	}
	return results
}
