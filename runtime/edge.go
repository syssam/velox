package runtime

import (
	"context"
	"fmt"
	"slices"
	"strings"

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

// ApplyEntityPolicy applies the query privacy policy of entity to s, a
// selector over that entity's table that has no query object behind it. It
// is called by generated code in two places:
//
//   - edge predicates — HasPosts(), HasPostsWith(...) — for the subquery
//     they select the target rows through, when the target declares a Policy;
//   - the old-value loader behind OldXxx, which reads the row a hook is
//     about to update; it applies the policy (as Ent's Client().X.Get does)
//     but, deliberately, not the interceptors.
//
// Without it the subquery read the target table unscoped: no query object
// existed, so the target's policy never ran, and a filter such as
// users(where: {hasPostsWith: {title: "…"}}) answered whether rows the
// caller may not read exist. A policy that filters (FilterFunc) narrows the
// subquery to the rows it allows; a policy that denies records its error on
// s, which fails the whole query — the same outcome as eager-loading a
// denied edge.
func ApplyEntityPolicy(s *sql.Selector, entity string) {
	policy := EntityPolicy(entity)
	if policy == nil {
		return
	}
	// Policies may filter through edges themselves (Post visible if its
	// author is): a cycle — User's filter through posts, Post's through
	// author — would re-evaluate forever. Fail closed instead.
	ctx := s.Context()
	chain, _ := ctx.Value(edgePolicyChainKey{}).([]string)
	if slices.Contains(chain, entity) {
		s.AddError(fmt.Errorf("velox: privacy policies form a cycle through edge predicates: %s",
			strings.Join(append(slices.Clone(chain), entity), " -> ")))
		return
	}
	ctx = context.WithValue(ctx, edgePolicyChainKey{}, append(slices.Clone(chain), entity))
	s.WithContext(ctx)
	q := NewEntityQuery(entity, Config{})
	if err := policy.EvalQuery(ctx, q); err != nil {
		s.AddError(err)
		return
	}
	if r, ok := q.(QueryReader); ok {
		for _, p := range r.GetPredicates() {
			p(s)
		}
	}
}

// edgePolicyChainKey carries the entities whose policies are being applied
// to nested edge subqueries, to detect a cycle between policies.
type edgePolicyChainKey struct{}
