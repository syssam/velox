package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/syssam/velox"
	"github.com/syssam/velox/dialect/sql/sqlgraph"
)

// =============================================================================
// Mutator / Query Factory Registry
// =============================================================================

// MutatorFunc is a function that executes a mutation for a specific entity type.
// Registered by each entity sub-package's init() function.
type MutatorFunc func(ctx context.Context, cfg Config, m any) (any, error)

// mutators stores registered mutator functions keyed by entity type name.
var (
	mutatorMu       sync.RWMutex
	mutators        = map[string]MutatorFunc{}
	registeredNames []string
)

// RegisterMutator registers a mutator function for the given entity type name.
// Called from generated entity sub-package init() functions.
func RegisterMutator(name string, fn MutatorFunc) {
	mutatorMu.Lock()
	defer mutatorMu.Unlock()
	if _, ok := mutators[name]; !ok {
		registeredNames = append(registeredNames, name)
	}
	mutators[name] = fn
	slog.Debug("velox: registered mutator", "entity", name)
}

// FindMutator looks up a registered mutator by entity type name.
func FindMutator(name string) MutatorFunc {
	mutatorMu.RLock()
	defer mutatorMu.RUnlock()
	return mutators[name]
}

// QueryFunc creates a Querier for a given entity type.
type QueryFunc func(cfg Config) any

var (
	queryMu        sync.RWMutex
	queryFactories = map[string]QueryFunc{}
)

// RegisterQueryFactory registers a query factory for an entity type.
func RegisterQueryFactory(name string, fn QueryFunc) {
	queryMu.Lock()
	defer queryMu.Unlock()
	queryFactories[name] = fn
	slog.Debug("velox: registered query factory", "entity", name)
}

// NewEntityQuery creates a querier for the named entity type.
// Panics with a descriptive message if the entity is not registered.
func NewEntityQuery(name string, cfg Config) any {
	queryMu.RLock()
	defer queryMu.RUnlock()
	fn, ok := queryFactories[name]
	if !ok {
		panic(fmt.Sprintf("velox: query factory not registered for entity %q — ensure the generated query package is imported (e.g., import _ \"your/pkg/query\"). The root client package normally does this automatically; see it if you are using velox generated code directly.", name))
	}
	return fn(cfg)
}

// EntityRegistration holds all per-entity registration data.
// Used by RegisterEntity() to register all entity metadata in one call.
type EntityRegistration struct {
	// Name is the entity type name (e.g. "User").
	Name string
	// Table is the SQL table name (e.g. "users").
	Table string
	// ValidColumn checks if a column exists on this table.
	ValidColumn func(string) bool
	// Mutator executes mutations for this entity type.
	Mutator MutatorFunc
}

// RegisterEntity registers all metadata for an entity in one call.
// Called from generated entity sub-package init() functions.
func RegisterEntity(r EntityRegistration) {
	RegisterMutator(r.Name, r.Mutator)
	RegisterColumns(r.Table, r.ValidColumn)
	slog.Debug("velox: registered entity", "entity", r.Name, "table", r.Table)
}

