package runtime

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/google/uuid"

	"github.com/syssam/velox/dialect"
	"github.com/syssam/velox/dialect/sql"
	"github.com/syssam/velox/dialect/sql/sqlgraph"
	"github.com/syssam/velox/schema/field"
)

// QueryContext holds query context like field projections.
type QueryContext struct {
	Type       string
	Fields     []string
	fieldsSeen map[string]struct{} // dedup set for AppendFieldOnce
	Unique     *bool
	Limit      *int
	Offset     *int
}

// Clone returns a deep copy of the QueryContext.
func (c *QueryContext) Clone() *QueryContext {
	if c == nil {
		return nil
	}
	clone := *c
	clone.Fields = CloneSlice(c.Fields)
	clone.fieldsSeen = nil // lazily rebuilt on next AppendFieldOnce
	if c.Unique != nil {
		v := *c.Unique
		clone.Unique = &v
	}
	if c.Limit != nil {
		v := *c.Limit
		clone.Limit = &v
	}
	if c.Offset != nil {
		v := *c.Offset
		clone.Offset = &v
	}
	return &clone
}

// AppendFieldOnce appends a field name if it is not already present.
func (c *QueryContext) AppendFieldOnce(f string) {
	if c.fieldsSeen == nil {
		c.fieldsSeen = make(map[string]struct{}, len(c.Fields))
		for _, existing := range c.Fields {
			c.fieldsSeen[existing] = struct{}{}
		}
	}
	if _, ok := c.fieldsSeen[f]; ok {
		return
	}
	c.fieldsSeen[f] = struct{}{}
	c.Fields = append(c.Fields, f)
}

// QueryBase holds non-generic query state. Compiled once, shared by all entity queries.
type QueryBase struct {
	Driver      dialect.Driver
	Table       string
	Columns     []string
	IDColumn    string
	FKColumns   []string
	IDFieldType field.Type // ID field type for Count/Exist queries.
	Ctx         *QueryContext
	Path        func(context.Context) (*sql.Selector, error) // Graph traversal path (set by QueryXxx methods).
	Predicates  []func(*sql.Selector)
	Order       []func(*sql.Selector)
	Modifiers   []func(*sql.Selector)
	Edges       []EdgeLoad
	WithFKs     bool
	Inters      []Interceptor
}

// NewQueryBase creates a new QueryBase.
func NewQueryBase(drv dialect.Driver, table string, columns []string, idColumn string, fkColumns []string, typeName string) *QueryBase {
	return &QueryBase{
		Driver:    drv,
		Table:     table,
		Columns:   columns,
		IDColumn:  idColumn,
		FKColumns: fkColumns,
		Ctx:       &QueryContext{Type: typeName},
	}
}

// GetIDColumn returns the primary key column name.
// Implements FieldCollectable.
func (q *QueryBase) GetIDColumn() string { return q.IDColumn }

// GetCtx returns the query context for field projection.
// Implements FieldCollectable.
func (q *QueryBase) GetCtx() *QueryContext { return q.Ctx }

// QueryReader provides read-only access to query state. Generated query
// types and QueryBase both satisfy this interface. Top-level functions
// (BuildQueryFrom, BuildSelectorFrom, MakeQuerySpec) accept QueryReader
// so generated queries can skip the queryBase() allocation bridge.
type QueryReader interface {
	GetDriver() dialect.Driver
	GetTable() string
	GetColumns() []string
	GetIDColumn() string
	GetFKColumns() []string
	GetIDFieldType() field.Type
	GetCtx() *QueryContext
	GetPath() func(context.Context) (*sql.Selector, error)
	GetPredicates() []func(*sql.Selector)
	GetOrder() []func(*sql.Selector)
	GetModifiers() []func(*sql.Selector)
	GetWithFKs() bool
}

// GetDriver returns the dialect driver. Implements QueryReader.
func (q *QueryBase) GetDriver() dialect.Driver { return q.Driver }

// GetTable returns the primary table name. Implements QueryReader.
func (q *QueryBase) GetTable() string { return q.Table }

// GetColumns returns the default column list. Implements QueryReader.
func (q *QueryBase) GetColumns() []string { return q.Columns }

// GetFKColumns returns the foreign-key columns needed for edge loading. Implements QueryReader.
func (q *QueryBase) GetFKColumns() []string { return q.FKColumns }

// GetIDFieldType returns the schema type of the ID field. Implements QueryReader.
func (q *QueryBase) GetIDFieldType() field.Type { return q.IDFieldType }

// GetPath returns the graph-traversal path function. Implements QueryReader.
func (q *QueryBase) GetPath() func(context.Context) (*sql.Selector, error) { return q.Path }

// GetPredicates returns the registered WHERE predicates. Implements QueryReader.
func (q *QueryBase) GetPredicates() []func(*sql.Selector) { return q.Predicates }

// GetOrder returns the registered ORDER BY functions. Implements QueryReader.
func (q *QueryBase) GetOrder() []func(*sql.Selector) { return q.Order }

