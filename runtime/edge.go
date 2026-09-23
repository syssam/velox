package runtime

import (
	"context"

	"github.com/syssam/velox/dialect/sql"
)

// =============================================================================
// Edge Load Options
// =============================================================================

// LoadOption configures edge loading.
type LoadOption func(*LoadConfig)

// LoadConfig holds configuration for eager loading an edge.
type LoadConfig struct {
	Predicates []func(*sql.Selector)
	Limit      *int
	Offset     *int
	Orders     []func(*sql.Selector)
	Fields     []string
	Edges      map[string][]LoadOption
}

// Limit sets the maximum number of edges to load.
func Limit(n int) LoadOption {
	return func(c *LoadConfig) {
		c.Limit = &n
	}
}

// Select specifies which fields to load.
func Select(fields ...string) LoadOption {
	return func(c *LoadConfig) {
		c.Fields = append(c.Fields, fields...)
	}
}

// OrderBy adds ordering to the edge load query.
func OrderBy(o ...func(*sql.Selector)) LoadOption {
	return func(c *LoadConfig) {
		c.Orders = append(c.Orders, o...)
	}
}

// WithEdge configures a nested edge to be eagerly loaded.
func WithEdge(name string, opts ...LoadOption) LoadOption {
	return func(c *LoadConfig) {
		if c.Edges == nil {
			c.Edges = make(map[string][]LoadOption)
		}
		c.Edges[name] = opts
	}
}

// =============================================================================
// Edge Metadata
// =============================================================================

// EdgeMeta describes a relationship edge for generic GraphQL field collection.
// It contains enough metadata to build eager-loading queries without importing
// the target entity's package — preventing circular imports in per-entity packages.
//
// Named EdgeMeta (not EdgeDescriptor) to avoid collision with gqlrelay.EdgeDescriptor,
// which serves a different purpose (Relay node introspection).
type EdgeMeta struct {
	// Name is the edge name as it appears in the schema (e.g., "employees").
	Name string
	// Target is the target entity table name (e.g., "employees").
	Target string
	// Unique indicates a single-entity relationship (O2O or M2O).
	Unique bool
	// Relay indicates this edge uses Relay cursor pagination.
	Relay bool
	// FKColumns lists the foreign key columns needed for this edge.
	// These are added to the parent query's SELECT to enable eager loading.
	FKColumns []string
	// Inverse is the back-reference edge name on the target entity (e.g., "user").
	Inverse string
}

// =============================================================================
// Config Context Propagation
// =============================================================================

// configKey is the context key for the runtime Config.
type configKey struct{}

// WithConfigContext returns a new context with the given Config attached.
// Used by generated Noder/Noders to propagate the runtime Config through
// context, so node resolvers can construct entity clients on demand.
func WithConfigContext(ctx context.Context, cfg Config) context.Context {
	return context.WithValue(ctx, configKey{}, cfg)
}

// ConfigFromContext returns the Config from the context, or zero Config if
// not set. Callers must check Config.Driver != nil before use.
func ConfigFromContext(ctx context.Context) Config {
	c, _ := ctx.Value(configKey{}).(Config)
	return c
}

// MaskNotFound returns nil if the error is a NotFoundError, otherwise returns the error as-is.
func MaskNotFound(err error) error {
	if IsNotFound(err) {
		return nil
	}
	return err
}
