package velox

import (
	"context"
	"strconv"
	"time"
)

// Cache is the interface for caching query results.
// Users should implement this interface with their preferred caching solution
// (e.g., Redis, Memcached, in-memory).
type Cache interface {
	// Get retrieves a value from the cache.
	// Returns nil, nil if the key doesn't exist.
	Get(ctx context.Context, key string) ([]byte, error)

	// Set stores a value in the cache with an optional TTL.
	// If ttl is 0, the value should not expire.
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error

	// Delete removes a value from the cache.
	Delete(ctx context.Context, key string) error

	// DeletePrefix removes all values with the given prefix.
	DeletePrefix(ctx context.Context, prefix string) error

	// Clear removes all values from the cache.
	Clear(ctx context.Context) error
}

// CacheKey generates a cache key for a query.
type CacheKey struct {
	Table      string
	Operation  string
	Predicates string
	OrderBy    string
	Limit      int
	Offset     int
}

// String returns the string representation of the cache key.
// Uses \x00 as separator to avoid collisions with values that may contain ":".
func (k CacheKey) String() string {
	const sep = "\x00"
	return k.Table + sep + k.Operation + sep + k.Predicates + sep + k.OrderBy + sep + strconv.Itoa(k.Limit) + sep + strconv.Itoa(k.Offset)
}