// GetModifiers returns the registered query modifiers. Implements QueryReader.
func (q *QueryBase) GetModifiers() []func(*sql.Selector) { return q.Modifiers }

// GetWithFKs reports whether FK columns should be included in selection. Implements QueryReader.
func (q *QueryBase) GetWithFKs() bool { return q.WithFKs }

// Where appends predicate functions to the query.
func (q *QueryBase) Where(ps ...func(*sql.Selector)) {
	q.Predicates = append(q.Predicates, ps...)
}

// PredicateAdder is the minimal interface implemented by generated
// query builders so the generated per-entity Filter can inject raw
// SQL-level predicates without reaching into the query's internal
// state. One method, one purpose: filters hold a PredicateAdder
// reference instead of a pointer into the query's predicates slice,
// so the query's internal representation can evolve without breaking
// filter construction.
//
// This interface exists at the runtime/filter boundary, not as a
// caller-facing API. AddPredicate is exported on the generated
// *XxxQuery type only because cross-package structural interface
// satisfaction in Go requires exported methods — direct callers
// should use Query.Where / Query.Filter, not AddPredicate.
type PredicateAdder interface {
	// AddPredicate appends a raw SQL-level predicate to the query.
	AddPredicate(func(*sql.Selector))
}

// SetLimit sets the query limit.
func (q *QueryBase) SetLimit(n int) { q.Ctx.Limit = &n }

// SetOffset sets the query offset.
func (q *QueryBase) SetOffset(n int) { q.Ctx.Offset = &n }

// AddOrder appends order functions to the query.
func (q *QueryBase) AddOrder(o ...func(*sql.Selector)) {
	q.Order = append(q.Order, o...)
}

// AddModifier appends modifier functions to the query.
func (q *QueryBase) AddModifier(m ...func(*sql.Selector)) {
	q.Modifiers = append(q.Modifiers, m...)
}

// SetUnique sets whether the query should return distinct results.
func (q *QueryBase) SetUnique(v bool) { q.Ctx.Unique = &v }

// WithEdgeLoad adds an edge to be eagerly loaded.
// Also enables FK column selection, which M2O edges need to resolve parent→child.
func (q *QueryBase) WithEdgeLoad(name string, opts ...LoadOption) {
	q.Edges = append(q.Edges, EdgeLoad{Name: name, Opts: opts})
	q.WithFKs = true
}

// WithNamedEdgeLoad adds a named edge load for distinguishing multiple loads of the same edge.
func (q *QueryBase) WithNamedEdgeLoad(label, name string, opts ...LoadOption) {
	q.Edges = append(q.Edges, EdgeLoad{Name: name, Label: label, Opts: opts})
	q.WithFKs = true
}

// ForUpdate locks the selected rows against concurrent updates.
func (q *QueryBase) ForUpdate(opts ...sql.LockOption) {
	q.SetUnique(false)
	q.AddModifier(func(s *sql.Selector) { s.ForUpdate(opts...) })
}

// ForShare locks the selected rows in shared mode.
func (q *QueryBase) ForShare(opts ...sql.LockOption) {
	q.SetUnique(false)
	q.AddModifier(func(s *sql.Selector) { s.ForShare(opts...) })
}

// ForNoKeyUpdate is like ForUpdate but weaker. PostgreSQL only.
func (q *QueryBase) ForNoKeyUpdate(opts ...sql.LockOption) {
	q.SetUnique(false)
	q.AddModifier(func(s *sql.Selector) { s.For(sql.LockNoKeyUpdate, opts...) })
}

// ForKeyShare is the weakest row-level lock. PostgreSQL only.
func (q *QueryBase) ForKeyShare(opts ...sql.LockOption) {
	q.SetUnique(false)
	q.AddModifier(func(s *sql.Selector) { s.For(sql.LockKeyShare, opts...) })
}

// CloneSlice returns nil if s is empty, otherwise an independent copy of s.
// Used by QueryBase.Clone and by per-entity generated Query.clone() to keep
// the clone idiom in one place. Exported because generated query packages
// call it across the package boundary.
//
// Behavior note: returning nil (not an empty slice) for empty input lets
// callers skip redundant work when the source slice is unset. For len, range,
// and append, nil and an empty slice behave identically; note that nil != []T{}
// under reflect.DeepEqual and direct nil-checks, so this is not a drop-in
// replacement for `append([]T{}, s...)` if a caller relies on those. For
// populated slices, deep-copy semantics match. Pinned by
// TestQueryBase_Clone_EmptyAllocInvariant and TestQueryBase_Clone_PopulatedDeepCopy
// in runtime/query_test.go.
func CloneSlice[T any](s []T) []T {
	if len(s) == 0 {
		return nil
	}
	out := make([]T, len(s))
	copy(out, s)
	return out
}

