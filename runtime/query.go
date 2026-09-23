package runtime

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"

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
	// PartitionLimit caps the rows an eager-loaded to-many edge query keeps
	// per parent. Set by WithEdgeLoad from runtime.Limit; the parent's
	// loader applies it with sql.Selector.LimitPerPartition, partitioned by
	// the key that links each row to its parent.
	PartitionLimit *int
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
	if c.PartitionLimit != nil {
		v := *c.PartitionLimit
		clone.PartitionLimit = &v
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

// QueryReader provides read-only access to query state. Generated query
// types satisfy this interface. Top-level functions (BuildQueryFrom,
// BuildSelectorFrom, MakeQuerySpec) accept QueryReader so generated
// queries can share one implementation without an allocation bridge.
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

// CloneSlice returns nil if s is empty, otherwise an independent copy of s.
// Used by per-entity generated Query.clone() to keep
// the clone idiom in one place. Exported because generated query packages
// call it across the package boundary.
//
// Behavior note: returning nil (not an empty slice) for empty input lets
// callers skip redundant work when the source slice is unset. For len, range,
// and append, nil and an empty slice behave identically; note that nil != []T{}
// under reflect.DeepEqual and direct nil-checks, so this is not a drop-in
// replacement for `append([]T{}, s...)` if a caller relies on those. For
// populated slices, deep-copy semantics match. Pinned by
// TestCloneSlice_EmptyReturnsNil and TestCloneSlice_PopulatedDeepCopy
// in runtime/query_test.go.
func CloneSlice[T any](s []T) []T {
	if len(s) == 0 {
		return nil
	}
	out := make([]T, len(s))
	copy(out, s)
	return out
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
		// A projection may already name a key (the GraphQL collector adds
		// the keys the selected edges need); select each column once.
		for _, fk := range q.GetFKColumns() {
			if !slices.Contains(columns, fk) {
				columns = append(columns, fk)
			}
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
		for _, fk := range fkCols {
			if !slices.Contains(spec.Node.Columns, fk) {
				spec.Node.Columns = append(spec.Node.Columns, fk)
			}
		}
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
		// Return the exact Go type that matches ft so the generated id.(T) assertion
		// succeeds. All integer variants scan via NullInt64 (see IDScanValues), so we
		// convert here. Each case mirrors the BaseType mapping in generate_helper.go.
		switch ft {
		case field.TypeInt64:
			return ni.Int64, nil
		case field.TypeInt8:
			return int8(ni.Int64), nil
		case field.TypeInt16:
			return int16(ni.Int64), nil
		case field.TypeInt32:
			return int32(ni.Int64), nil
		case field.TypeUint:
			return uint(ni.Int64), nil
		case field.TypeUint8:
			return uint8(ni.Int64), nil
		case field.TypeUint16:
			return uint16(ni.Int64), nil
		case field.TypeUint32:
			return uint32(ni.Int64), nil
		case field.TypeUint64:
			return uint64(ni.Int64), nil
		default: // TypeInt (default velox ID type) and any future integer-like type
			return int(ni.Int64), nil
		}
	}
}

// AggregateFunc applies an aggregation step on a sql.Selector.
type AggregateFunc = func(*sql.Selector) string

// QueryGroupBy executes a GROUP BY query with aggregation.
//
// The selector is built like QuerySelect's: predicates, order and
// limit/offset first, then the group columns and aggregate expressions,
// DISTINCT when Unique is set, and modifiers LAST so they see (and may
// override) the finished SELECT list.
func QueryGroupBy(ctx context.Context, q QueryReader, groupFields []string, fns []AggregateFunc, v any) error {
	selector, err := BuildQueryFrom(ctx, q)
	if err != nil {
		return err
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
	selector.GroupBy(groupFields...)
	if qctx := q.GetCtx(); qctx.Unique != nil && *qctx.Unique {
		selector.Distinct()
	}
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
// Typed Scanning (ScanAll / ScanFirst)
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
