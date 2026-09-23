package runtime

import (
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
	// Value represents a dynamic value returned by mutations or queries.
	Value = velox.Value
	// Query represents a query builder.
	Query = velox.Query
	// Traverser is the interface for traversing query nodes.
	Traverser = velox.Traverser
)

// =============================================================================
// GraphQL Field Collection
// =============================================================================

// FieldCollectable is implemented by every generated query builder (in the
// query/ package). The GraphQL field collector (gqlrelay.CollectFields, which
// the generated Paginate and CollectFields methods call) drives it to project
// columns and eager-load the edges a GraphQL selection asks for.
type FieldCollectable interface {
	// GetIDColumn returns the primary key column name.
	GetIDColumn() string
	// GetCtx returns the query context for field projection.
	GetCtx() *QueryContext
	// WithEdgeLoad enables eager loading of the named edge, applies opts to
	// the edge query and returns that query, or nil when the query has no
	// edge of that name. Limit in opts applies per parent, not in total.
	WithEdgeLoad(name string, opts ...LoadOption) FieldCollectable
}

// CollectMeta holds GraphQL field collection metadata for an entity.
// Used by the GraphQL field collector to map GraphQL field/edge names to
// database columns and edge configurations for query projection and eager
// loading. Populated per entity at init() time by the generated
// gql_collection.go files.
type CollectMeta struct {
	// FieldColumns maps GraphQL field names to database column names.
	FieldColumns map[string]string
	// Edges maps GraphQL edge names to edge metadata for eager loading.
	Edges map[string]EdgeMeta
	// CollectedFor maps a GraphQL field name that has no column of its own
	// (a custom resolver such as fullName) to the database columns the
	// resolver needs. Populated from graphql.CollectedFor annotations; a
	// column annotated for several names appears under each of them, and
	// several columns can be collected for one name. Without an entry the
	// collector treats the field as unknown and falls back to SELECT *.
	CollectedFor map[string][]string
	// InterfaceFields maps a GraphQL interface field (graphql.InterfaceField)
	// to the edges that back it, so selecting the field eager-loads every
	// contributing edge — or, when the selection is covered by __typename and
	// id and every edge owns its foreign key, only the key columns.
	InterfaceFields map[string]InterfaceFieldMeta
}

// InterfaceFieldMeta describes one GraphQL interface field.
type InterfaceFieldMeta struct {
	// Edges are the keys into CollectMeta.Edges of the contributing edges.
	Edges []string
	// Satisfies lists the GraphQL interface and its implementor type names,
	// the type conditions a selection on the field may use.
	Satisfies []string
	// FastPath is true when the generated resolver can answer a selection
	// covered by __typename and id from the foreign keys alone. Only then
	// may the collector skip loading the edges: a renamed edge resolves
	// through the ordinary edge method and would otherwise be queried once
	// per row.
	FastPath bool
}