// Clone returns a deep copy of the QueryBase. Slice fields that are empty
// in the source stay nil in the clone (see CloneSlice). Deep-copy semantics
// for populated slices are pinned by TestQueryBase_Clone_PopulatedDeepCopy.
func (q *QueryBase) Clone() *QueryBase {
	if q == nil {
		return nil
	}
	clone := *q
	clone.Ctx = q.Ctx.Clone()
	clone.Predicates = CloneSlice(q.Predicates)
	clone.Order = CloneSlice(q.Order)
	clone.Modifiers = CloneSlice(q.Modifiers)
	clone.Edges = CloneSlice(q.Edges)
	clone.Inters = CloneSlice(q.Inters)
	return &clone
}

// BuildQuery delegates to BuildQueryFrom for backward compatibility.
func (q *QueryBase) BuildQuery(ctx context.Context) (*sql.Selector, error) {
	return BuildQueryFrom(ctx, q)
}

// BuildSelector delegates to BuildSelectorFrom for backward compatibility.
func (q *QueryBase) BuildSelector(ctx context.Context) (*sql.Selector, error) {
	return BuildSelectorFrom(ctx, q)
}

// QuerySpec delegates to MakeQuerySpec for backward compatibility.
func (q *QueryBase) QuerySpec(idFieldType field.Type) *sqlgraph.QuerySpec {
	return MakeQuerySpec(q, idFieldType)
}

// resolvePathFrom resolves the graph traversal path from a QueryReader and returns
// the FROM selector. Must be called with the caller's context to propagate
// cancellation and tracing.
func resolvePathFrom(ctx context.Context, q QueryReader) (*sql.Selector, error) {
	path := q.GetPath()
	if path == nil {
		return nil, nil
	}
	return path(ctx)
}

// BuildQueryFrom constructs a *sql.Selector from a QueryReader's state.
// This is used by QueryXxx methods on the query builder to create a sub-select
// for graph traversal (SetNeighbors pattern). The selector contains the table,
// predicates, limit/offset, and order clauses from the current query.
func BuildQueryFrom(ctx context.Context, q QueryReader) (*sql.Selector, error) {
	var selector *sql.Selector
	if from, err := resolvePathFrom(ctx, q); err != nil {
		return nil, err
	} else if from != nil {
		selector = from
	} else {
		selector = sql.Select().From(sql.Table(q.GetTable()))
	}
	selector.SetDialect(q.GetDriver().Dialect())
	for _, p := range q.GetPredicates() {
		p(selector)
	}
	for _, o := range q.GetOrder() {
		o(selector)
	}
	qctx := q.GetCtx()
	if qctx.Limit != nil {
		selector.Limit(*qctx.Limit)
	}
	if qctx.Offset != nil {
		selector.Offset(*qctx.Offset)
		// SQLite requires LIMIT when OFFSET is used. Inject a large limit
		// if the caller set offset without limit, matching sqlgraph behavior.
		if qctx.Limit == nil {
			selector.Limit(math.MaxInt32)
		}
	}
	return selector, nil
}

// BuildSelectorFrom constructs a fully-configured *sql.Selector ready for
// execution from a QueryReader. Unlike BuildQueryFrom (which returns a bare
// selector for graph traversal), BuildSelectorFrom also applies column
// selection, FK columns, and DISTINCT.
func BuildSelectorFrom(ctx context.Context, q QueryReader) (*sql.Selector, error) {
	selector, err := BuildQueryFrom(ctx, q)
	if err != nil {
		return nil, err
	}
	// Select columns: use projected fields if set, else all columns (+ FK columns if needed).
	columns := q.GetColumns()
	qctx := q.GetCtx()
	if fields := qctx.Fields; len(fields) > 0 {
		idCol := q.GetIDColumn()
		columns = make([]string, 0, len(fields)+1)
		columns = append(columns, idCol)
		for _, f := range fields {
			if f != idCol {
				columns = append(columns, f)
			}
		}
	}
	if q.GetWithFKs() {
		if fkCols := q.GetFKColumns(); len(fkCols) > 0 {
			columns = append(columns, fkCols...)
		}
	}
	selector.Select(selector.Columns(columns...)...)
	if qctx.Unique != nil && *qctx.Unique {
		selector.Distinct()
	}
	// Modifiers run LAST so callers can replace the default projection
	// (e.g. aggregate queries emitting SUM/COUNT/TO_CHAR expressions via
	// selector.Select) or append to it (selector.AppendSelect). Matches
	// Ent's sqlgraph.query.selector ordering.
	for _, m := range q.GetModifiers() {
		m(selector)
	}
	return selector, nil
}

