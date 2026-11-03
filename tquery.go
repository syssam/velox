package velox

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"unsafe"

	"github.com/syssam/velox/internal/engine"
)

// TableQuery is a SELECT on the entity of a Table, Query with the entity's
// type erased. For generated code. It builds the Statement a Query builds,
// through the same pipeline, so the policies, a scope, the hooks, routing and
// the errors are the same; DB.QueryTable starts one, Untyped makes one of a
// Query and Typed a Query of one. Like a Query it is a value: each chaining
// method returns a new TableQuery. Its rows are unsafe.Pointers to the
// entity, and its columns and relations are indexes, as Statement numbers
// them.
type TableQuery struct {
	db    *DB
	t     *table
	st    Statement
	err   error
	loads []engine.LoadSpec
	after unsafe.Pointer // After's row, copied
	joins *joinSet
}

// QueryTable starts a query on t's entity: Query, untyped, for generated
// code (see TableQuery).
func (db *DB) QueryTable(t Table) TableQuery {
	s := t.state()
	return TableQuery{db: db, t: s, st: Statement{Op: OpSelect, Entity: s.info, InTx: db.e.InTx}}
}

// Untyped returns q as a TableQuery: the same statement, relations, After row
// and joins. For generated code.
func Untyped[E any](q Query[E]) TableQuery {
	u := TableQuery{db: q.db, st: q.st, err: q.err, loads: q.loads, after: unsafe.Pointer(q.after), joins: q.joins}
	if q.meta == nil {
		u.t = &table{err: errNoTable}
		return u
	}
	u.t = tableOfMeta(q.meta)
	return u
}

// Typed returns q as a Query of E, q's entity: the inverse of Untyped. For
// generated code. A q of another entity gives a query that fails.
func Typed[E any](q TableQuery) Query[E] {
	m, err := metaOf[E]()
	if m == nil {
		return Query[E]{db: q.db, meta: &Meta[E]{err: err}}
	}
	out := Query[E]{db: q.db, meta: m, st: q.st, err: q.err, loads: q.loads, after: (*E)(q.after), joins: q.joins}
	if m.err == nil && q.t.info != m.info {
		name := "no entity"
		if q.t.info != nil {
			name = q.t.info.Name
		}
		out.err = fmt.Errorf("velox: %s: Typed was given a query of %s", m.info.Name, name)
	}
	return out
}

// Table returns the query's Table.
func (q TableQuery) Table() Table { return Table{q.t} }

// fail is q with err as its error, unless it has one.
func (q TableQuery) fail(err error) TableQuery {
	if q.err == nil {
		q.err = err
	}
	return q
}

// Where returns the query with conditions added, as Query.Where.
func (q TableQuery) Where(ps ...Predicate) TableQuery {
	q.st.Where = slices.Clip(q.st.Where) // as in Query.Where
	for _, p := range ps {
		q.st.Where = append(q.st.Where, p)
		if q.err == nil {
			if err := engine.PredErr(engine.PredNode(p)); err != nil {
				q.err = err
			}
		}
	}
	return q
}

// OrderBy returns the query with orderings added, as Query.OrderBy.
func (q TableQuery) OrderBy(os ...OrderTerm) TableQuery {
	q.st.OrderBy = append(slices.Clip(q.st.OrderBy), os...)
	return q
}

// Ordered reports whether the query has an OrderBy, as Query.Ordered.
func (q TableQuery) Ordered() bool { return len(q.st.OrderBy) > 0 }

// Limit is Query.Limit.
func (q TableQuery) Limit(n int) TableQuery {
	q.st.Limit = max(n, 0)
	return q
}

// Offset is Query.Offset.
func (q TableQuery) Offset(n int) TableQuery {
	q.st.Offset = max(n, 0)
	return q
}

// Unbounded is Query.Unbounded.
func (q TableQuery) Unbounded() TableQuery {
	q.st.Unbounded = true
	return q
}

// MaxRows is Query.MaxRows.
func (q TableQuery) MaxRows(n int) TableQuery {
	q.st.MaxRows = max(n, 0)
	return q
}

