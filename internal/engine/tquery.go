package engine

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"unsafe"
)

// FirstRows is the most rows a query's All allocates ahead, and RowGrowth how much further
// each time they fill, up to Limit. BenchmarkLimitBound on a 168-byte entity:
// Limit(1000) returning one row costs 3.5 KB at 16, 6.6 KB at 32, 11 KB at 64,
// against 173 KB at 1024. Growing by append, all 1000 rows cost 603 KB against 196
// KB at 1024; a read past 16 rows is likely a long one, so it grows eightfold.
const (
	FirstRows = 16
	RowGrowth = 8
)

// PageRows is the largest Limit All allocates in full ahead: a Limit that
// small is likely a page's size, and growing from FirstRows to it costs a
// second allocation and a copy. A larger Limit is a bound, and starts at
// FirstRows.
const PageRows = 128

// FirstCap is the number of rows All allocates ahead for st: none for an
// AllowUnbounded read, whose Limit is the row cap's, all of a Limit up to
// PageRows, and FirstRows of a larger one.
func FirstCap(st *Statement) int {
	switch {
	case st.Unbounded:
		return 0
	case st.Limit <= PageRows:
		return st.Limit
	default:
		return FirstRows
	}
}

// TableQuery is a SELECT on the entity of a Table, velox's Query with the
// entity's type erased: package untyped's TableQuery. It builds the
// Statement a Query builds, through the same pipeline, so the policies, a
// scope, the hooks, routing and the errors are the same. Like a Query it is
// a value: each chaining method returns a new TableQuery. Its rows are a *E
// in an any, and its columns and relations are indexes, as Statement numbers
// them.
type TableQuery struct {
	e     *DB
	t     *table
	st    Statement
	err   error
	loads []LoadSpec
	after unsafe.Pointer // After's row, copied
	joins *JoinSet
	// afterVals are AfterValues' values; nil for After's row alone.
	afterVals []any
	// orderVals says All reads the order values beside the rows
	// (OrderValues).
	orderVals bool
}

// TableQueryDB returns the DB q runs on: untyped.QueryDB.
func TableQueryDB(q TableQuery) *DB { return q.e }

// QueryTable starts a query on t's entity through e: untyped.Query.
func QueryTable(e *DB, t Table) TableQuery {
	s := t.state()
	return TableQuery{e: e, t: s, st: Statement{Op: OpSelect, Entity: s.info, InTx: e.InTx}}
}

