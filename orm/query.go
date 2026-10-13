package orm

import (
	"context"
	stdsql "database/sql"
	"database/sql/driver"
	"fmt"
	"math"
	"reflect"
	"slices"
	"time"
	"unicode/utf8"

	schemafield "github.com/syssam/velox/schema/field"

	"github.com/google/uuid"

	"github.com/syssam/velox"
	"github.com/syssam/velox/dialect"
	"github.com/syssam/velox/dialect/sql"
	"github.com/syssam/velox/dialect/sql/sqlgraph"
	"github.com/syssam/velox/privacy"
)

// QueryCore is the query of any entity. Generated per-entity query types
// embed one and convert their typed arguments into its fields.
type QueryCore struct {
	M *Meta
	// Ex is what the query runs on: the database handle or transaction it
	// was built from. A query without one (the description of an eager load)
	// cannot be run.
	Ex       velox.Executor
	Preds    []P
	Orders   []O
	With     []*EdgeLoad
	Limit    int
	Offset   int
	Distinct bool
	Lock     Lock
	LockOpt  LockOption
	// Cols, when not empty, are the columns to read into the entities.
	Cols []string
	// nulls, when set, is told which of its columns were NULL in each row
	// read: a page's cursor needs to know, and a field that is not a
	// pointer cannot say.
	nulls *nullWatch
}

// nullWatch records, for the rows a query reads, which of some columns held
// NULL.
type nullWatch struct {
	cols []int    // indexes into Meta.Columns
	rows [][]bool // per row read, per watched column
}

// nullSeen scans a column and notes whether it was NULL.
type nullSeen struct {
	to   stdsql.Scanner
	null *bool
}

func (n nullSeen) Scan(v any) error {
	*n.null = v == nil
	return n.to.Scan(v)
}

// Columns narrows what the query reads to the named columns.
func (q *QueryCore) Columns(cols ...string) {
	q.Cols = append(q.Cols[:len(q.Cols):len(q.Cols)], cols...)
}

// projection returns the columns the query reads into its entities: nil for
// all of them, and otherwise the ones asked for together with the ones the
// query cannot do without (the id, what a page's cursor is made of, the key
// of an edge it loads that is stored in its own table).
func (q *QueryCore) projection() ([]string, error) {
	if len(q.Cols) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(q.Cols)+1)
	add := func(col string) {
		if !slices.Contains(out, col) {
			out = append(out, col)
		}
	}
	if id := q.M.id(); id != nil {
		add(id.Column)
	}
	for _, col := range q.Cols {
		if q.M.col(col) < 0 {
			return nil, fmt.Errorf("velox: %s has no column %q to read", q.M.Name, col)
		}
		add(col)
	}
	for _, o := range q.Orders {
		if o.Col != "" && q.M.col(o.Col) >= 0 {
			add(o.Col)
		}
	}
	for _, l := range q.With {
		if l.Edge != nil && l.Edge.fkOnOwner() {
			add(l.Edge.Columns[0])
		}
	}
	return out, nil
}

// Query is what query policies and interceptors receive: the pending query
// of any entity. Rules written against privacy.Filter narrow it with WhereP.
type Query struct{ c *QueryCore }

var _ privacy.Filterable = (*Query)(nil)

// Type returns the name of the entity queried.
func (q *Query) Type() string { return q.c.M.Name }

// WhereP adds conditions every returned row must satisfy. A nil function is
// no condition, as the zero predicate is in a query.
func (q *Query) WhereP(ps ...func(*sql.Selector)) {
	for _, p := range ps {
		if p != nil {
			q.c.Preds = append(q.c.Preds, P{Op: opRaw, raw: p})
		}
	}
}

// Filter returns the query as the target of privacy filter rules.
func (q *Query) Filter() privacy.Filter { return q }

// HasColumn reports whether the table of the entity queried has the column:
// for a rule shared by many entities (a mixin's), which filters on a column
// only some of them have.
func (q *Query) HasColumn(column string) bool { return q.c.M.col(column) >= 0 }

// Limit caps the number of rows.
func (q *Query) Limit(n int) { q.c.Limit = n }