// ForUpdate is Query.ForUpdate.
func (q TableQuery) ForUpdate() TableQuery {
	q.st.Lock = LockForUpdate
	return q
}

// ForShare is Query.ForShare.
func (q TableQuery) ForShare() TableQuery {
	q.st.Lock = LockForShare
	return q
}

// Columns returns the query selecting only the columns cs, indexes into the
// entity's columns, as Query.Columns; none selects every column. An index out
// of range fails the query.
func (q TableQuery) Columns(cs ...int) TableQuery {
	if len(cs) == 0 {
		q.st.Columns = nil
		return q
	}
	if q.t.info != nil {
		for _, c := range cs {
			if c < 0 || c >= len(q.t.info.Columns) {
				return q.fail(fmt.Errorf("velox: %s: Columns was given column %d, of %d", q.t.info.Name, c, len(q.t.info.Columns)))
			}
		}
	}
	q.st.Columns = slices.Clone(cs)
	return q
}

// Selects reports whether the query reads every column of cs, as
// Query.Selects.
func (q TableQuery) Selects(cs ...int) bool {
	if q.st.Columns == nil {
		return true
	}
	for _, c := range cs {
		if !slices.Contains(q.st.Columns, c) {
			return false
		}
	}
	return true
}

// After returns the query continuing after the row at p, as Query.After: the
// row is copied.
func (q TableQuery) After(p unsafe.Pointer) TableQuery {
	if q.t.err != nil {
		return q
	}
	if p == nil {
		return q.fail(fmt.Errorf("velox: %s: After was given a nil row", q.t.info.Name))
	}
	q.after = q.t.copyRow(p)
	return q
}

// Join returns the query also reading to-one relation edge, whose target is
// target, in the same statement: Query.Join, untyped.
func (q TableQuery) Join(edge int, target Table) TableQuery { return q.join(edge, target, false) }

// LeftJoin is Join as LEFT JOIN: Query.LeftJoin, untyped.
func (q TableQuery) LeftJoin(edge int, target Table) TableQuery { return q.join(edge, target, true) }

func (q TableQuery) join(edge int, target Table, left bool) TableQuery {
	if q.t.err != nil {
		return q
	}
	ts := target.state()
	ref := engine.RelRef{Owner: q.t.target.Get, Target: ts.target, I: int32(edge)}
	if ts.err != nil {
		return q.fail(ts.err)
	}
	l, err := ref.Link()
	if err != nil {
		return q.fail(err)
	}
	if l.Many || l.Join != nil {
		return q.fail(fmt.Errorf("velox: %s: %s is a to-many relation, and Join reads a to-one relation; load it with With, or test it with Has or HasWhere", q.t.info.Name, l.Name))
	}
	if slices.ContainsFunc(q.st.Joins, func(j engine.JoinClause) bool { return j.Ref.I == int32(edge) }) {
		return q.fail(fmt.Errorf("velox: %s: the query joins %s twice", q.t.info.Name, l.Name))
	}
	off, err := engine.RelOffset(q.t.info, int32(edge))
	if err != nil {
		return q.fail(err)
	}
	q.st.Joins = append(slices.Clip(q.st.Joins), engine.JoinClause{Ref: ref, Left: left})
	own := q.t
	set := joinSet{bindOwner: func(cols []int, dst []any, owner unsafe.Pointer) { bindTableRow(own, cols, dst, owner) }}
	if q.joins != nil {
		set.fills = slices.Clip(q.joins.fills)
	}
	n := len(ts.info.Columns)
	set.fills = append(set.fills, joinFill{width: n, bind: func(dst []any) func(unsafe.Pointer) {
		row := target.New()
		nulls := 0
		for c := range n {
			var d any
			if ts.nilCols != nil && ts.nilCols[c] {
				d = &nilScanner{dst: ts.addr(row, c)}
			} else {
				d = engine.ScanDst(ts.info, c, ts.addr(row, c))
			}
			if left {
				d = &nullProbe{dst: d, nulls: &nulls}
			}
			dst[c] = d
		}
		return func(owner unsafe.Pointer) {
			h := (*engine.Holder)(unsafe.Add(owner, off))
			h.Loaded = true
			if left && nulls == n {
				h.V = nil // as in Query.Join
				return
			}
			h.V = ts.kit.Ptr(row)
		}
	}})
	q.joins = &set
	return q
}

