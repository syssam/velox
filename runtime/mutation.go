package runtime

import (
	"context"
	"sync/atomic"

	"github.com/syssam/velox"
)

// Op is an alias for velox.Op so generated mutations can satisfy the velox.Mutation interface.
type Op = velox.Op

// Mutation operation constants matching velox.Op values.
const (
	OpCreate    = velox.OpCreate
	OpUpdate    = velox.OpUpdate
	OpUpdateOne = velox.OpUpdateOne
	OpDelete    = velox.OpDelete
	OpDeleteOne = velox.OpDeleteOne
)

// =============================================================================
// Interceptor / Hook / Mutator type aliases
// =============================================================================

// Type aliases from core velox package.
// Using aliases ensures generated entity sub-packages can import these types
// from runtime/ directly, without needing the model/ intermediate package.
type (
	// Hook is a function that wraps a Mutator to add behavior before/after mutations.
	Hook = velox.Hook
	// Mutator is the interface wrapping the Mutate method.
	Mutator = velox.Mutator
	// MutateFunc is an adapter to allow ordinary functions as Mutator.
	MutateFunc = velox.MutateFunc
	// Mutation is the interface for accessing mutation state.
	Mutation = velox.Mutation
	// Interceptor is a function that wraps a Querier to add behavior before/after queries.
	Interceptor = velox.Interceptor
	// Querier is the interface wrapping the Query method.
	Querier = velox.Querier
	// QuerierFunc is an adapter to allow ordinary functions as Querier.
	QuerierFunc = velox.QuerierFunc
	// InterceptFunc is an adapter to allow ordinary functions as Interceptor.
	InterceptFunc = velox.InterceptFunc
	// Value represents a dynamic value returned by mutations or queries.
	Value = velox.Value
	// Query represents a query builder.
	Query = velox.Query
	// Traverser is the interface for traversing query nodes.
	Traverser = velox.Traverser
	// TraverseFunc is an adapter for ordinary functions as Traverser.
	TraverseFunc = velox.TraverseFunc
)

// =============================================================================
// GraphQL Field Collection
// =============================================================================

// FieldCollectable is implemented by query builders that support GraphQL field collection.
// Both the self-contained query types (in query/ package) and QueryBase implement this.
type FieldCollectable interface {
	// GetIDColumn returns the primary key column name.
	GetIDColumn() string
	// GetCtx returns the query context for field projection.
	GetCtx() *QueryContext
	// WithEdgeLoad adds an edge to be eagerly loaded by name.
	WithEdgeLoad(name string, opts ...LoadOption)
}

// fieldCollector holds the registered GraphQL field collection function.
// Set by contrib/graphql at init time when GraphQL support is active.
// Uses atomic.Pointer for safe concurrent access (even though init() runs
// before goroutines, tests may register collectors concurrently).
var fieldCollector atomic.Pointer[func(ctx context.Context, q FieldCollectable, fields map[string]string, edges map[string]EdgeMeta, satisfies []string) error]

// SetFieldCollector registers the GraphQL field collection function.
// Called by contrib/graphql's init() when GraphQL support is active.
func SetFieldCollector(fn func(ctx context.Context, q FieldCollectable, fields map[string]string, edges map[string]EdgeMeta, satisfies []string) error) {
	fieldCollector.Store(&fn)
}

// CollectFields performs GraphQL field collection if a collector is registered.
// In ORM-only mode (no collector registered) this is a no-op and always returns nil.
// When contrib/graphql is imported, its init() registers a collector that inspects
// the gqlgen FieldContext and configures column projection and edge eager-loading.
// The satisfies parameter specifies additional GraphQL interface names the entity
// implements (for union/interface type resolution).
func CollectFields(ctx context.Context, q FieldCollectable, fields map[string]string, edges map[string]EdgeMeta, satisfies ...string) error {
	fn := fieldCollector.Load()
	if fn == nil {
		return nil
	}
	return (*fn)(ctx, q, fields, edges, satisfies)
}

// CollectMeta holds GraphQL field collection metadata for an entity.
// Used by contrib/graphql to map GraphQL field/edge names to database columns
// and edge configurations for efficient query projection and eager loading.
//
// Registered per-entity at init() time by generated gql_collection.go files.
// This replaces the FieldColumns/Edges fields that were previously on TypeInfo.
type CollectMeta struct {
	// FieldColumns maps GraphQL field names to database column names.
	FieldColumns map[string]string
	// Edges maps GraphQL edge names to edge metadata for eager loading.
	Edges map[string]EdgeMeta
}