// TraverseFunc is a function as a traverser: it is handed the query of every
// read before it runs, of any entity, to narrow it. Register it with
// DB.Intercept.
//
//	conn.Intercept(orm.TraverseFunc(func(ctx context.Context, q *orm.Query) error {
//		if q.HasColumn("deleted_at") {
//			q.WhereP(sql.FieldIsNull("deleted_at"))
//		}
//		return nil
//	}))
//
// It reaches reads only: see the README on why a rule about which rows a
// caller may touch belongs in the policy.
type TraverseFunc func(context.Context, *Query) error

// Intercept leaves the read as it is: a traverser acts before it.
func (f TraverseFunc) Intercept(next velox.Querier) velox.Querier { return next }

// Traverse calls f.
func (f TraverseFunc) Traverse(ctx context.Context, q velox.Query) error {
	oq, ok := q.(*Query)
	if !ok {
		return fmt.Errorf("velox: a traverser was handed a %T, not a query of this package", q)
	}
	return f(ctx, oq)
}

// InterceptFunc is a function as an interceptor that runs before the read it
// is handed and can refuse it: every read of an entity, the ones an eager
// load makes of its neighbors included.
type InterceptFunc func(context.Context, *Query) error

// Intercept runs f before the read.
func (f InterceptFunc) Intercept(next velox.Querier) velox.Querier {
	return velox.QuerierFunc(func(ctx context.Context, q velox.Query) (velox.Value, error) {
		oq, ok := q.(*Query)
		if !ok {
			return nil, fmt.Errorf("velox: an interceptor was handed a %T, not a query of this package", q)
		}
		if err := f(ctx, oq); err != nil {
			return nil, err
		}
		return next.Query(ctx, q)
	})
}

// Offset skips rows.
func (q *Query) Offset(n int) { q.c.Offset = n }

// guarded runs exec for the query inside the policy of the entity and the
// interceptors of the database handle. They work on a copy of the query, so
// what they add belongs to this execution and not to the builder.
func (q *QueryCore) guarded(ctx context.Context, c *velox.ExecutorState, exec func(context.Context, *QueryCore) (any, error)) (any, error) {
	if err := q.M.ready(); err != nil {
		return nil, err
	}
	inters := c.Interceptors()
	policy := q.M.policy()
	if policy == nil && len(inters) == 0 {
		return exec(ctx, q)
	}
	own := *q
	own.Preds = slices.Clone(q.Preds)
	qw := &Query{c: &own}
	if policy != nil {
		if err := policy.EvalQuery(ctx, qw); err != nil {
			return nil, err
		}
	}
	for _, in := range inters {
		if t, ok := in.(velox.Traverser); ok {
			if err := t.Traverse(ctx, qw); err != nil {
				return nil, err
			}
		}
	}
	var qr velox.Querier = velox.QuerierFunc(func(ctx context.Context, vq velox.Query) (velox.Value, error) {
		qq, ok := vq.(*Query)
		if !ok {
			return nil, fmt.Errorf("velox: an interceptor passed on a %T instead of the query it received", vq)
		}
		return exec(ctx, qq.c)
	})
	for i := len(inters) - 1; i >= 0; i-- {
		qr = inters[i].Intercept(qr)
	}
	return qr.Query(ctx, qw)
}

// conn returns the connection the query runs on.
func (q *QueryCore) conn() (*velox.ExecutorState, error) { return connOf(q.Ex, q.M) }

// connOf returns the connection of ex, or the error of running something of
// entity m that was not built from a handle.
func connOf(ex velox.Executor, m *Meta) (*velox.ExecutorState, error) {
	if m == nil {
		return nil, ErrNoEntity
	}
	if ex != nil {
		if c := ex.State(); c != nil && c.Driver() != nil {
			return c, nil
		}
	}
	return nil, fmt.Errorf("%w: %s", velox.ErrNoExecutor, m.Name)
}

// All runs the query and returns the entities, each a *Entity of q.M.
//
//go:noinline
func (q *QueryCore) All(ctx context.Context) ([]any, error) {
	c, err := q.conn()
	if err != nil {
		return nil, err
	}
	v, err := q.guarded(ctx, c, func(ctx context.Context, q *QueryCore) (any, error) { return q.all(ctx, c) })
	return rowsOf(q.M, v, err)
}

func (q *QueryCore) nullsOf() []int {
	if q.nulls == nil {
		return nil
	}
	return q.nulls.cols
}