// bindTableRow is bindRow, untyped: it points dst at the fields of the row at
// p that cols (all, for nil) name.
func bindTableRow(t *table, cols []int, dst []any, p unsafe.Pointer) {
	for i := range dst {
		c := i
		if cols != nil {
			c = cols[i]
		}
		if t.nilCols != nil && t.nilCols[c] {
			dst[i] = &nilScanner{dst: t.addr(p, c)}
			continue
		}
		dst[i] = engine.ScanDst(t.info, c, t.addr(p, c))
	}
}

// tableRun is the state of one TableQuery.All, as allRun is of Query.All.
type tableRun struct {
	st    Statement
	t     *table
	joins *joinSet
	buf   reflect.Value // the rows' array, a []E
	n     int
}

// readTable reads every row into a.buf, anew should an around hook retry. It
// allocates as readAll does.
func readTable(a *tableRun, r engine.Rows) error {
	rootN := engine.ColumnCount(&a.st)
	dst := make([]any, rootN+a.joins.width())
	n := 0
	if !a.st.Unbounded {
		n = min(a.st.Limit, firstRows)
	}
	typ := a.t.slice.(reflect.Type)
	a.buf, a.n = reflect.MakeSlice(typ, n, n), 0
	for r.Next() {
		if a.n == a.buf.Len() {
			c := max(2*a.n, firstRows)
			if !a.st.Unbounded {
				c = max(min(a.st.Limit, a.n*rowGrowth), a.n+1)
			}
			b := reflect.MakeSlice(typ, c, c)
			reflect.Copy(b, a.buf)
			a.buf = b
		}
		p := unsafe.Add(a.buf.UnsafePointer(), uintptr(a.n)*a.t.size)
		a.n++
		if a.joins != nil {
			if err := a.joins.scan(r, a.st.Columns, rootN, dst, p); err != nil {
				return err
			}
			continue
		}
		bindTableRow(a.t, a.st.Columns, dst, p)
		if err := r.Scan(dst...); err != nil {
			return err
		}
	}
	return nil
}

// rows is the run's rows.
func (a *tableRun) rows() Rows {
	if a.n == 0 {
		return Rows{size: a.t.size}
	}
	return Rows{p: a.buf.UnsafePointer(), size: a.t.size, n: a.n}
}

// check is the error any way of running q fails with before preparing it.
func (q TableQuery) check() error {
	if q.t.err != nil {
		return q.t.err
	}
	return q.err
}

// read runs the query for its rows, as Query.read.
func (q TableQuery) read(ctx context.Context) (out Rows, ran []int, at engine.Target, err error) {
	if err := q.check(); err != nil {
		return Rows{}, nil, at, err
	}
	if len(q.loads) > 0 {
		if q.st.Lock != 0 {
			return Rows{}, nil, at, fmt.Errorf("velox: %s: a locking read loads no relations; load them in another query", q.t.info.Name)
		}
		if err := engine.CheckLoads(q.t.info, q.st.Columns, q.loads); err != nil {
			return Rows{}, nil, at, err
		}
	}
	st, err := prepareStmt(&q.st, q.t.info, q.after)
	if err != nil {
		return Rows{}, nil, at, err
	}
	if q.db.e.Hooked() {
		a := &tableRun{st: st, t: q.t, joins: q.joins}
		err = q.db.e.Run(ctx, &a.st, func(r engine.Rows) error { return readTable(a, r) }, nil)
		out, ran, at = a.rows(), a.st.Columns, engine.StmtRan(&a.st)
	} else {
		a := tableRun{st: st, t: q.t, joins: q.joins}
		err = q.db.e.Plain(ctx, &a.st, func(r engine.Rows) error { return readTable(&a, r) }, nil)
		out, ran, at = a.rows(), a.st.Columns, engine.StmtRan(&a.st)
	}
	if err != nil {
		return Rows{}, nil, at, err
	}
	return out, ran, at, nil
}