// MakeQuerySpec builds a sqlgraph.QuerySpec from a QueryReader's state.
func MakeQuerySpec(q QueryReader, idFieldType field.Type) *sqlgraph.QuerySpec {
	table := q.GetTable()
	cols := q.GetColumns()
	idCol := q.GetIDColumn()
	qctx := q.GetCtx()
	spec := sqlgraph.NewQuerySpec(table, cols,
		&sqlgraph.FieldSpec{Column: idCol, Type: idFieldType})

	if qctx.Unique != nil {
		spec.Unique = *qctx.Unique
	}

	if fields := qctx.Fields; len(fields) > 0 {
		spec.Node.Columns = make([]string, 0, len(fields)+1)
		spec.Node.Columns = append(spec.Node.Columns, idCol)
		for _, f := range fields {
			if f != idCol {
				spec.Node.Columns = append(spec.Node.Columns, f)
			}
		}
	}

	fkCols := q.GetFKColumns()
	if q.GetWithFKs() && len(fkCols) > 0 {
		if spec.Node.Columns == nil {
			// No specific field selection — start with all regular columns, then add FKs.
			spec.Node.Columns = make([]string, 0, len(cols)+len(fkCols))
			spec.Node.Columns = append(spec.Node.Columns, cols...)
		}
		spec.Node.Columns = append(spec.Node.Columns, fkCols...)
	}

	preds := q.GetPredicates()
	if len(preds) > 0 {
		spec.Predicate = func(s *sql.Selector) {
			for _, p := range preds {
				p(s)
			}
		}
	}

	if qctx.Limit != nil {
		spec.Limit = *qctx.Limit
	}
	if qctx.Offset != nil {
		spec.Offset = *qctx.Offset
	}

	order := q.GetOrder()
	if len(order) > 0 {
		spec.Order = func(s *sql.Selector) {
			for _, o := range order {
				o(s)
			}
		}
	}

	mods := q.GetModifiers()
	if len(mods) > 0 {
		spec.Modifiers = mods
	}

	return spec
}

// QueryAllSC executes a SELECT query and returns all matching entities as []any.
// Used by EdgeQuery and other non-generic query paths.
func QueryAllSC(ctx context.Context, q QueryReader, sc *ScanConfig) ([]any, error) {
	from, err := resolvePathFrom(ctx, q)
	if err != nil {
		return nil, err
	}
	spec := MakeQuerySpec(q, sc.IDFieldType)
	spec.From = from
	drv := q.GetDriver()
	var nodes []any

	spec.ScanValues = sc.ScanValues
	spec.Assign = func(columns []string, values []any) error {
		node := sc.New()
		if sc.SetDriver != nil {
			sc.SetDriver(node, drv)
		}
		nodes = append(nodes, node)
		return sc.Assign(node, columns, values)
	}

	if err := sqlgraph.QueryNodes(ctx, drv, spec); err != nil {
		return nil, err
	}
	return nodes, nil
}

// QueryCount executes a COUNT query.
func QueryCount(ctx context.Context, q QueryReader, idFieldType field.Type) (int, error) {
	from, err := resolvePathFrom(ctx, q)
	if err != nil {
		return 0, err
	}
	// Build spec then nil columns so the generated SQL is COUNT(*)
	// instead of COUNT(col1, col2, ...) which fails on SQLite.
	// QueryReader is read-only, so we clear columns on the spec directly
	// rather than cloning.
	spec := MakeQuerySpec(q, idFieldType)
	spec.Node.Columns = nil
	spec.From = from
	return sqlgraph.CountNodes(ctx, q.GetDriver(), spec)
}

// QueryExist returns true if any matching entity exists.
func QueryExist(ctx context.Context, q QueryReader, idFieldType field.Type) (bool, error) {
	n, err := QueryCount(ctx, q, idFieldType)
	return n > 0, err
}

// QueryIDsOnly scans only the ID column from the query.
// The base is cloned internally so callers do not need to pre-clone.
func QueryIDsOnly(ctx context.Context, base *QueryBase) ([]any, error) {
	return queryIDsOnlyNoClone(ctx, base.Clone())
}

// queryIDsOnlyNoClone is the internal implementation of QueryIDsOnly that does
// not clone. Used by QueryFirstIDOnly and QueryOnlyIDOnly which pre-clone.
func queryIDsOnlyNoClone(ctx context.Context, base *QueryBase) ([]any, error) {
	from, err := resolvePathFrom(ctx, base)
	if err != nil {
		return nil, err
	}
	base.Columns = []string{base.IDColumn}
	spec := base.QuerySpec(base.IDFieldType)
	spec.From = from

	idFieldType := base.IDFieldType
	var ids []any
	spec.ScanValues = func(_ []string) ([]any, error) {
		return IDScanValues(idFieldType), nil
	}
	spec.Assign = func(_ []string, values []any) error {
		if len(values) == 0 {
			return fmt.Errorf("velox: QueryIDs: no values returned")
		}
		id, err := ExtractID(values[0], idFieldType)
		if err != nil {
			return err
		}
		ids = append(ids, id)
		return nil
	}
	if err := sqlgraph.QueryNodes(ctx, base.Driver, spec); err != nil {
		return nil, err
	}
	return ids, nil
}