// rowsOf is what a read of entities returned through the interceptors. An
// interceptor that hands back something else has replaced the result with
// what the caller cannot be given, and that is an error, not no rows.
func rowsOf(m *Meta, v any, err error) ([]any, error) {
	if err != nil || v == nil {
		return nil, err
	}
	rows, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("velox: an interceptor returned %T for the rows of %s", v, m.Name)
	}
	return rows, nil
}

// First returns the first entity, or ErrNotFound.
//
//go:noinline
func (q *QueryCore) First(ctx context.Context) (any, error) {
	c := *q
	c.Limit = 1
	rows, err := c.All(ctx)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, q.M.NotFound()
	}
	return rows[0], nil
}

// Only returns the single entity the query matches: ErrNotFound for none,
// ErrNotSingular for more than one.
//
//go:noinline
func (q *QueryCore) Only(ctx context.Context) (any, error) {
	c := *q
	c.Limit = 2
	rows, err := c.All(ctx)
	if err != nil {
		return nil, err
	}
	switch len(rows) {
	case 0:
		return nil, q.M.NotFound()
	case 1:
		return rows[0], nil
	default:
		return nil, q.M.notSingular()
	}
}

// Each runs the query and calls fn with one entity at a time, without holding
// them all: for results too large to load. fn returns false to stop. The rows
// stay open while fn runs, so on a transaction no other statement can run
// until Each returns; eager loads need all the rows and are refused.
//
//go:noinline
func (q *QueryCore) Each(ctx context.Context, fn func(any) bool) error {
	if len(q.With) > 0 {
		return fmt.Errorf("velox: iterating %s row by row cannot eager-load edges; use All or Paginate", q.M.Name)
	}
	c, cerr := q.conn()
	if cerr != nil {
		return cerr
	}
	_, err := q.guarded(ctx, c, func(ctx context.Context, q *QueryCore) (any, error) {
		read, perr := q.projection()
		if perr != nil {
			return nil, perr
		}
		sel := q.selector(ctx, c, read)
		query, args := sel.Query()
		if err := sel.Err(); err != nil {
			return nil, err
		}
		rows := &sql.Rows{}
		if err := c.Driver().Query(ctx, query, args, rows); err != nil {
			return nil, err
		}
		defer func() { _ = rows.Close() }()
		cols, err := rows.Columns()
		if err != nil {
			return nil, err
		}
		idx := make([]int, len(cols))
		for i, col := range cols {
			idx[i] = q.M.col(col)
		}
		dest := make([]any, len(cols))
		slots := make([]slot, len(cols))
		for rows.Next() {
			e := q.M.New()
			for i, at := range idx {
				if at >= 0 {
					dest[i] = q.M.dest(e, at, &slots[i])
				} else {
					dest[i] = discard{}
				}
			}
			if err := rows.Scan(dest...); err != nil {
				return nil, fmt.Errorf("velox: scanning %s: %w", q.M.Name, err)
			}
			if !fn(e) {
				return nil, nil
			}
		}
		return nil, rows.Err()
	})
	return err
}

// Count returns the number of rows the query matches, whatever window
// (Limit, Offset) it asks for.
//
//go:noinline
func (q *QueryCore) Count(ctx context.Context) (int, error) {
	c, err := q.conn()
	if err != nil {
		return 0, err
	}
	v, err := q.guarded(ctx, c, func(ctx context.Context, q *QueryCore) (any, error) { return q.count(ctx, c) })
	if err != nil {
		return 0, err
	}
	if v == nil {
		return 0, nil // as All returns no rows for an interceptor's nil
	}
	n, ok := v.(int)
	if !ok {
		return 0, fmt.Errorf("velox: an interceptor returned %T for the count of %s", v, q.M.Name)
	}
	return n, nil
}

func (q *QueryCore) count(ctx context.Context, c *velox.ExecutorState) (int, error) {
	all := *q
	all.Limit, all.Offset, all.Orders, all.Lock = 0, 0, nil, LockNone
	s := all.selector(ctx, c, nil)
	s.Count()
	query, args := s.Query()
	if err := s.Err(); err != nil {
		return 0, err
	}
	rows := &sql.Rows{}
	if err := c.Driver().Query(ctx, query, args, rows); err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	n := 0
	if rows.Next() {
		if err := rows.Scan(&n); err != nil {
			return 0, err
		}
	}
	return n, rows.Err()
}

