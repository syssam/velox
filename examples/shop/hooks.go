//go:build ignore

// This file contains example hooks and interceptors for the shop application.
// Copy and modify these examples for your own use.

package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"example.com/shop/velox"
	"example.com/shop/velox/intercept"
)

// =============================================================================
// MUTATION HOOKS
// =============================================================================

// LoggingHook logs all mutations for auditing purposes.
func LoggingHook() velox.Hook {
	return func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
			start := time.Now()

			// Log before mutation
			log.Printf("[MUTATION] %s.%s starting", m.Type(), m.Op().String())

			// Execute mutation
			v, err := next.Mutate(ctx, m)

			// Log after mutation
			duration := time.Since(start)
			if err != nil {
				log.Printf("[MUTATION] %s.%s failed after %v: %v",
					m.Type(), m.Op().String(), duration, err)
			} else {
				log.Printf("[MUTATION] %s.%s completed in %v",
					m.Type(), m.Op().String(), duration)
			}

			return v, err
		})
	}
}

// SoftDeleteHook intercepts delete operations and converts them to soft deletes.
// Requires a "deleted_at" field on the entity.
func SoftDeleteHook() velox.Hook {
	return func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
			// Only intercept delete operations
			if m.Op() != velox.OpDelete && m.Op() != velox.OpDeleteOne {
				return next.Mutate(ctx, m)
			}

			// Convert to update with deleted_at set
			// This is entity-specific - shown as a pattern
			log.Printf("[SOFT DELETE] Converting delete to soft delete for %s", m.Type())

			// Continue with normal delete for entities without soft delete
			return next.Mutate(ctx, m)
		})
	}
}

// TimestampHook automatically sets created_at and updated_at fields.
func TimestampHook() velox.Hook {
	return func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
			now := time.Now()

			switch m.Op() {
			case velox.OpCreate:
				// Set created_at if field exists
				if setter, ok := m.(interface{ SetCreatedAt(time.Time) }); ok {
					setter.SetCreatedAt(now)
				}
				// Set updated_at if field exists
				if setter, ok := m.(interface{ SetUpdatedAt(time.Time) }); ok {
					setter.SetUpdatedAt(now)
				}
			case velox.OpUpdate, velox.OpUpdateOne:
				// Set updated_at if field exists
				if setter, ok := m.(interface{ SetUpdatedAt(time.Time) }); ok {
					setter.SetUpdatedAt(now)
				}
			}

			return next.Mutate(ctx, m)
		})
	}
}

// ValidationHook validates mutations before execution.
func ValidationHook() velox.Hook {
	return func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
			// Example: Validate email format on User mutations
			if m.Type() == "User" {
				if email, exists := m.Field("email"); exists {
					if emailStr, ok := email.(string); ok {
						if !isValidEmail(emailStr) {
							return nil, fmt.Errorf("invalid email format: %s", emailStr)
						}
					}
				}
			}

			return next.Mutate(ctx, m)
		})
	}
}

func isValidEmail(email string) bool {
	// Simple validation - use a proper library in production
	return len(email) > 3 && contains(email, "@") && contains(email, ".")
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// =============================================================================
// QUERY INTERCEPTORS
// =============================================================================

// TenantInterceptor filters all queries by tenant_id for multi-tenancy.
func TenantInterceptor(tenantID string) velox.Interceptor {
	return intercept.TraverseFunc(func(ctx context.Context, q intercept.Query) error {
		// Add tenant filter to all queries
		q.WhereP(func(s *velox.Selector) {
			s.Where(velox.EQ("tenant_id", tenantID))
		})
		return nil
	})
}

// SoftDeleteInterceptor filters out soft-deleted records.
func SoftDeleteInterceptor() velox.Interceptor {
	return intercept.TraverseFunc(func(ctx context.Context, q intercept.Query) error {
		// Exclude soft-deleted records
		q.WhereP(func(s *velox.Selector) {
			s.Where(velox.IsNull("deleted_at"))
		})
		return nil
	})
}

// LimitInterceptor adds a maximum limit to prevent large queries.
func LimitInterceptor(maxLimit int) velox.Interceptor {
	return intercept.TraverseFunc(func(ctx context.Context, q intercept.Query) error {
		q.Limit(maxLimit)
		return nil
	})
}

// LoggingInterceptor logs all query operations.
func LoggingInterceptor() velox.Interceptor {
	return intercept.Func(func(ctx context.Context, q intercept.Query) error {
		log.Printf("[QUERY] %s query started", q.Type())
		return nil
	})
}

// CachingInterceptor example (requires external cache implementation).
type CachingInterceptor struct {
	cache Cache
	ttl   time.Duration
}

type Cache interface {
	Get(key string) (any, bool)
	Set(key string, value any, ttl time.Duration)
}

func (c *CachingInterceptor) Intercept(next velox.Querier) velox.Querier {
	return velox.QuerierFunc(func(ctx context.Context, q velox.Query) (velox.Value, error) {
		// Generate cache key from query
		key := fmt.Sprintf("%T:%v", q, q) // Simplified - use proper key generation

		// Check cache
		if cached, ok := c.cache.Get(key); ok {
			log.Printf("[CACHE] Hit for %s", key)
			return cached.(velox.Value), nil
		}

		// Execute query
		result, err := next.Query(ctx, q)
		if err != nil {
			return nil, err
		}

		// Cache result
		c.cache.Set(key, result, c.ttl)
		log.Printf("[CACHE] Miss for %s, cached result", key)

		return result, err
	})
}

// =============================================================================
// USAGE EXAMPLES
// =============================================================================

func setupClient() *velox.Client {
	client, err := velox.Open("sqlite", "file:shop.db?cache=shared&_pragma=foreign_keys(1)")
	if err != nil {
		log.Fatalf("failed opening connection: %v", err)
	}

	// Register global hooks (applied to all entities)
	client.Use(
		LoggingHook(),
		TimestampHook(),
		ValidationHook(),
	)

	// Register global interceptors (applied to all queries)
	client.Intercept(
		LimitInterceptor(1000), // Prevent unbounded queries
		LoggingInterceptor(),
	)

	return client
}

func setupClientWithTenantIsolation(tenantID string) *velox.Client {
	client, err := velox.Open("sqlite", "file:shop.db?cache=shared&_pragma=foreign_keys(1)")
	if err != nil {
		log.Fatalf("failed opening connection: %v", err)
	}

	// Add tenant isolation interceptor
	client.Intercept(TenantInterceptor(tenantID))

	// Add soft delete interceptor
	client.Intercept(SoftDeleteInterceptor())

	return client
}

// Per-entity hooks example
func setupEntityHooks(client *velox.Client) {
	// Add hook only to User entity
	client.User.Use(func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
			log.Printf("[USER] Mutation: %s", m.Op().String())
			return next.Mutate(ctx, m)
		})
	})

	// Add hook only to Order entity
	client.Order.Use(func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
			if m.Op() == velox.OpCreate {
				log.Printf("[ORDER] New order created")
			}
			return next.Mutate(ctx, m)
		})
	})

	// Add interceptor only to Product queries
	client.Product.Intercept(intercept.TraverseFunc(func(ctx context.Context, q intercept.Query) error {
		// Only show active products by default
		q.WhereP(func(s *velox.Selector) {
			s.Where(velox.EQ("active", true))
		})
		return nil
	}))
}

// Velox selector types (simplified for example)
type Selector struct{}

func (s *Selector) Where(p any) {}

func EQ(field string, value any) any { return nil }
func IsNull(field string) any        { return nil }
func Not(p any) any                  { return nil }