// load loads the query's relations for rows, as Query.load.
func (q TableQuery) load(ctx context.Context, rows Rows, ran []int, at engine.Target) error {
	if len(q.loads) == 0 || rows.n == 0 {
		return nil
	}
	if ran != nil {
		if err := engine.CheckLoads(q.t.info, ran, q.loads); err != nil {
			return err
		}
	}
	ptrs := make([]unsafe.Pointer, rows.n)
	for i := range ptrs {
		ptrs[i] = rows.At(i)
	}
	return engine.LoadRelations(ctx, &q.db.e, q.db.e.LoadPin(at), q.t.info, ptrs, q.loads, q.st.MaxRows)
}

// All runs the query and returns every row, as Query.All.
func (q TableQuery) All(ctx context.Context) (Rows, error) {
	out, ran, at, err := q.read(ctx)
	if err != nil {
		return Rows{}, err
	}
	if err := q.load(ctx, out, ran, at); err != nil {
		return Rows{}, err
	}
	return out, nil
}

// One runs the query for exactly one row, as Query.One: its error wraps
// ErrNotFound when no row matches and ErrNotSingular when more than one does.
func (q TableQuery) One(ctx context.Context) (unsafe.Pointer, error) {
	q.st.Limit = 2
	out, ran, at, err := q.read(ctx)
	if err != nil {
		return nil, err
	}
	switch out.n {
	case 0:
		return nil, fmt.Errorf("velox: %s: %w", q.t.info.Name, ErrNotFound)
	case 1:
		if err := q.load(ctx, out, ran, at); err != nil {
			return nil, err
		}
		return out.At(0), nil
	}
	return nil, fmt.Errorf("velox: %s: %w", q.t.info.Name, ErrNotSingular)
}

// First runs the query for its first row, as Query.First: its error wraps
// ErrNotFound when no row matches.
func (q TableQuery) First(ctx context.Context) (unsafe.Pointer, error) {
	q.st.Limit = 1
	out, ran, at, err := q.read(ctx)
	if err != nil {
		return nil, err
	}
	if out.n == 0 {
		return nil, fmt.Errorf("velox: %s: %w", q.t.info.Name, ErrNotFound)
	}
	if err := q.load(ctx, out, ran, at); err != nil {
		return nil, err
	}
	return out.At(0), nil
}

// Count is Query.Count.
func (q TableQuery) Count(ctx context.Context) (int64, error) {
	if err := q.check(); err != nil {
		return 0, err
	}
	q.st.Columns = nil // as in Query.Count
	st, err := prepareStmt(&q.st, q.t.info, q.after)
	if err != nil {
		return 0, err
	}
	return countStmt(ctx, q.db, st)
}

// Exists is Query.Exists.
func (q TableQuery) Exists(ctx context.Context) (bool, error) {
	if err := q.check(); err != nil {
		return false, err
	}
	q.st.Columns = nil // as in Query.Count
	st, err := prepareStmt(&q.st, q.t.info, q.after)
	if err != nil {
		return false, err
	}
	return existsStmt(ctx, q.db, st)
}

// SQL is Query.SQL.
func (q TableQuery) SQL(ctx context.Context) (string, []any, error) {
	ctx = engine.DryRunContext(ctx) // first, so that every hook sees it
	if err := q.check(); err != nil {
		return "", nil, err
	}
	if len(q.loads) > 0 {
		if q.st.Lock != 0 {
			return "", nil, fmt.Errorf("velox: %s: a locking read loads no relations; load them in another query", q.t.info.Name)
		}
		if err := engine.CheckLoads(q.t.info, q.st.Columns, q.loads); err != nil {
			return "", nil, err
		}
	}
	st, err := prepareStmt(&q.st, q.t.info, q.after)
	if err != nil {
		return "", nil, err
	}
	text, args, err := q.db.e.DryRun(ctx, &st)
	if err != nil {
		return "", nil, err
	}
	if len(q.loads) > 0 && st.Columns != nil {
		if err := engine.CheckLoads(q.t.info, st.Columns, q.loads); err != nil {
			return "", nil, err
		}
	}
	return text, args, nil
}