// ValidateRegistries checks that all registered entity types have consistent
// registrations across the mutator and query registries. Call this at
// application startup to catch missing imports or code generation issues early.
// Returns nil if all registries are consistent.
func ValidateRegistries() error {
	mutatorMu.RLock()
	defer mutatorMu.RUnlock()
	queryMu.RLock()
	defer queryMu.RUnlock()

	var errs []string
	for _, name := range registeredNames {
		if _, ok := queryFactories[name]; !ok {
			errs = append(errs, fmt.Sprintf("entity %q: mutator registered but query factory missing", name))
		}
	}
	for name := range queryFactories {
		if _, ok := mutators[name]; !ok {
			errs = append(errs, fmt.Sprintf("entity %q: query factory registered but mutator missing", name))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("velox: registry validation failed:\n  %s", strings.Join(errs, "\n  "))
	}
	slog.Debug("velox: all registries validated", "entities", len(registeredNames))
	return nil
}

// =============================================================================
// Entity Policy Registry
// =============================================================================

// policyRegistry stores, per entity type name, the address of the entity's
// RuntimePolicy variable. Each entity's generated runtime init() registers
// it so cross-package edge queries (e.g. entity.User.QueryPosts()) and
// eager loads can look up the TARGET entity's policy without importing its
// sub-package. The variable is read at lookup time, not copied at init: an
// edge query sees the same policy the target's own client reads, including
// after the variable is replaced.
var (
	policyMu       sync.RWMutex
	policyRegistry = map[string]*velox.Policy{}
)

// RegisterEntityPolicy registers the policy variable of an entity type.
// Called from generated runtime init() functions with the address of the
// entity's RuntimePolicy. A nil pointer is a no-op.
func RegisterEntityPolicy(name string, p *velox.Policy) {
	if p == nil {
		return
	}
	policyMu.Lock()
	defer policyMu.Unlock()
	policyRegistry[name] = p
	slog.Debug("velox: registered entity policy", "entity", name)
}

// EntityPolicy returns the current value of the named entity's registered
// policy variable, or nil if the entity has no policy (or its sub-package
// is not imported). Used by edge query constructors and eager loads to wire
// the target entity's policy onto freshly-built queries.
func EntityPolicy(name string) velox.Policy {
	policyMu.RLock()
	defer policyMu.RUnlock()
	if p := policyRegistry[name]; p != nil {
		return *p
	}
	return nil
}

// =============================================================================
// Column Registry
// =============================================================================

// columnRegistry stores column validation functions keyed by table name.
// Each entity registers its ValidColumn function at init() time.
// Used by generated checkColumn() to validate ordering columns without
// importing every entity sub-package.
var (
	columnMu       sync.RWMutex
	columnRegistry = map[string]func(string) bool{}
)

// RegisterColumns registers a column validation function for a table.
// Called from each entity package's init() function alongside RegisterType.
func RegisterColumns(table string, valid func(string) bool) {
	columnMu.Lock()
	defer columnMu.Unlock()
	columnRegistry[table] = valid
}

// ValidColumn checks if the column exists in the given table.
// Returns an error if the table is unknown or the column is invalid.
func ValidColumn(table, column string) error {
	columnMu.RLock()
	check, ok := columnRegistry[table]
	columnMu.RUnlock()
	if !ok {
		return fmt.Errorf("unknown table %q", table)
	}
	if !check(column) {
		return fmt.Errorf("unknown column %q for table %q", column, table)
	}
	return nil
}

// =============================================================================
// Node Resolver Registry
// =============================================================================

// Edge-schema defaults: an M2M edge declared with Through() writes a row of
// the join entity, whose fields may have defaults (Membership.joined_at =
// time.Now). The join entity's client package registers a function that
// returns those default fields; the entity adding the edge — which cannot
// import the join entity's package — looks it up by the join table. Written
// at init, read on the create/update path.
var (
	edgeDefaultsMu sync.RWMutex
	edgeDefaults   = map[string]func() []*sqlgraph.FieldSpec{}
)

// RegisterEdgeSchemaDefaults registers the default-field builder of the edge
// schema stored in table. Called from generated init() functions.
func RegisterEdgeSchemaDefaults(table string, fn func() []*sqlgraph.FieldSpec) {
	edgeDefaultsMu.Lock()
	defer edgeDefaultsMu.Unlock()
	edgeDefaults[table] = fn
}

// EdgeSchemaDefaults returns the default field values for a new row of the
// edge schema stored in table, or nil when it has none registered.
func EdgeSchemaDefaults(table string) []*sqlgraph.FieldSpec {
	edgeDefaultsMu.RLock()
	fn := edgeDefaults[table]
	edgeDefaultsMu.RUnlock()
	if fn == nil {
		return nil
	}
	return fn()
}

// NodeRegistry provides a global registry for Node interface resolution.
// Each entity package registers its resolver at init time, eliminating
// the need for a monolithic type-switch in the root package.
var (
	nodeMu       sync.RWMutex
	nodeRegistry = map[string]NodeResolver{}
)

// NodeResolver resolves a node by table name and ID.
type NodeResolver struct {
	// Type is the GraphQL type name (e.g., "User").
	Type string
	// Resolve fetches the node by ID.
	Resolve func(ctx context.Context, id any) (any, error)
}

// RegisterNodeResolver registers a node resolver for a table.
// Called from generated entity sub-package init() functions.
func RegisterNodeResolver(table string, r NodeResolver) {
	nodeMu.Lock()
	defer nodeMu.Unlock()
	nodeRegistry[table] = r
	slog.Debug("velox: registered node resolver", "table", table, "type", r.Type)
}

// ResolveNode resolves id against every registered NodeResolver and returns
// the one node that has it. found is false when no type has the id. When
// more than one does — per-table keys collide — it returns an error wrapping
// ErrAmbiguousNodeID that names the types, never an arbitrary one of them:
// the registry is a map, so "first match" differed from call to call.
// Resolvers that do not handle the id's Go type are skipped like not-found.
// Every type is probed, one query each; with FeatureGlobalID ids do not
// collide and exactly one probe matches.
func ResolveNode(ctx context.Context, id any) (node any, found bool, err error) {
	resolvers := NodeResolvers()
	names := slices.Sorted(maps.Keys(resolvers))
	var types []string
	for _, name := range names {
		r := resolvers[name]
		v, err := r.Resolve(ctx, id)
		if err != nil {
			if IsNotFound(err) || IsNodeIDTypeMismatch(err) {
				continue
			}
			return nil, false, err
		}
		node = v
		types = append(types, r.Type)
	}
	switch len(types) {
	case 0:
		return nil, false, nil
	case 1:
		return node, true, nil
	default:
		return nil, false, fmt.Errorf("%w: id %v is a %s; use globally unique IDs (FeatureGlobalID)",
			ErrAmbiguousNodeID, id, strings.Join(types, " and a "))
	}
}

// NodeResolvers returns a copy of all registered node resolvers.
func NodeResolvers() map[string]NodeResolver {
	nodeMu.RLock()
	defer nodeMu.RUnlock()
	result := make(map[string]NodeResolver, len(nodeRegistry))
	maps.Copy(result, nodeRegistry)
	return result
}