// IDScanValues returns scanner values for the ID column based on field type.
func IDScanValues(ft field.Type) []any {
	switch ft {
	case field.TypeString, field.TypeUUID:
		return []any{new(sql.NullString)}
	default:
		// int, int64, uint, etc.
		return []any{new(sql.NullInt64)}
	}
}

// ExtractID extracts the ID value from a scanned sql.Null* value.
func ExtractID(v any, ft field.Type) (any, error) {
	switch ft {
	case field.TypeUUID:
		ns, ok := v.(*sql.NullString)
		if !ok {
			return nil, fmt.Errorf("velox: unexpected scan type %T for UUID ID", v)
		}
		id, err := uuid.Parse(ns.String)
		if err != nil {
			return nil, fmt.Errorf("velox: invalid UUID %q: %w", ns.String, err)
		}
		return id, nil
	case field.TypeString:
		ns, ok := v.(*sql.NullString)
		if !ok {
			return nil, fmt.Errorf("velox: unexpected scan type %T for string ID", v)
		}
		return ns.String, nil
	default:
		ni, ok := v.(*sql.NullInt64)
		if !ok {
			return nil, fmt.Errorf("velox: unexpected scan type %T for int ID", v)
		}
		return int(ni.Int64), nil
	}
}

// QueryFirstIDOnly returns the first matching entity ID. Uses lightweight ID-only scanning.
func QueryFirstIDOnly(ctx context.Context, base *QueryBase) (any, error) {
	clone := base.Clone()
	clone.SetLimit(1)
	ids, err := queryIDsOnlyNoClone(ctx, clone)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, NewNotFoundError(base.Ctx.Type)
	}
	return ids[0], nil
}

// QueryOnlyIDOnly returns the only matching entity ID. Uses lightweight ID-only scanning.
func QueryOnlyIDOnly(ctx context.Context, base *QueryBase) (any, error) {
	clone := base.Clone()
	clone.SetLimit(2)
	ids, err := queryIDsOnlyNoClone(ctx, clone)
	if err != nil {
		return nil, err
	}
	switch len(ids) {
	case 0:
		return nil, NewNotFoundError(base.Ctx.Type)
	case 1:
		return ids[0], nil
	default:
		return nil, NewNotSingularError(base.Ctx.Type)
	}
}

// AggregateFunc applies an aggregation step on a sql.Selector.
type AggregateFunc = func(*sql.Selector) string

// QueryGroupBy executes a GROUP BY query with aggregation.
func QueryGroupBy(ctx context.Context, q QueryReader, groupFields []string, fns []AggregateFunc, v any) error {
	var selector *sql.Selector
	if from, err := resolvePathFrom(ctx, q); err != nil {
		return err
	} else if from != nil {
		selector = from
	} else {
		selector = sql.Select().From(sql.Table(q.GetTable()))
	}
	selector.SetDialect(q.GetDriver().Dialect())

	// Apply predicates from the query.
	for _, p := range q.GetPredicates() {
		p(selector)
	}

	// Apply modifiers (e.g., multi-schema table name rewriting).
	for _, m := range q.GetModifiers() {
		m(selector)
	}

	// Apply ordering.
	for _, o := range q.GetOrder() {
		o(selector)
	}

	// Apply limit/offset.
	qctx := q.GetCtx()
	if limit := qctx.Limit; limit != nil {
		selector.Limit(*limit)
	}
	if offset := qctx.Offset; offset != nil {
		selector.Offset(*offset)
		if qctx.Limit == nil {
			selector.Limit(math.MaxInt32)
		}
	}

	// Add group-by columns.
	for _, f := range groupFields {
		selector.AppendSelect(f)
	}

	// Apply aggregate functions.
	for _, fn := range fns {
		agg := fn(selector)
		if agg != "" {
			selector.AppendSelect(agg)
		}
	}

	// Add GROUP BY.
	selector.GroupBy(groupFields...)

	rows := &sql.Rows{}
	query, args := selector.Query()
	drv := q.GetDriver()
	if err := drv.Query(ctx, query, args, rows); err != nil {
		return err
	}
	defer rows.Close()

	return sql.ScanSlice(rows, v)
}

// QueryScan executes the query with field projection and scans results into v.
// Unlike BuildSelector (which forces the ID column), QueryScan uses Ctx.Fields as-is
// for arbitrary projections like Select("name", "email").Scan(&results).
func QueryScan(ctx context.Context, q QueryReader, v any) error {
	return QuerySelect(ctx, q, nil, v)
}

