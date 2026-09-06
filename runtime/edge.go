package runtime

import (
	"context"
	"log/slog"
	"sync"

	"github.com/syssam/velox/dialect"
	"github.com/syssam/velox/dialect/sql"
	"github.com/syssam/velox/schema/field"
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

// Where adds predicates to the edge load query.
func Where(ps ...func(*sql.Selector)) LoadOption {
	return func(c *LoadConfig) {
		c.Predicates = append(c.Predicates, ps...)
	}
}

// Limit sets the maximum number of edges to load.
func Limit(n int) LoadOption {
	return func(c *LoadConfig) {
		c.Limit = &n
	}
}

// Offset sets the number of edges to skip before loading.
func Offset(n int) LoadOption {
	return func(c *LoadConfig) {
		c.Offset = &n
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

// EdgeLoad holds the name and options for an edge to be loaded.
type EdgeLoad struct {
	Name  string
	Label string // Optional label for named edge loading
	Opts  []LoadOption
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
// Registered Type Info
// =============================================================================

// RegisteredTypeInfo holds non-generic type registration info used by generic
// runtime helpers (scan, assign, ID extraction) that cannot import per-entity
// packages directly.
type RegisteredTypeInfo struct {
	Table       string
	Columns     []string
	IDColumn    string
	IDFieldType field.Type // ID field type (e.g., field.TypeInt, field.TypeUUID, field.TypeString).
	ScanValues  func(columns []string) ([]any, error)
	New         func() any
	Assign      func(entity any, columns []string, values []any) error
	GetID       func(entity any) any
}

var (
	typeInfoMu      sync.RWMutex
	registeredTypes = map[string]*RegisteredTypeInfo{}
)

// RegisterTypeInfo registers non-generic type info for a table.
// Called from generated init() alongside RegisterType.
func RegisterTypeInfo(table string, info *RegisteredTypeInfo) {
	typeInfoMu.Lock()
	defer typeInfoMu.Unlock()
	registeredTypes[table] = info
	slog.Debug("velox: registered type info", "table", table)
}

// FindRegisteredType looks up non-generic type info by table name.
func FindRegisteredType(table string) *RegisteredTypeInfo {
	typeInfoMu.RLock()
	defer typeInfoMu.RUnlock()
	return registeredTypes[table]
}

// =============================================================================
// Driver / Config Context Propagation
// =============================================================================

// driverKey is the context key for the database driver.
type driverKey struct{}

// WithDriverContext returns a new context with the given driver attached.
// Used by generated code and transaction wrappers to propagate the driver
// through context so downstream edge resolvers can construct queries without
// explicitly threading the driver through every call.
func WithDriverContext(ctx context.Context, drv dialect.Driver) context.Context {
	return context.WithValue(ctx, driverKey{}, drv)
}

// DriverFromContext returns the driver from the context, or nil if not set.
func DriverFromContext(ctx context.Context) dialect.Driver {
	d, _ := ctx.Value(driverKey{}).(dialect.Driver)
	return d
}

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

// =============================================================================
// EdgeQuery Type
// =============================================================================

// EdgeQuery is a non-generic query builder for edge traversal results.
// It holds query state directly (self-contained) and constructs a QueryBase
// on-the-fly when needed by terminal functions. Entity-specific typed wrappers
// (generated code) embed EdgeQuery and add typed All/First/Only methods.
type EdgeQuery struct {
	Config
	driver     dialect.Driver
	table      string
	columns    []string
	idColumn   string
	fkColumns  []string
	ctx        *QueryContext
	path       func(context.Context) (*sql.Selector, error)
	predicates []func(*sql.Selector)
	order      []func(*sql.Selector)
	modifiers  []func(*sql.Selector)
	edges      []EdgeLoad
	withFKs    bool
	inters     []Interceptor
	scan       *ScanConfig
}

// NewEdgeQuery creates an EdgeQuery from config, QueryBase, and ScanConfig.
// The QueryBase fields are copied into the self-contained EdgeQuery.
func NewEdgeQuery(cfg Config, qb *QueryBase, sc *ScanConfig) *EdgeQuery {
	return &EdgeQuery{
		Config:     cfg,
		driver:     qb.Driver,
		table:      qb.Table,
		columns:    qb.Columns,
		idColumn:   qb.IDColumn,
		fkColumns:  qb.FKColumns,
		ctx:        qb.Ctx,
		path:       qb.Path,
		predicates: qb.Predicates,
		order:      qb.Order,
		modifiers:  qb.Modifiers,
		edges:      qb.Edges,
		withFKs:    qb.WithFKs,
		inters:     qb.Inters,
		scan:       sc,
	}
}

// Where appends predicates to the query.
func (q *EdgeQuery) Where(ps ...func(*sql.Selector)) *EdgeQuery {
	q.predicates = append(q.predicates, ps...)
	return q
}

// Limit sets the maximum number of records to return.
func (q *EdgeQuery) Limit(n int) *EdgeQuery {
	q.ctx.Limit = &n
	return q
}

// Offset sets the number of records to skip.
func (q *EdgeQuery) Offset(n int) *EdgeQuery {
	q.ctx.Offset = &n
	return q
}

// Order specifies the record ordering.
func (q *EdgeQuery) Order(o ...func(*sql.Selector)) *EdgeQuery {
	q.order = append(q.order, o...)
	return q
}

// Select specifies which columns to return.
func (q *EdgeQuery) Select(columns ...string) *EdgeQuery {
	q.ctx.Fields = append(q.ctx.Fields, columns...)
	return q
}

// Unique configures the query to filter duplicate records.
func (q *EdgeQuery) Unique(v bool) *EdgeQuery {
	q.ctx.Unique = &v
	return q
}

// Modify adds a query modifier for custom SQL.
func (q *EdgeQuery) Modify(modifiers ...func(*sql.Selector)) *EdgeQuery {
	q.modifiers = append(q.modifiers, modifiers...)
	return q
}

// WithEdge tells the query to eagerly load the named edge.
func (q *EdgeQuery) WithEdge(name string, opts ...LoadOption) *EdgeQuery {
	q.edges = append(q.edges, EdgeLoad{Name: name, Opts: opts})
	q.withFKs = true
	return q
}

// Clone returns a deep copy of the query.
func (q *EdgeQuery) Clone() *EdgeQuery {
	if q == nil {
		return nil
	}
	return &EdgeQuery{
		Config:     q.Config,
		driver:     q.driver,
		table:      q.table,
		columns:    q.columns,
		idColumn:   q.idColumn,
		fkColumns:  q.fkColumns,
		ctx:        q.ctx.Clone(),
		path:       q.path,
		predicates: CloneSlice(q.predicates),
		order:      CloneSlice(q.order),
		modifiers:  CloneSlice(q.modifiers),
		edges:      CloneSlice(q.edges),
		withFKs:    q.withFKs,
		inters:     CloneSlice(q.inters),
		scan:       q.scan,
	}
}

// toQueryBase constructs a *QueryBase from the EdgeQuery fields.
func (q *EdgeQuery) toQueryBase() *QueryBase {
	return &QueryBase{
		Driver:     q.driver,
		Table:      q.table,
		Columns:    q.columns,
		IDColumn:   q.idColumn,
		FKColumns:  q.fkColumns,
		Ctx:        q.ctx,
		Path:       q.path,
		Predicates: q.predicates,
		Order:      q.order,
		Modifiers:  q.modifiers,
		Edges:      q.edges,
		WithFKs:    q.withFKs,
		Inters:     q.inters,
	}
}

// Count returns the number of matching entities.
func (q *EdgeQuery) Count(ctx context.Context) (int, error) {
	return QueryCount(ctx, q.toQueryBase(), q.idFieldType())
}

// CountX is like Count, but panics on error.
func (q *EdgeQuery) CountX(ctx context.Context) int {
	n, err := q.Count(ctx)
	if err != nil {
		panic(err)
	}
	return n
}

// Exist returns whether any matching entity exists.
func (q *EdgeQuery) Exist(ctx context.Context) (bool, error) {
	return QueryExist(ctx, q.toQueryBase(), q.idFieldType())
}

// ExistX is like Exist, but panics on error.
func (q *EdgeQuery) ExistX(ctx context.Context) bool {
	ok, err := q.Exist(ctx)
	if err != nil {
		panic(err)
	}
	return ok
}

// Scan applies the query and scans results into v.
func (q *EdgeQuery) Scan(ctx context.Context, v any) error {
	return QueryScan(ctx, q.toQueryBase(), v)
}

// ScanX is like Scan, but panics on error.
func (q *EdgeQuery) ScanX(ctx context.Context, v any) {
	if err := q.Scan(ctx, v); err != nil {
		panic(err)
	}
}

// IDs executes the query and returns all matching entity IDs.
func (q *EdgeQuery) IDs(ctx context.Context) ([]any, error) {
	return QueryIDsOnly(ctx, q.toQueryBase())
}

// IDsX is like IDs, but panics on error.
func (q *EdgeQuery) IDsX(ctx context.Context) []any {
	ids, err := q.IDs(ctx)
	if err != nil {
		panic(err)
	}
	return ids
}

// FirstID returns the first matching entity ID.
func (q *EdgeQuery) FirstID(ctx context.Context) (any, error) {
	return QueryFirstIDOnly(ctx, q.toQueryBase())
}

// FirstIDX is like FirstID, but panics on error.
func (q *EdgeQuery) FirstIDX(ctx context.Context) any {
	id, err := q.FirstID(ctx)
	if err != nil {
		panic(err)
	}
	return id
}

// OnlyID returns the only matching entity ID.
func (q *EdgeQuery) OnlyID(ctx context.Context) (any, error) {
	return QueryOnlyIDOnly(ctx, q.toQueryBase())
}

// OnlyIDX is like OnlyID, but panics on error.
func (q *EdgeQuery) OnlyIDX(ctx context.Context) any {
	id, err := q.OnlyID(ctx)
	if err != nil {
		panic(err)
	}
	return id
}

// AllAny executes the query and returns all matching entities as []any.
// Used by generated typed wrappers to provide type-safe results.
func (q *EdgeQuery) AllAny(ctx context.Context) ([]any, error) {
	if q.scan == nil {
		return nil, nil
	}
	return QueryAllSC(ctx, q.toQueryBase(), q.scan)
}

// FirstAny returns the first matching entity as any.
func (q *EdgeQuery) FirstAny(ctx context.Context) (any, error) {
	clone := q.Clone()
	clone.ctx.Limit = intPtr(1)
	nodes, err := clone.AllAny(ctx)
	if err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		return nil, NewNotFoundError(q.ctx.Type)
	}
	return nodes[0], nil
}

// OnlyAny returns the only matching entity as any.
func (q *EdgeQuery) OnlyAny(ctx context.Context) (any, error) {
	clone := q.Clone()
	clone.ctx.Limit = intPtr(2)
	nodes, err := clone.AllAny(ctx)
	if err != nil {
		return nil, err
	}
	switch len(nodes) {
	case 0:
		return nil, NewNotFoundError(q.ctx.Type)
	case 1:
		return nodes[0], nil
	default:
		return nil, NewNotSingularError(q.ctx.Type)
	}
}

// GetDriver returns the database driver.
func (q *EdgeQuery) GetDriver() dialect.Driver { return q.driver }

// GetCtx returns the query context for field projection.
func (q *EdgeQuery) GetCtx() *QueryContext { return q.ctx }

// GetPredicates returns the query predicates.
func (q *EdgeQuery) GetPredicates() []func(*sql.Selector) { return q.predicates }

// GetOrder returns the query order functions.
func (q *EdgeQuery) GetOrder() []func(*sql.Selector) { return q.order }

// GetModifiers returns the query modifier functions.
func (q *EdgeQuery) GetModifiers() []func(*sql.Selector) { return q.modifiers }

// GetPath returns the graph traversal path function.
func (q *EdgeQuery) GetPath() func(context.Context) (*sql.Selector, error) { return q.path }

// GetInters returns the query interceptors.
func (q *EdgeQuery) GetInters() []Interceptor { return q.inters }

// GetWithFKs returns whether FK columns should be included.
func (q *EdgeQuery) GetWithFKs() bool { return q.withFKs }

// idFieldType returns the ID field type from the scan config.
// Falls back to field.TypeInt if no scan config is available.
func (q *EdgeQuery) idFieldType() field.Type {
	if q.scan != nil {
		return q.scan.IDFieldType
	}
	return field.TypeInt
}

// intPtr returns a pointer to the given int value.
func intPtr(n int) *int { return &n }