// TableQueryOf is the TableQuery of a typed query's parts: untyped.FromQuery.
func TableQueryOf(e *DB, t Table, st Statement, err error, loads []LoadSpec, after unsafe.Pointer, joins *JoinSet) TableQuery {
	return TableQuery{e: e, t: t.state(), st: st, err: err, loads: loads, after: after, joins: joins}
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
			if err := PredErr(PredNode(p)); err != nil {
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

// Limit is Query.Limit, except that n <= 0 removes the limit: a caller
// such as veloxgql reuses a query and clears its Limit for a batch.
func (q TableQuery) Limit(n int) TableQuery {
	q.st.Limit = max(n, 0)
	return q
}

// Offset is Query.Offset.
func (q TableQuery) Offset(n int) TableQuery {
	q.st.Offset = max(n, 0)
	return q
}

// AllowUnbounded is Query.AllowUnbounded.
func (q TableQuery) AllowUnbounded() TableQuery {
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

// Writable is Query.Writable.
func (q TableQuery) Writable() TableQuery {
	q.st.Writable = true
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

// After returns the query continuing after row, a *E of its entity, as
// Query.After: the row is copied. Another row fails the query.
func (q TableQuery) After(row any) TableQuery {
	if q.t.err != nil {
		return q
	}
	p, err := q.t.rowOf(row)
	if err != nil {
		return q.fail(fmt.Errorf("velox: %s: After: %w", q.t.info.Name, err))
	}
	q.after, q.afterVals = q.t.copyRow(p), nil
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
	ref := RelRef{Owner: q.t.target.Get, Target: ts.target, I: int32(edge)}
	if ts.err != nil {
		return q.fail(ts.err)
	}
	l, err := ref.Link()
	if err != nil {
		return q.fail(err)
	}
	if l.Many || l.Join != nil {
		return q.fail(fmt.Errorf("velox: %s: %s is a to-many relation, and Join reads a to-one relation; load it with With, or test it with Has or HasWith", q.t.info.Name, l.Name))
	}
	if slices.ContainsFunc(q.st.Joins, func(j JoinClause) bool { return j.Ref.I == int32(edge) }) {
		return q.fail(fmt.Errorf("velox: %s: the query joins %s twice", q.t.info.Name, l.Name))
	}
	off, err := RelOffset(q.t.info, int32(edge))
	if err != nil {
		return q.fail(err)
	}
	q.st.Joins = append(slices.Clip(q.st.Joins), JoinClause{Ref: ref, Left: left})
	own := q.t
	set := JoinSet{BindOwner: func(cols []int, dst []any, owner unsafe.Pointer) { bindTableRow(own, cols, dst, owner) }}
	if q.joins != nil {
		set.Fills = slices.Clip(q.joins.Fills)
	}
	n := len(ts.info.Columns)
	set.Fills = append(set.Fills, JoinFill{Width: n, Off: off, Target: ts.info, Bind: func(dst []any) func(unsafe.Pointer) {
		row := ts.newRow()
		nulls := 0
		for c := range n {
			var d any
			if ts.nilCols != nil && ts.nilCols[c] {
				d = &NilScanner{Dst: ts.addr(row, c)}
			} else {
				d = ScanDst(ts.info, c, ts.addr(row, c))
			}
			if left {
				d = &NullProbe{Dst: d, Nulls: &nulls}
			}
			dst[c] = d
		}
		return func(owner unsafe.Pointer) {
			h := (*Holder)(unsafe.Add(owner, off))
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
			dst[i] = &NilScanner{Dst: t.addr(p, c)}
			continue
		}
		dst[i] = ScanDst(t.info, c, t.addr(p, c))
	}
}

// tableRun is the state of one TableQuery.All, as allRun is of Query.All.
type tableRun struct {
	st    Statement
	t     *table
	joins *JoinSet
	buf   reflect.Value // the rows' array, a []E
	n     int
	vals  *orderValueScan // the order values, when the statement reads them
}

// readTable reads every row into a.buf, anew should an around hook retry. It
// allocates as readAll does.
func readTable(a *tableRun, r Rows) error {
	rootN := ColumnCount(&a.st)
	a.vals = newOrderValueScan(&a.st)
	dst := make([]any, rootN+a.joins.Width()+a.vals.width())
	if a.vals != nil {
		copy(dst[len(dst)-a.vals.n:], a.vals.dst)
	}
	n := FirstCap(&a.st)
	typ := a.t.slice.(reflect.Type)
	a.buf, a.n = reflect.MakeSlice(typ, n, n), 0
	for r.Next() {
		if a.n == a.buf.Len() {
			c := max(2*a.n, FirstRows)
			if !a.st.Unbounded {
				c = max(min(a.st.Limit, a.n*RowGrowth), a.n+1)
			}
			b := reflect.MakeSlice(typ, c, c)
			reflect.Copy(b, a.buf)
			a.buf = b
		}
		p := unsafe.Add(a.buf.UnsafePointer(), uintptr(a.n)*a.t.size)
		a.n++
		if a.joins != nil {
			if err := a.joins.Scan(r, a.st.Columns, rootN, dst, p); err != nil {
				return err
			}
			if a.vals != nil {
				a.vals.keep(r)
			}
			continue
		}
		bindTableRow(a.t, a.st.Columns, dst[:rootN], p)
		if err := scanWithValues(r, dst, a.vals); err != nil {
			return err
		}
	}
	return nil
}

// rows is the run's rows.
func (a *tableRun) rows() TableRows {
	if a.n == 0 {
		return TableRows{size: a.t.size, info: a.t.info}
	}
	r := TableRows{p: a.buf.UnsafePointer(), size: a.t.size, n: a.n, info: a.t.info}
	if a.vals != nil {
		r.vals, r.nv = a.vals.vals, a.vals.n
	}
	return r
}

// check is the error any way of running q fails with before preparing it.
func (q TableQuery) check() error {
	if q.t.err != nil {
		return q.t.err
	}
	return q.err
}

// read runs the query for its rows, as Query.read.
func (q TableQuery) read(ctx context.Context) (out TableRows, ran []int, at Target, err error) {
	if err := q.check(); err != nil {
		return TableRows{}, nil, at, err
	}
	if len(q.loads) > 0 {
		if q.st.Lock != 0 {
			return TableRows{}, nil, at, fmt.Errorf("velox: %s: a locking read loads no relations; load them in another query", q.t.info.Name)
		}
		if err := CheckLoads(q.t.info, q.st.Columns, q.loads); err != nil {
			return TableRows{}, nil, at, err
		}
	}
	st, err := q.prepare()
	if err != nil {
		return TableRows{}, nil, at, err
	}
	st.OrderValues = q.orderVals
	if q.e.Hooked() {
		a := &tableRun{st: st, t: q.t, joins: q.joins}
		err = q.e.Run(ctx, &a.st, func(r Rows) error { return readTable(a, r) }, nil)
		out, ran, at = a.rows(), a.st.Columns, StmtRan(&a.st)
	} else {
		a := tableRun{st: st, t: q.t, joins: q.joins}
		err = q.e.Plain(ctx, &a.st, func(r Rows) error { return readTable(&a, r) }, nil)
		out, ran, at = a.rows(), a.st.Columns, StmtRan(&a.st)
	}
	if err != nil {
		return TableRows{}, nil, at, err
	}
	q.e.In(ctx).src.PutRows(q.t.info, out.p, out.size, out.n, q.joins.JoinFills())
	return out, ran, at, nil
}

// load loads the query's relations for rows, as Query.load.
func (q TableQuery) load(ctx context.Context, rows TableRows, ran []int, at Target) error {
	if len(q.loads) == 0 || rows.n == 0 {
		return nil
	}
	if ran != nil {
		if err := CheckLoads(q.t.info, ran, q.loads); err != nil {
			return err
		}
	}
	ptrs := make([]unsafe.Pointer, rows.n)
	for i := range ptrs {
		ptrs[i] = rows.at(i)
	}
	return LoadRelations(ctx, q.e, q.e.LoadPin(at), q.t.info, ptrs, q.loads, q.st.MaxRows)
}

// All runs the query and returns every row, as Query.All.
func (q TableQuery) All(ctx context.Context) (TableRows, error) {
	out, ran, at, err := q.read(ctx)
	if err != nil {
		return TableRows{}, err
	}
	if err := q.load(ctx, out, ran, at); err != nil {
		return TableRows{}, err
	}
	return out, nil
}

// One runs the query for exactly one row, as Query.One: its error wraps
// ErrNotFound when no row matches and ErrNotSingular when more than one does.
func (q TableQuery) One(ctx context.Context) (any, error) {
	p, err := q.one(ctx)
	if err != nil {
		return nil, err
	}
	return q.t.kit.Ptr(p), nil
}

// one is One's row.
func (q TableQuery) one(ctx context.Context) (unsafe.Pointer, error) {
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
		return out.at(0), nil
	}
	return nil, fmt.Errorf("velox: %s: %w", q.t.info.Name, ErrNotSingular)
}

// First runs the query for its first row, as Query.First: its error wraps
// ErrNotFound when no row matches.
func (q TableQuery) First(ctx context.Context) (any, error) {
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
	st, err := q.prepare()
	if err != nil {
		return 0, err
	}
	return CountStmt(ctx, q.e, st)
}

// Exists is Query.Exists.
func (q TableQuery) Exists(ctx context.Context) (bool, error) {
	if err := q.check(); err != nil {
		return false, err
	}
	q.st.Columns = nil // as in Query.Count
	st, err := q.prepare()
	if err != nil {
		return false, err
	}
	return ExistsStmt(ctx, q.e, st)
}

// SQL is Query.SQL.
func (q TableQuery) SQL(ctx context.Context) (string, []any, error) {
	ctx = DryRunContext(ctx) // first, so that every hook sees it
	if err := q.check(); err != nil {
		return "", nil, err
	}
	if len(q.loads) > 0 {
		if q.st.Lock != 0 {
			return "", nil, fmt.Errorf("velox: %s: a locking read loads no relations; load them in another query", q.t.info.Name)
		}
		if err := CheckLoads(q.t.info, q.st.Columns, q.loads); err != nil {
			return "", nil, err
		}
	}
	st, err := q.prepareAny()
	if err != nil {
		return "", nil, err
	}
	st.OrderValues = q.orderVals && st.Project == nil
	text, args, err := q.e.DryRun(ctx, &st)
	if err != nil {
		return "", nil, err
	}
	if len(q.loads) > 0 && st.Columns != nil {
		if err := CheckLoads(q.t.info, st.Columns, q.loads); err != nil {
			return "", nil, err
		}
	}
	return text, args, nil
}

// each is what LoadEach and CountEach read of q, as eachOf.
func (q TableQuery) each() EachQuery {
	e := EachQuery{DB: q.e, St: q.st, After: q.after, Loads: q.loads, Err: q.err, Joins: q.joins.Scanner(), In: q.t.info}
	if q.t.err != nil {
		e.Err = q.t.err
	}
	return e
}

// LoadEach is the package's LoadEach, untyped: for each of owners, rows of
// owner's entity (each a *E in an any), its rows of q related through
// owner's relation edge, whose target is q's entity, with at most each per
// owner (Rel.LimitEach; 0 for no limit).
func (q TableQuery) LoadEach(ctx context.Context, owner Table, edge, each int, owners []any) ([]TableRows, error) {
	os := owner.state()
	ps, err := tableOwners(os, owners)
	if err != nil {
		return nil, err
	}
	parts, err := LoadEach(ctx, q.each(), os.info, os.err, LoadSpec{Rel: int32(edge), Target: q.t.target, Each: max(each, 0)}, ps)
	if err != nil {
		return nil, err
	}
	out := make([]TableRows, len(parts))
	for i, p := range parts {
		out[i] = sliceRows(p, q.t.size, q.t.info)
	}
	return out, nil
}

// CountEach is the package's CountEach, untyped, as LoadEach is LoadEach.
func (q TableQuery) CountEach(ctx context.Context, owner Table, edge int, owners []any) ([]int64, error) {
	os := owner.state()
	ps, err := tableOwners(os, owners)
	if err != nil {
		return nil, err
	}
	return CountEach(ctx, q.each(), os.info, os.err, LoadSpec{Rel: int32(edge), Target: q.t.target}, ps)
}

// tableOwners is os.rowsOf(owners): the owner Table's error when it is
// invalid, as LoadEach and CountEach report it.
func tableOwners(os *table, owners []any) ([]unsafe.Pointer, error) {
	return os.rowsOf(owners)
}

// prepare is the statement a run of q sends, checked as the untyped entry
// points check theirs.
func (q TableQuery) prepare() (Statement, error) {
	if q.st.Project != nil {
		return Statement{}, errProjectRows(q.t.info.Name)
	}
	return q.prepareAny()
}

// prepareAny is prepare for a query that may hold a projection: SQL shows
// it and Rows reads it.
func (q TableQuery) prepareAny() (Statement, error) {
	st := CloneStatement(&q.st)
	if q.after != nil {
		if err := AddAfterValues(&st, q.t.info, q.after, q.afterVals, q.e.D); err != nil {
			return Statement{}, err
		}
	}
	return st, q.t.checkStatement(&st)
}

// PrepareStmt is the statement a run of a query sends: st cloned, with the
// condition of After's row (nil for none), a row of info's entity.
func PrepareStmt(st *Statement, info *EntityInfo, after unsafe.Pointer) (Statement, error) {
	c := CloneStatement(st)
	if after == nil {
		return c, nil
	}
	err := AddAfter(&c, info, after)
	return c, err
}

// CountStmt runs st, prepared, as a COUNT.
func CountStmt(ctx context.Context, db *DB, st Statement) (int64, error) {
	st.Count, st.Columns, st.OrderBy, st.Limit, st.Offset = true, nil, nil, 0, 0
	var n int64
	var err error
	if db.Hooked() {
		c := &CountRun{St: st}
		err = db.Run(ctx, &c.St, func(r Rows) error { return ReadCount(c, r) }, nil)
		n = c.N
	} else {
		// The count is scanned into a variable apart from st: Scan's
		// destination escapes, and a CountRun's N would take its Statement
		// to the heap.
		var cnt int64
		err = db.Plain(ctx, &st, func(r Rows) error { return scanCount(&cnt, r) }, nil)
		n = cnt
	}
	if err != nil {
		return 0, err
	}
	return n, nil
}

// ExistsStmt runs st, prepared, as SELECT 1 … LIMIT 1.
func ExistsStmt(ctx context.Context, db *DB, st Statement) (bool, error) {
	st.Exists, st.Columns, st.OrderBy, st.Limit, st.Offset = true, nil, nil, 1, 0
	var found bool
	var err error
	if db.Hooked() {
		x := &ExistsRun{St: st}
		err = db.Run(ctx, &x.St, func(r Rows) error { return ReadExists(x, r) }, nil)
		found = x.Found
	} else {
		x := ExistsRun{St: st}
		err = db.Plain(ctx, &x.St, func(r Rows) error { return ReadExists(&x, r) }, nil)
		found = x.Found
	}
	if err != nil {
		return false, err
	}
	return found, nil
}

// projection is a copy of q's Projection to extend, or a new one.
func (q TableQuery) projection() *Projection {
	if q.st.Project == nil {
		return &Projection{}
	}
	return cloneProject(q.st.Project)
}

// Select is Query.Select: the query reads terms, made by the package's
// ColumnTerm, ExprTerm and AggTerm, in place of the entity's columns, and is
// read with Rows.
func (q TableQuery) Select(ts ...Term) TableQuery {
	p := q.projection()
	p.Terms = append(p.Terms, ts...)
	q.st.Project = p
	return q
}

// GroupBy is Query.GroupBy. It needs Select.
func (q TableQuery) GroupBy(ts ...Term) TableQuery {
	if q.st.Project == nil {
		return q.fail(fmt.Errorf("velox: %s: GroupBy needs Select: it groups the terms Select reads", q.t.info.Name))
	}
	p := q.projection()
	p.GroupBy = append(p.GroupBy, ts...)
	q.st.Project = p
	return q
}

// Having is Query.Having. It needs Select.
func (q TableQuery) Having(ps ...Predicate) TableQuery {
	if q.st.Project == nil {
		return q.fail(fmt.Errorf("velox: %s: Having needs Select: it filters the groups Select reads", q.t.info.Name))
	}
	p := q.projection()
	for _, h := range ps {
		p.Having = append(p.Having, h)
		if err := PredErr(PredNode(h)); err != nil {
			q = q.fail(err)
		}
	}
	q.st.Project = p
	return q
}

// Rows is Query.Rows: the rows of the query's Select, one at a time, scanned
// by position.
func (q TableQuery) Rows(ctx context.Context) RawRows {
	return ProjectRows(ctx, q.e, func() (Statement, error) {
		name := q.t.info.Name
		switch {
		case q.check() != nil:
			return Statement{}, q.check()
		case q.st.Project == nil:
			return Statement{}, fmt.Errorf("velox: %s: Rows reads a projection: add Select", name)
		case len(q.loads) > 0:
			return Statement{}, fmt.Errorf("velox: %s: a projection cannot be read with With", name)
		case q.after != nil:
			return Statement{}, fmt.Errorf("velox: %s: a projection cannot be read After a row", name)
		}
		return q.prepareAny()
	})
}

// errProjectRows is the refusal of a reader of entities on a projection.
func errProjectRows(entity string) error {
	return fmt.Errorf("velox: %s: a projection reads terms, not entities: read it with Rows", entity)
}