// QuerySelect executes a Select-builder query, optionally with aggregate
// functions. It is the counterpart to QueryGroupBy for the no-grouping case.
//
// SELECT list resolution:
//   - fns != nil:             user-selected Fields (if any) + aggregate exprs
//   - fns == nil, Fields set: the user-selected fields only
//   - fns == nil, no Fields:  default entity columns (plain Scan behavior)
//
// This is what allows `client.User.Query().Aggregate(Sum(FieldAge)).Int(ctx)`
// to emit `SELECT SUM(age)` instead of `SELECT id, name, ..., SUM(age)`.
func QuerySelect(ctx context.Context, q QueryReader, fns []AggregateFunc, v any) error {
	selector, err := BuildQueryFrom(ctx, q)
	if err != nil {
		return err
	}
	qctx := q.GetCtx()
	switch {
	case len(fns) > 0:
		// Aggregate mode: start with any user-selected fields, then append
		// aggregate expressions. Default entity columns are NOT added — the
		// caller wants aggregate output only.
		selector.Select(qctx.Fields...)
		for _, fn := range fns {
			agg := fn(selector)
			if agg != "" {
				selector.AppendSelect(agg)
			}
		}
	case len(qctx.Fields) > 0:
		selector.Select(qctx.Fields...)
	default:
		selector.Select(q.GetColumns()...)
	}
	if qctx.Unique != nil && *qctx.Unique {
		selector.Distinct()
	}
	// Modifiers run LAST so they can override the SELECT list built above.
	// Matches Ent's sqlgraph.query.selector ordering and keeps behavior
	// consistent with BuildSelectorFrom.
	for _, m := range q.GetModifiers() {
		m(selector)
	}
	rows := &sql.Rows{}
	query, args := selector.Query()
	drv := q.GetDriver()
	if err := drv.Query(ctx, query, args, rows); err != nil {
		return err
	}
	defer rows.Close()
	return sql.ScanSlice(rows, v)
}

// =============================================================================
// ScanConfig
// =============================================================================

// ScanConfig holds scanning functions needed for edge loading and CRUD operations.
type ScanConfig struct {
	Table       string
	Columns     []string
	IDColumn    string
	IDFieldType field.Type
	ScanValues  func(columns []string) ([]any, error)
	New         func() any
	Assign      func(entity any, columns []string, values []any) error
	GetID       func(entity any) any
	SetDriver   func(entity any, drv dialect.Driver)
}

// =============================================================================
// Typed Scanning (ScanAll / ScanFirst / ScanOnly / ScanMapRows)
// =============================================================================

// Scannable is the interface that generated entity types implement for DB row scanning.
// Entity structs implement these methods directly for zero-wrapping scan.
// Methods are defined on pointer receivers, so the constraint is on *T.
type Scannable interface {
	ScanValues(columns []string) ([]any, error)
	AssignValues(columns []string, values []any) error
}

// ScannableOf constrains T such that *T implements Scannable.
// This allows ScanAll to create new(T) and call methods on the pointer receiver.
type ScannableOf[T any] interface {
	Scannable
	*T
}

// ScanAll executes the query and scans all rows into typed entity pointers.
// The build function returns a fully-configured *sql.Selector (with columns,
// DISTINCT, etc. already applied). ScanAll just executes and scans.
func ScanAll[T any, PT ScannableOf[T]](ctx context.Context, drv dialect.Driver, build func(context.Context) (*sql.Selector, error)) ([]*T, error) {
	selector, err := build(ctx)
	if err != nil {
		return nil, err
	}
	rows := &sql.Rows{}
	query, args := selector.Query()
	if qErr := drv.Query(ctx, query, args, rows); qErr != nil {
		return nil, qErr
	}
	defer rows.Close()
	scannedCols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("scan columns: %w", err)
	}
	var nodes []*T
	for rows.Next() {
		node := new(T)
		pt := PT(node)
		vals, err := pt.ScanValues(scannedCols)
		if err != nil {
			return nil, err
		}
		if err := rows.Scan(vals...); err != nil {
			return nil, err
		}
		if err := pt.AssignValues(scannedCols, vals); err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return nodes, nil
}

// ScanFirst executes the query with LIMIT 1 and returns the first result.
// LIMIT 1 is injected internally so callers don't need to set it.
// typeName is used for the NotFoundError message.
func ScanFirst[T any, PT ScannableOf[T]](ctx context.Context, drv dialect.Driver, build func(context.Context) (*sql.Selector, error), typeName string) (*T, error) {
	nodes, err := ScanAll[T, PT](ctx, drv, func(ctx context.Context) (*sql.Selector, error) {
		s, err := build(ctx)
		if err != nil {
			return nil, err
		}
		s.Limit(1)
		return s, nil
	})
	if err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		return nil, NewNotFoundError(typeName)
	}
	return nodes[0], nil
}

