package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"strings"
	"sync"

	"github.com/syssam/velox"
)

// =============================================================================
// Mutator / Query Factory / Entity Client Registry
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

// RegisteredTypeNames returns all registered entity type names.
func RegisteredTypeNames() []string {
	mutatorMu.RLock()
	defer mutatorMu.RUnlock()
	return registeredNames
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
		panic(fmt.Sprintf("velox: query factory not registered for entity %q — ensure the entity package is imported (e.g., import _ \"your/pkg/%s\")", name, strings.ToLower(name)))
	}
	return fn(cfg)
}

// EntityClientFunc creates a typed entity client from runtime config.
type EntityClientFunc func(cfg Config) any

var (
	clientMu      sync.RWMutex
	entityClients = map[string]EntityClientFunc{}
)

// RegisterEntityClient registers an entity client factory.
func RegisterEntityClient(name string, fn EntityClientFunc) {
	clientMu.Lock()
	defer clientMu.Unlock()
	entityClients[name] = fn
	slog.Debug("velox: registered entity client", "entity", name)
}

// NewEntityClient creates a typed entity client by name.
// Panics with a descriptive message if the entity is not registered.
func NewEntityClient(name string, cfg Config) any {
	clientMu.RLock()
	defer clientMu.RUnlock()
	fn, ok := entityClients[name]
	if !ok {
		panic(fmt.Sprintf("velox: entity client not registered for %q — ensure the entity package is imported (e.g., import _ \"your/pkg/%s\")", name, strings.ToLower(name)))
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
	// TypeInfo provides scan/assign capabilities.
	TypeInfo *RegisteredTypeInfo
	// ValidColumn checks if a column exists on this table.
	ValidColumn func(string) bool
	// Mutator executes mutations for this entity type.
	Mutator MutatorFunc
	// Client constructs a typed entity client from Config.
	Client EntityClientFunc
}

// RegisterEntity registers all metadata for an entity in one call.
// Called from generated entity sub-package init() functions.
func RegisterEntity(r EntityRegistration) {
	RegisterMutator(r.Name, r.Mutator)
	RegisterEntityClient(r.Name, r.Client)
	RegisterTypeInfo(r.Table, r.TypeInfo)
	RegisterColumns(r.Table, r.ValidColumn)
	slog.Debug("velox: registered entity", "entity", r.Name, "table", r.Table)
}

// ValidateRegistries checks that all registered entity types have consistent
// registrations across mutator, query, and client registries. Call this at
// application startup to catch missing imports or code generation issues early.
// Returns nil if all registries are consistent.
func ValidateRegistries() error {
	mutatorMu.RLock()
	defer mutatorMu.RUnlock()
	queryMu.RLock()
	defer queryMu.RUnlock()
	clientMu.RLock()
	defer clientMu.RUnlock()

	var errs []string
	for _, name := range registeredNames {
		if _, ok := queryFactories[name]; !ok {
			errs = append(errs, fmt.Sprintf("entity %q: mutator registered but query factory missing", name))
		}
		if _, ok := entityClients[name]; !ok {
			errs = append(errs, fmt.Sprintf("entity %q: mutator registered but entity client missing", name))
		}
	}
	for name := range queryFactories {
		if _, ok := mutators[name]; !ok {
			errs = append(errs, fmt.Sprintf("entity %q: query factory registered but mutator missing", name))
		}
	}
	for name := range entityClients {
		if _, ok := mutators[name]; !ok {
			errs = append(errs, fmt.Sprintf("entity %q: entity client registered but mutator missing", name))
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

// policyRegistry stores per-entity privacy policies keyed by entity type name.
// Each entity sub-package's runtime.go init() registers its RuntimePolicy here
// so cross-package edge queries (e.g. entity.User.QueryPosts()) can look up
// the TARGET entity's policy without importing its sub-package.
var (
	policyMu       sync.RWMutex
	policyRegistry = map[string]velox.Policy{}
)

// RegisterEntityPolicy registers a privacy policy for an entity type.
// Called from generated entity sub-package runtime.go init() functions.
// Passing a nil policy is a no-op (entities without privacy policies
// simply never call this).
func RegisterEntityPolicy(name string, p velox.Policy) {
	if p == nil {
		return
	}
	policyMu.Lock()
	defer policyMu.Unlock()
	policyRegistry[name] = p
	slog.Debug("velox: registered entity policy", "entity", name)
}

// EntityPolicy returns the registered privacy policy for the named entity,
// or nil if the entity has no policy (or its sub-package is not imported).
// Used by cross-package edge query constructors to wire the target entity's
// policy onto freshly-built queries.
func EntityPolicy(name string) velox.Policy {
	policyMu.RLock()
	defer policyMu.RUnlock()
	return policyRegistry[name]
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

// NodeResolvers returns a copy of all registered node resolvers.
func NodeResolvers() map[string]NodeResolver {
	nodeMu.RLock()
	defer nodeMu.RUnlock()
	result := make(map[string]NodeResolver, len(nodeRegistry))
	maps.Copy(result, nodeRegistry)
	return result
}
