package runtime

import (
	"context"

	"github.com/syssam/velox/dialect/sql"
)

// =============================================================================
// Edge Load Options
// =============================================================================

// LoadOption configures an edge loaded through a generated query's
// WithEdgeLoad method.
type LoadOption func(*LoadConfig)

// LoadConfig holds configuration for eager loading an edge.
type LoadConfig struct {
	// Predicates filter the loaded rows of every parent alike.
	Predicates []func(*sql.Selector)
	// Limit caps the rows loaded for EACH parent of a to-many edge — never
	// the total across parents. It is ignored on a to-one edge.
	Limit *int
	// Orders sort the loaded rows; on a limited to-many edge they also rank
	// the rows each parent keeps, with the primary key as the final tiebreak.
	Orders []func(*sql.Selector)
	// Fields project the edge query onto these columns. The primary key and
	// the foreign keys the loader needs are always selected.
	Fields []string
	// Edges are nested edges to load on the edge query, by edge name.
	Edges map[string][]LoadOption
}

// NewLoadConfig returns the LoadConfig the given options describe.
func NewLoadConfig(opts ...LoadOption) *LoadConfig {
	c := &LoadConfig{}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Limit keeps at most n rows per parent of a to-many edge. The limit is
// applied inside the database with ROW_NUMBER() OVER (PARTITION BY <parent
// key>), so loading an edge for many parents stays one query and every
// parent gets its own n rows — a plain LIMIT would cap the total instead.
// Ranking follows the edge query's order, then the primary key.
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
	// Relay indicates a to-many edge exposed as a Relay connection.
	Relay bool
	// PagesLoaded reports that the connection's generated entity method
	// answers a page without cursors, filter or order from the eager-loaded
	// edge (gqlrelay.PageLoaded). The collector eager-loads a connection
	// only then; otherwise every row would query it again anyway.
	PagesLoaded bool
	// FKColumns lists the foreign-key columns of THIS entity's table the
	// edge needs (the key of an edge that owns it); empty when the key lives
	// on the other table or a join table. Added to the parent's projection.
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