// each is what LoadEach and CountEach read of q, as eachOf.
func (q TableQuery) each() engine.EachQuery {
	e := engine.EachQuery{St: q.st, After: q.after, Loads: q.loads, Err: q.err, Joins: q.joins.scanner(), In: q.t.info}
	if q.db != nil {
		e.DB = &q.db.e
	}
	if q.t.err != nil {
		e.Err = q.t.err
	}
	return e
}

// LoadEach is the package's LoadEach, untyped: for each row at owners, rows
// of owner's entity, its rows of q related through owner's relation edge,
// whose target is q's entity, with at most each per owner (Rel.LimitEach; 0
// for no limit).
func (q TableQuery) LoadEach(ctx context.Context, owner Table, edge, each int, owners []unsafe.Pointer) ([]Rows, error) {
	os := owner.state()
	parts, err := engine.LoadEach(ctx, q.each(), os.info, os.err, engine.LoadSpec{Rel: int32(edge), Target: q.t.target, Each: max(each, 0)}, owners)
	if err != nil {
		return nil, err
	}
	out := make([]Rows, len(parts))
	for i, p := range parts {
		out[i] = sliceRows(p, q.t.size)
	}
	return out, nil
}

// CountEach is the package's CountEach, untyped, as LoadEach is LoadEach.
func (q TableQuery) CountEach(ctx context.Context, owner Table, edge int, owners []unsafe.Pointer) ([]int64, error) {
	os := owner.state()
	return engine.CountEach(ctx, q.each(), os.info, os.err, engine.LoadSpec{Rel: int32(edge), Target: q.t.target}, owners)
}

// prepareStmt is the statement a run of a query sends: st cloned, with the
// condition of After's row (nil for none), a row of info's entity.
func prepareStmt(st *Statement, info *EntityInfo, after unsafe.Pointer) (Statement, error) {
	c := engine.CloneStatement(st)
	if after == nil {
		return c, nil
	}
	err := engine.AddAfter(&c, info, after)
	return c, err
}

// countStmt runs st, prepared, as a COUNT.
func countStmt(ctx context.Context, db *DB, st Statement) (int64, error) {
	st.Count, st.Columns, st.OrderBy, st.Limit, st.Offset = true, nil, nil, 0, 0
	var n int64
	var err error
	if db.e.Hooked() {
		c := &engine.CountRun{St: st}
		err = db.e.Run(ctx, &c.St, func(r engine.Rows) error { return engine.ReadCount(c, r) }, nil)
		n = c.N
	} else {
		c := engine.CountRun{St: st}
		err = db.e.Plain(ctx, &c.St, func(r engine.Rows) error { return engine.ReadCount(&c, r) }, nil)
		n = c.N
	}
	if err != nil {
		return 0, err
	}
	return n, nil
}

// existsStmt runs st, prepared, as SELECT 1 … LIMIT 1.
func existsStmt(ctx context.Context, db *DB, st Statement) (bool, error) {
	st.Exists, st.Columns, st.OrderBy, st.Limit, st.Offset = true, nil, nil, 1, 0
	var found bool
	var err error
	if db.e.Hooked() {
		x := &engine.ExistsRun{St: st}
		err = db.e.Run(ctx, &x.St, func(r engine.Rows) error { return engine.ReadExists(x, r) }, nil)
		found = x.Found
	} else {
		x := engine.ExistsRun{St: st}
		err = db.e.Plain(ctx, &x.St, func(r engine.Rows) error { return engine.ReadExists(&x, r) }, nil)
		found = x.Found
	}
	if err != nil {
		return false, err
	}
	return found, nil
}