// ScanMapRows executes the query and scans all rows into []map[string]any.
// Values are scanned as their natural SQL types (int64, float64, string, []byte, etc.).
func ScanMapRows(ctx context.Context, drv dialect.Driver, build func(context.Context) (*sql.Selector, error)) ([]map[string]any, error) {
	selector, err := build(ctx)
	if err != nil {
		return nil, err
	}
	rows := &sql.Rows{}
	query, args := selector.Query()
	if qErr := drv.Query(ctx, query, args, rows); qErr != nil {
		return nil, qErr
	}
	defer rows.Close()

	columns, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
	}
	colNames := make([]string, len(columns))
	for i, c := range columns {
		colNames[i] = c.Name()
	}

	var result []map[string]any
	for rows.Next() {
		values := make([]any, len(colNames))
		for i := range values {
			values[i] = new(any)
		}
		if err := rows.Scan(values...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(colNames))
		for i, col := range colNames {
			row[col] = *(values[i].(*any)) //nolint:errcheck // values[i] is always *any from our allocation above
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// ScanOnly executes the query and returns exactly one result.
// LIMIT 2 is injected internally to detect non-singular results
// without scanning the full table.
// typeName is used for NotFoundError/NotSingularError messages.
func ScanOnly[T any, PT ScannableOf[T]](ctx context.Context, drv dialect.Driver, build func(context.Context) (*sql.Selector, error), typeName string) (*T, error) {
	nodes, err := ScanAll[T, PT](ctx, drv, func(ctx context.Context) (*sql.Selector, error) {
		s, err := build(ctx)
		if err != nil {
			return nil, err
		}
		s.Limit(2)
		return s, nil
	})
	if err != nil {
		return nil, err
	}
	switch len(nodes) {
	case 0:
		return nil, NewNotFoundError(typeName)
	case 1:
		return nodes[0], nil
	default:
		return nil, NewNotSingularError(typeName)
	}
}

// =============================================================================
// Selector (scalar accessor helpers for Select/GroupBy builders)
// =============================================================================

// Selector is embedded by Select and GroupBy builders to provide scalar
// accessor methods (Strings, Ints, Float64s, Bools and their singular/X
// variants). Follows Ent's selector pattern: defined once, embedded everywhere.
//
// Fields are unexported to prevent external mutation. Use NewSelector to
// construct and AppendFns to add aggregate functions.
type Selector struct {
	label string
	flds  *[]string
	fns   []AggregateFunc
	scan  func(context.Context, any) error
}

// NewSelector creates a Selector with the given label, field pointer, and scan function.
// Called by generated Select()/GroupBy() constructors.
func NewSelector(label string, flds *[]string, scan func(context.Context, any) error) Selector {
	return Selector{label: label, flds: flds, scan: scan}
}

// AppendFns adds aggregate functions to the selector.
// Called by generated Aggregate() methods.
func (s *Selector) AppendFns(fns ...AggregateFunc) {
	s.fns = append(s.fns, fns...)
}

// Fns returns the aggregate functions. Used by generated sqlScan methods.
func (s *Selector) Fns() []AggregateFunc {
	return s.fns
}

// ScanX is like Scan, but panics on error.
func (s *Selector) ScanX(ctx context.Context, v any) {
	if err := s.scan(ctx, v); err != nil {
		panic(err)
	}
}

// Strings returns string values. Requires exactly one field via Select().
func (s *Selector) Strings(ctx context.Context) ([]string, error) {
	if s.flds != nil && len(*s.flds) > 1 {
		return nil, errors.New("velox: Strings is not achievable when selecting more than 1 field")
	}
	var v []string
	if err := s.scan(ctx, &v); err != nil {
		return nil, err
	}
	return v, nil
}

// StringsX is like Strings, but panics on error.
func (s *Selector) StringsX(ctx context.Context) []string {
	v, err := s.Strings(ctx)
	if err != nil {
		panic(err)
	}
	return v
}

// String returns a single string value.
func (s *Selector) String(ctx context.Context) (_ string, err error) {
	var v []string
	if v, err = s.Strings(ctx); err != nil {
		return
	}
	switch len(v) {
	case 1:
		return v[0], nil
	case 0:
		err = NewNotFoundError(s.label)
	default:
		err = NewNotSingularError(s.label)
	}
	return
}

// StringX is like String, but panics on error.
func (s *Selector) StringX(ctx context.Context) string {
	v, err := s.String(ctx)
	if err != nil {
		panic(err)
	}
	return v
}

// Ints returns int values. Requires exactly one field via Select().
func (s *Selector) Ints(ctx context.Context) ([]int, error) {
	if s.flds != nil && len(*s.flds) > 1 {
		return nil, errors.New("velox: Ints is not achievable when selecting more than 1 field")
	}
	var v []int
	if err := s.scan(ctx, &v); err != nil {
		return nil, err
	}
	return v, nil
}

// IntsX is like Ints, but panics on error.
func (s *Selector) IntsX(ctx context.Context) []int {
	v, err := s.Ints(ctx)
	if err != nil {
		panic(err)
	}
	return v
}

// Int returns a single int value.
func (s *Selector) Int(ctx context.Context) (_ int, err error) {
	var v []int
	if v, err = s.Ints(ctx); err != nil {
		return
	}
	switch len(v) {
	case 1:
		return v[0], nil
	case 0:
		err = NewNotFoundError(s.label)
	default:
		err = NewNotSingularError(s.label)
	}
	return
}

// IntX is like Int, but panics on error.
func (s *Selector) IntX(ctx context.Context) int {
	v, err := s.Int(ctx)
	if err != nil {
		panic(err)
	}
	return v
}

// Float64s returns float64 values. Requires exactly one field via Select().
func (s *Selector) Float64s(ctx context.Context) ([]float64, error) {
	if s.flds != nil && len(*s.flds) > 1 {
		return nil, errors.New("velox: Float64s is not achievable when selecting more than 1 field")
	}
	var v []float64
	if err := s.scan(ctx, &v); err != nil {
		return nil, err
	}
	return v, nil
}

// Float64sX is like Float64s, but panics on error.
func (s *Selector) Float64sX(ctx context.Context) []float64 {
	v, err := s.Float64s(ctx)
	if err != nil {
		panic(err)
	}
	return v
}

// Float64 returns a single float64 value.
func (s *Selector) Float64(ctx context.Context) (_ float64, err error) {
	var v []float64
	if v, err = s.Float64s(ctx); err != nil {
		return
	}
	switch len(v) {
	case 1:
		return v[0], nil
	case 0:
		err = NewNotFoundError(s.label)
	default:
		err = NewNotSingularError(s.label)
	}
	return
}

// Float64X is like Float64, but panics on error.
func (s *Selector) Float64X(ctx context.Context) float64 {
	v, err := s.Float64(ctx)
	if err != nil {
		panic(err)
	}
	return v
}

// Bools returns bool values. Requires exactly one field via Select().
func (s *Selector) Bools(ctx context.Context) ([]bool, error) {
	if s.flds != nil && len(*s.flds) > 1 {
		return nil, errors.New("velox: Bools is not achievable when selecting more than 1 field")
	}
	var v []bool
	if err := s.scan(ctx, &v); err != nil {
		return nil, err
	}
	return v, nil
}

// BoolsX is like Bools, but panics on error.
func (s *Selector) BoolsX(ctx context.Context) []bool {
	v, err := s.Bools(ctx)
	if err != nil {
		panic(err)
	}
	return v
}

// Bool returns a single bool value.
func (s *Selector) Bool(ctx context.Context) (_ bool, err error) {
	var v []bool
	if v, err = s.Bools(ctx); err != nil {
		return
	}
	switch len(v) {
	case 1:
		return v[0], nil
	case 0:
		err = NewNotFoundError(s.label)
	default:
		err = NewNotSingularError(s.label)
	}
	return
}

// BoolX is like Bool, but panics on error.
func (s *Selector) BoolX(ctx context.Context) bool {
	v, err := s.Bool(ctx)
	if err != nil {
		panic(err)
	}
	return v
}

// =============================================================================
// Delete (DeleterBase + DeleteNodes)
// =============================================================================

// DeleterBase holds non-generic delete state.
type DeleterBase struct {
	Driver     dialect.Driver
	Table      string
	IDColumn   string
	IDType     field.Type
	FieldTypes map[string]field.Type
	Predicates []func(*sql.Selector)
	Schema     string // for multi-schema support
}

// ScanWithInterceptors runs sqlFn through the interceptor chain.
// Used by generated Select.Scan and GroupBy.Scan to avoid per-entity
// boilerplate for the interceptor iteration loop.
func ScanWithInterceptors(ctx context.Context, q Query, inters []Interceptor, sqlFn func(context.Context, any) error, v any) error {
	if len(inters) == 0 {
		return sqlFn(ctx, v)
	}
	qr := Querier(QuerierFunc(func(ctx context.Context, _ Query) (Value, error) {
		return nil, sqlFn(ctx, v)
	}))
	for i := len(inters) - 1; i >= 0; i-- {
		qr = inters[i].Intercept(qr)
	}
	_, err := qr.Query(ctx, q)
	return err
}

// RunTraversers iterates interceptors, calling Traverse on any that
// implement Traverser. Used by generated prepareQuery methods.
func RunTraversers(ctx context.Context, q Query, inters []Interceptor) error {
	for _, inter := range inters {
		if inter == nil {
			return fmt.Errorf("velox: uninitialized interceptor (forgotten import runtime?)")
		}
		if trv, ok := inter.(Traverser); ok {
			if err := trv.Traverse(ctx, q); err != nil {
				return err
			}
		}
	}
	return nil
}

// DeleteNodes executes DELETE and returns the number of affected rows.
func DeleteNodes(ctx context.Context, base *DeleterBase) (int, error) {
	spec := sqlgraph.NewDeleteSpec(base.Table,
		&sqlgraph.FieldSpec{
			Column: base.IDColumn,
			Type:   base.IDType,
		},
	)
	if base.Schema != "" {
		spec.Node.Schema = base.Schema
	}

	if len(base.Predicates) > 0 {
		spec.Predicate = func(s *sql.Selector) {
			for _, p := range base.Predicates {
				p(s)
			}
		}
	}

	n, err := sqlgraph.DeleteNodes(ctx, base.Driver, spec)
	if err != nil {
		return n, MayWrapConstraintError(err)
	}
	return n, nil
}