// Exists reports whether the query matches a row.
//
//go:noinline
func (q *QueryCore) Exists(ctx context.Context) (bool, error) {
	c := *q
	c.Limit = 1
	c.With = nil
	c.Orders = nil // the order cannot change whether a row exists
	ids, err := c.IDs(ctx)
	return len(ids) > 0, err
}

// IDs returns the ids of the rows the query matches.
//
//go:noinline
func (q *QueryCore) IDs(ctx context.Context) ([]any, error) {
	c, err := q.conn()
	if err != nil {
		return nil, err
	}
	v, err := q.guarded(ctx, c, func(ctx context.Context, q *QueryCore) (any, error) { return q.ids(ctx, c) })
	return rowsOf(q.M, v, err)
}

func (q *QueryCore) ids(ctx context.Context, c *velox.ExecutorState) ([]any, error) {
	if q.M.id() == nil {
		return nil, fmt.Errorf("velox: %s has no single-column id", q.M.Name)
	}
	id := q.M.id().Column
	idx := q.M.col(id)
	// Ids are unique already, and SELECT DISTINCT id ORDER BY another column
	// is an error on Postgres.
	plain := *q
	plain.Distinct = false
	s := plain.selector(ctx, c, []string{id})
	query, args := s.Query()
	if err := s.Err(); err != nil {
		return nil, err
	}
	rows := &sql.Rows{}
	if err := c.Driver().Query(ctx, query, args, rows); err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []any
	for rows.Next() {
		e := q.M.New()
		var s slot
		if err := rows.Scan(q.M.dest(e, idx, &s)); err != nil {
			return nil, err
		}
		ids = append(ids, q.M.get(e, id))
	}
	return ids, rows.Err()
}

// loadErr returns the first error among the loads and their nested loads, so
// that an unsupported one is reported whether or not any row reaches it.
func loadErr(ls []*EdgeLoad) error {
	for _, l := range ls {
		if l.Err != nil {
			return l.Err
		}
		if err := loadErr(l.With); err != nil {
			return err
		}
	}
	return nil
}

// selector renders the query without its eager loads. cols nil selects every
// column of the entity.
func (q *QueryCore) selector(ctx context.Context, c *velox.ExecutorState, cols []string) *sql.Selector {
	if cols == nil {
		cols = q.M.Columns
	}
	b := sql.Dialect(c.Driver().Dialect())
	t := b.Table(q.M.Table)
	// Predicates read the request context from the selector (the policy of
	// an edge predicate's target is evaluated with it).
	s := b.Select(t.Columns(cols...)...).From(t).WithContext(ctx)
	for i := range q.Preds {
		compile(&q.Preds[i])(s)
	}
	for _, o := range q.Orders {
		if o.err != nil {
			s.AddError(o.err)
			continue
		}
		if o.raw != nil {
			o.raw(s)
			continue
		}
		if o.Col == "" {
			continue // the zero term orders by nothing, as the zero predicate filters nothing
		}
		if q.M.col(o.Col) < 0 {
			s.AddError(fmt.Errorf("velox: %s cannot be ordered by %q: not a column", q.M.Name, o.Col))
			continue
		}
		if o.Desc {
			s.OrderBy(sql.Desc(s.C(o.Col)))
		} else {
			s.OrderBy(sql.Asc(s.C(o.Col)))
		}
	}
	if q.Limit > 0 {
		s.Limit(q.Limit)
	}
	if q.Offset > 0 {
		// SQLite and MySQL accept OFFSET only after LIMIT.
		if q.Limit <= 0 {
			s.Limit(math.MaxInt32)
		}
		s.Offset(q.Offset)
	}
	// Postgres rejects a row lock on SELECT DISTINCT; the lock wins.
	if q.Distinct && (q.Lock == LockNone || dialect.GetCapabilities(c.Driver().Dialect()).Has(dialect.CapLockWithDistinct)) {
		s.Distinct()
	}
	q.lock(s)
	return s
}

// discard receives a column the entity has no field for.
type discard struct{}

func (discard) Scan(any) error { return nil }

// all runs the query, scans every row into a new entity and loads the
// requested edges.
func (q *QueryCore) all(ctx context.Context, c *velox.ExecutorState) ([]any, error) {
	if err := loadErr(q.With); err != nil {
		return nil, err
	}
	read, perr := q.projection()
	if perr != nil {
		return nil, perr
	}
	sel := q.selector(ctx, c, read)
	query, args := sel.Query()
	if err := sel.Err(); err != nil {
		return nil, err
	}
	rows := &sql.Rows{}
	if err := c.Driver().Query(ctx, query, args, rows); err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	// The position of each result column among the entity's, found once.
	idx := make([]int, len(cols))
	for i, col := range cols {
		idx[i] = q.M.col(col)
	}
	dest := make([]any, len(cols))
	slots := make([]slot, len(cols))
	var out []any
	for rows.Next() {
		e := q.M.New()
		var seen []bool
		if q.nulls != nil {
			seen = make([]bool, len(q.nulls.cols))
			q.nulls.rows = append(q.nulls.rows, seen)
		}
		for i, at := range idx {
			if at >= 0 {
				dest[i] = q.M.dest(e, at, &slots[i])
				if k := slices.Index(q.nullsOf(), at); k >= 0 {
					// Not for bytes that cannot be NULL: a driver hands an
					// empty value over as nil too (SQLite), and there nil
					// is the empty value, as it is when written.
					f := q.M.Field(cols[i])
					if sc, ok := dest[i].(stdsql.Scanner); ok && (f == nil || f.Type != schemafield.TypeBytes || !f.NotNull) {
						dest[i] = nullSeen{to: sc, null: &seen[k]}
					}
				}
			} else {
				dest[i] = discard{}
			}
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("velox: scanning %s: %w", q.M.Name, err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Close before loading edges: a transaction has one connection, and it
	// cannot run the edge queries while these rows are open.
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for _, l := range q.With {
		if err := loadEdge(ctx, c, l, out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// compile turns a predicate into the selector function the SQL layer takes.
// truth is what is known of a predicate before any row is seen.
type truth uint8

const (
	depends truth = iota // on the row
	holds                // for every row: the zero predicate, an empty And
	fails                // for no row: an empty Or
)

// combine compiles And, Or and Not over their operands, folding the ones
// whose truth is known. The SQL layer renders a combinator without operands
// as no condition at all; here And of nothing holds, Or of nothing fails and
// Not negates the conjunction of its operands, so that a condition built
// from a list of filters means the same thing when the list is empty: a
// delete of "any of these" over no filter removes nothing.
func combine(p *P) (func(*sql.Selector), truth) {
	fns := make([]func(*sql.Selector), 0, len(p.Subs))
	known := holds // of the operands seen so far, as a conjunction (And, Not)
	if p.Op == OpOr {
		known = fails // as a disjunction
	}
	for i := range p.Subs {
		sub := &p.Subs[i]
		var fn func(*sql.Selector)
		k := depends
		switch sub.Op {
		case 0:
			k = holds // the zero predicate is no condition
		case OpAnd, OpOr, OpNot:
			fn, k = combine(sub)
		default:
			fn = compile(sub)
		}
		switch {
		case k == depends:
			fns = append(fns, fn)
			continue
		case p.Op == OpOr && k == holds:
			return nil, holds
		case p.Op != OpOr && k == fails:
			known = fails
		}
	}
	switch p.Op {
	case OpOr:
		if len(fns) == 0 {
			return nil, fails
		}
		return sql.OrFuncs(fns...), depends
	case OpAnd:
		switch {
		case known == fails:
			return nil, fails
		case len(fns) == 0:
			return nil, holds
		}
		return sql.AndFuncs(fns...), depends
	}
	// Not: the negation of the conjunction.
	switch {
	case known == fails:
		return nil, holds
	case len(fns) == 0:
		return nil, fails
	}
	return sql.NotFuncs(fns...), depends
}

func compile(p *P) func(*sql.Selector) {
	switch p.Op {
	case 0:
		// The zero predicate is no condition: code that builds a predicate
		// only under some circumstance can pass the variable either way.
		return func(*sql.Selector) {}
	case opRaw:
		fn, _ := p.raw.(func(*sql.Selector))
		return fn
	case opInvalid:
		err, _ := p.Val.(error)
		return func(s *sql.Selector) { s.AddError(err) }
	case OpEQ:
		return sql.FieldEQ(p.Col, operand(p.Val))
	case OpNEQ:
		return sql.FieldNEQ(p.Col, operand(p.Val))
	case OpGT:
		return sql.FieldGT(p.Col, operand(p.Val))
	case OpGTE:
		return sql.FieldGTE(p.Col, operand(p.Val))
	case OpLT:
		return sql.FieldLT(p.Col, operand(p.Val))
	case OpLTE:
		return sql.FieldLTE(p.Col, operand(p.Val))
	case OpIn, OpNotIn:
		vals, ok := anys(p.Val)
		if !ok {
			return badOperand(p, "a list")
		}
		if p.Op == OpIn {
			return sql.FieldInValues(p.Col, vals...)
		}
		return sql.FieldNotInValues(p.Col, vals...)
	case OpIsNil:
		return sql.FieldIsNull(p.Col)
	case OpNotNil:
		return sql.FieldNotNull(p.Col)
	case OpContains, OpHasPrefix, OpHasSuffix, OpEqualFold, OpContainsFold:
		text, ok := str(p.Val)
		if !ok {
			return badOperand(p, "a string")
		}
		switch p.Op {
		case OpContains:
			return sql.FieldContains(p.Col, text)
		case OpHasPrefix:
			return sql.FieldHasPrefix(p.Col, text)
		case OpHasSuffix:
			return sql.FieldHasSuffix(p.Col, text)
		case OpEqualFold:
			return sql.FieldEqualFold(p.Col, text)
		}
		return sql.FieldContainsFold(p.Col, text)
	case OpAnd, OpOr, OpNot:
		fn, k := combine(p)
		switch k {
		case holds:
			return func(*sql.Selector) {}
		case fails:
			return func(s *sql.Selector) { s.Where(sql.False()) }
		}
		return fn
	case opNeighborOf:
		e, id := p.Edge, p.Val
		if e.fkOnOwner() {
			// The owner row holds the key of its neighbor. It is read from the
			// database, not from the entity in memory, which may be stale.
			return func(s *sql.Selector) {
				b := sql.Dialect(s.Dialect())
				ot := b.Table(e.Owner.Table)
				s.Where(sql.In(s.C(e.Target.id().Column),
					b.Select(ot.C(e.Columns[0])).From(ot).Where(sql.EQ(ot.C(e.Owner.id().Column), id))))
			}
		}
		// Target rows joined to one owner row through an M2M table.
		ownerCol, targetCol := e.Columns[0], e.Columns[1]
		if e.Inverse {
			ownerCol, targetCol = targetCol, ownerCol
		}
		return func(s *sql.Selector) {
			b := sql.Dialect(s.Dialect())
			jt := b.Table(e.Table)
			s.Where(sql.In(s.C(e.Target.id().Column),
				b.Select(jt.C(targetCol)).From(jt).Where(sql.EQ(jt.C(ownerCol), id))))
		}
	case OpHasEdge:
		e := p.Edge
		step := edgeStep(e)
		fns := make([]func(*sql.Selector), len(p.Subs))
		for i := range p.Subs {
			fns[i] = compile(&p.Subs[i])
		}
		if len(fns) == 0 && e.Target.policy() == nil {
			return func(s *sql.Selector) { sqlgraph.HasNeighbors(s, step) }
		}
		return func(s *sql.Selector) {
			sqlgraph.HasNeighborsWith(s, step, func(t *sql.Selector) {
				// The neighbors are rows of the target: its policy decides
				// which of them count, as it does for a query of the target.
				if err := targetPolicy(s.Context(), e.Target, t); err != nil {
					s.AddError(err)
					return
				}
				for _, fn := range fns {
					fn(t)
				}
			})
		}
	}
	return func(s *sql.Selector) { s.AddError(fmt.Errorf("velox: unknown predicate operator %d", p.Op)) }
}

// targetPolicy applies the query policy of m to t, the subquery of an edge
// predicate whose target is m.
func targetPolicy(ctx context.Context, m *Meta, t *sql.Selector) error {
	policy := m.policy()
	if policy == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	q := &Query{c: &QueryCore{M: m}}
	if err := policy.EvalQuery(ctx, q); err != nil {
		return err
	}
	for i := range q.c.Preds {
		compile(&q.c.Preds[i])(t)
	}
	return nil
}

// anys and str read the operand of a predicate the generated code built.
// operand is a value as it is compared with a column: a time as it would be
// stored (see stored). A row written with a time is found by that time,
// whatever digits it had below the microsecond.
func operand(v any) any {
	switch t := v.(type) {
	case time.Time:
		return stored(t)
	case *time.Time:
		if t != nil {
			at := stored(*t)
			return &at
		}
	case driver.Valuer:
		if at, ok := valuedTime(t); ok {
			return at
		}
	}
	return v
}

// anys returns the values of a list operand: a []any, or any other slice.
func anys(v any) ([]any, bool) {
	a, ok := v.([]any)
	if !ok {
		rv := reflect.ValueOf(v)
		if rv.Kind() != reflect.Slice {
			return nil, false
		}
		a = make([]any, rv.Len())
		for i := range a {
			a[i] = rv.Index(i).Interface()
		}
	} else {
		a = slices.Clone(a)
	}
	for i := range a {
		a[i] = operand(a[i])
	}
	return a, true
}

// str returns the text of a text operand: a string, or a value of a type
// declared on string.
func str(v any) (string, bool) {
	if s, ok := v.(string); ok {
		return s, true
	}
	if rv := reflect.ValueOf(v); rv.Kind() == reflect.String {
		return rv.String(), true
	}
	return "", false
}

// badOperand fails the query: an operand of the wrong kind must not become
// a condition that says something else (an empty pattern matches every row).
func badOperand(p *P, want string) func(*sql.Selector) {
	err := fmt.Errorf("velox: the condition on %q takes %s, not a %T", p.Col, want, p.Val)
	return func(s *sql.Selector) { s.AddError(err) }
}

func sqlgraphRel(r Rel) sqlgraph.Rel {
	switch r {
	case O2O:
		return sqlgraph.O2O
	case O2M:
		return sqlgraph.O2M
	case M2O:
		return sqlgraph.M2O
	default:
		return sqlgraph.M2M
	}
}

// edgeStep describes the traversal of e from its owner to its target.
func edgeStep(e *EdgeMeta) *sqlgraph.Step {
	return sqlgraph.NewStep(
		sqlgraph.From(e.Owner.Table, e.Owner.id().Column),
		sqlgraph.To(e.Target.Table, e.Target.id().Column),
		sqlgraph.Edge(sqlgraphRel(e.Rel), e.Inverse, e.Table, e.Columns...),
	)
}

// key normalizes a key so that the same key read from two places lands in
// one map bucket whatever its Go type: an id read as int and a foreign key
// read as int64, a uuid.UUID field and the text a join table returned for it,
// a named string and a plain one.
func key(v any) any {
	switch k := v.(type) {
	case int64, string:
		return k
	case int:
		return int64(k)
	case int32:
		return int64(k)
	case int16:
		return int64(k)
	case int8:
		return int64(k)
	case uint32:
		return int64(k)
	case uint:
		return unsignedKey(uint64(k))
	case uint64:
		return unsignedKey(k)
	case []byte:
		// Sixteen bytes that are not text are a UUID in binary form.
		if len(k) == 16 && !utf8.Valid(k) {
			if u, err := uuid.FromBytes(k); err == nil {
				return u.String()
			}
		}
		return string(k)
	case uuid.UUID:
		return k.String()
	}
	// A named type is keyed by its underlying value.
	switch rv := reflect.ValueOf(v); rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return unsignedKey(rv.Uint())
	case reflect.String:
		return rv.String()
	}
	return v
}

// unsignedKey is the int64 a driver returns for an unsigned column, or v
// itself when it does not fit.
func unsignedKey(v uint64) any {
	if v > math.MaxInt64 {
		return v
	}
	return int64(v)
}

func has(m map[any]struct{}, k any) bool {
	_, ok := m[k]
	return ok
}

// load runs the query of an eager load: the neighbors are read as a query of
// their entity would read them, under its policy and the interceptors.
func (q *QueryCore) load(ctx context.Context, c *velox.ExecutorState) ([]any, error) {
	v, err := q.guarded(ctx, c, func(ctx context.Context, q *QueryCore) (any, error) { return q.all(ctx, c) })
	return rowsOf(q.M, v, err)
}

// loadEdge loads one edge for all parents with one query (two for M2M) and
// assigns the neighbors through the edge's generated setter.
func loadEdge(ctx context.Context, c *velox.ExecutorState, l *EdgeLoad, parents []any) error {
	if l.Err != nil {
		return l.Err
	}
	if len(parents) == 0 {
		return nil
	}
	e := l.Edge
	// A target the caller may not read fails the load whether or not any
	// neighbor exists; otherwise the error would tell the caller which.
	if policy := e.Target.policy(); policy != nil {
		if err := policy.EvalQuery(ctx, &Query{c: &QueryCore{M: e.Target}}); err != nil {
			return err
		}
	}
	sub := &QueryCore{M: e.Target, Preds: append([]P(nil), l.Preds...), Orders: l.Orders, With: l.With}
	if len(l.Cols) > 0 {
		// With the key the neighbors are matched to their parents by, when
		// it is a column of theirs. (The id, and the keys of the edges
		// loaded from them in turn, the query adds itself.)
		sub.Cols = append([]string(nil), l.Cols...)
		if e.Rel != M2M && !e.fkOnOwner() {
			sub.Cols = append(sub.Cols, e.Columns[0])
		}
	}
	byParent := make(map[any][]any, len(parents))

	switch {
	case e.Rel == M2M:
		ownerCol, targetCol := e.Columns[0], e.Columns[1]
		if e.Inverse {
			ownerCol, targetCol = targetCol, ownerCol
		}
		ids := make([]any, len(parents))
		for i, p := range parents {
			ids[i] = e.Owner.get(p, e.Owner.id().Column)
		}
		b := sql.Dialect(c.Driver().Dialect())
		jt := b.Table(e.Table)
		js := b.Select(jt.C(ownerCol), jt.C(targetCol)).From(jt)
		sql.FieldInValues(ownerCol, ids...)(js)
		query, args := js.Query()
		rows := &sql.Rows{}
		if err := c.Driver().Query(ctx, query, args, rows); err != nil {
			return err
		}
		// owners lists, per target, the parents it is joined to, in join order.
		owners := make(map[any][]any)
		var targetIDs []any
		for rows.Next() {
			var o, t any
			if err := rows.Scan(&o, &t); err != nil {
				_ = rows.Close()
				return err
			}
			o, t = key(o), key(t)
			if _, ok := owners[t]; !ok {
				targetIDs = append(targetIDs, t)
			}
			owners[t] = append(owners[t], o)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if len(targetIDs) > 0 {
			sub.Preds = append(sub.Preds, P{Op: OpIn, Col: e.Target.id().Column, Val: targetIDs})
			targets, err := sub.load(ctx, c)
			if err != nil {
				return err
			}
			// Appending in the target query's order keeps that order within
			// each parent.
			for _, t := range targets {
				for _, o := range owners[key(e.Target.get(t, e.Target.id().Column))] {
					byParent[o] = append(byParent[o], t)
				}
			}
		}

	case e.fkOnOwner():
		fk := e.Columns[0]
		var fks []any
		seen := make(map[any]struct{})
		for _, p := range parents {
			v := e.Owner.get(p, fk)
			if v == nil {
				continue
			}
			if k := key(v); !has(seen, k) {
				seen[k] = struct{}{}
				fks = append(fks, v)
			}
		}
		byID := make(map[any]any, len(fks))
		if len(fks) > 0 {
			sub.Preds = append(sub.Preds, P{Op: OpIn, Col: e.Target.id().Column, Val: fks})
			targets, err := sub.load(ctx, c)
			if err != nil {
				return err
			}
			for _, t := range targets {
				byID[key(e.Target.get(t, e.Target.id().Column))] = t
			}
		}
		for _, p := range parents {
			var neighbors []any
			if v := e.Owner.get(p, fk); v != nil {
				if t, ok := byID[key(v)]; ok {
					neighbors = []any{t}
				}
			}
			e.link(p, neighbors)
		}
		return nil

	default: // the foreign key is a column of the target's table
		fk := e.Columns[0]
		ids := make([]any, len(parents))
		for i, p := range parents {
			ids[i] = e.Owner.get(p, e.Owner.id().Column)
		}
		sub.Preds = append(sub.Preds, P{Op: OpIn, Col: fk, Val: ids})
		children, err := sub.load(ctx, c)
		if err != nil {
			return err
		}
		for _, ch := range children {
			v := e.Target.get(ch, fk)
			if v == nil {
				continue
			}
			k := key(v)
			byParent[k] = append(byParent[k], ch)
		}
	}

	for _, p := range parents {
		children := byParent[key(e.Owner.get(p, e.Owner.id().Column))]
		if l.Limit > 0 && len(children) > l.Limit {
			children = children[:l.Limit]
		}
		e.link(p, children)
	}
	return nil
}
