package velox

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"reflect"
	"slices"
	"unsafe"
)

// Query is a SELECT on entity E, built by chaining and run by All, One, Iter
// or Count. A Query is a value: each chaining method returns a new Query and
// leaves the one it was called on as it was, so a base query can be extended
// in several ways, from several goroutines. Running a query does not change
// it either, so it can run again.
type Query[E any] struct {
	db    *DB
	meta  *Meta[E]
	st    Statement
	err   error      // a condition's error, returned by every way of running the query
	loads []loadSpec // the relations All loads
	after *E         // After's row, copied
}

// Query starts a query on entity E:
//
//	db.Query[billing.Invoice]().Where(billing.Invoices.Paid.EQ(false)).Limit(50).All(ctx)
func (db *DB) Query[E any, PE Entity[E]]() Query[E] {
	m := PE(nil).VeloxMeta()
	return Query[E]{db: db, meta: m, st: Statement{Op: OpSelect, Entity: m.info, InTx: db.inTx}}
}

// Where returns the query with conditions added. All of a query's conditions
// must hold.
func (q Query[E]) Where[_ Lazy[E]](ps ...Pred[E]) Query[E] {
	// Clipped, so that appending copies: another query extended from the
	// same one shares its backing array.
	q.st.Where = slices.Clip(q.st.Where)
	for _, p := range ps {
		q.st.Where = append(q.st.Where, Predicate(p))
		if q.err == nil {
			if err := predErr(p.n); err != nil {
				q.err = err
			}
		}
	}
	return q
}

// With returns the query also loading relation r of every entity it returns,
// with one more query per relation (chunked under the dialect's parameter
// limit). Chain With for several relations; r's With loads nested ones.
// Nothing is ever loaded lazily.
//
// The query must select the column each relation is keyed by (every column,
// unless Columns or Omit say otherwise). Entities loaded through a One are
// shared by every owner with the same key: changing one changes it for all.
func (q Query[E]) With[T any](r Rel[E, T]) Query[E] {
	sp := r.spec()
	if q.err == nil {
		q.err = specErr(sp)
	}
	q.loads = append(slices.Clip(q.loads), sp)
	return q
}

// OrderBy returns the query with orderings added, applied in the order given.
func (q Query[E]) OrderBy[_ Lazy[E]](os ...Order[E]) Query[E] {
	q.st.OrderBy = slices.Clip(q.st.OrderBy) // as in Where
	for _, o := range os {
		q.st.OrderBy = append(q.st.OrderBy, o.t)
	}
	return q
}

// Limit returns the query returning at most n rows. n <= 0 removes the limit.
func (q Query[E]) Limit[_ Lazy[E]](n int) Query[E] {
	q.st.Limit = max(n, 0)
	return q
}

// Offset returns the query skipping its first n rows. n <= 0 removes the
// offset.
func (q Query[E]) Offset[_ Lazy[E]](n int) Query[E] {
	q.st.Offset = max(n, 0)
	return q
}

// Unbounded returns the query declared to have no Limit on purpose. The
// guardrails refuse a query with neither (sub-project 2).
func (q Query[E]) Unbounded[_ Lazy[E]]() Query[E] {
	q.st.Unbounded = true
	return q
}

// Columns returns the query selecting only the given columns; the other
// fields keep their zero values. With no columns it selects every column.
func (q Query[E]) Columns[_ Lazy[E]](cs ...Column[E]) Query[E] {
	if len(cs) == 0 {
		q.st.Columns = nil
		return q
	}
	// A new slice, never the old one's array: no clip needed.
	q.st.Columns = make([]int, len(cs))
	for i, c := range cs {
		q.st.Columns[i] = int(c.column(nil))
	}
	return q
}

// Omit returns the query selecting every column except the given ones.
func (q Query[E]) Omit[_ Lazy[E]](cs ...Column[E]) Query[E] {
	if q.meta.err != nil {
		return q
	}
	skip := make(map[int32]bool, len(cs))
	for _, c := range cs {
		skip[c.column(nil)] = true
	}
	q.st.Columns = make([]int, 0, len(q.meta.info.Columns)) // a new slice, as in Columns
	for i := range q.meta.info.Columns {
		if !skip[int32(i)] {
			q.st.Columns = append(q.st.Columns, i)
		}
	}
	return q
}

// After returns the query continuing after x, the last row of the previous
// page: keyset pagination, which unlike Offset neither skips nor repeats rows
// when rows are added between pages, and costs the same on every page. The
// query's OrderBy must include every column of the primary key or of a
// unique index, and no column that can be NULL. x must be a row a query
// returned: its values carry the columns' stored precision, which a value made
// in memory (a time with nanoseconds in a column that keeps microseconds) may
// not, and the comparison would then skip or repeat a row. Count applies
// After's condition too.
func (q Query[E]) After[_ Lazy[E]](x *E) Query[E] {
	if x == nil {
		if q.err == nil && q.meta.err == nil {
			q.err = fmt.Errorf("velox: %s: After was given a nil row", q.meta.info.Name)
		}
		return q
	}
	c := *x
	q.after = &c
	return q
}

// sliceOrMap reports whether t, a reflect.Type, is a slice or map type. It is
// not generic, so that prepare holds no reflect.Type (design §3 rule 7).
func sliceOrMap(t any) bool {
	k := rtype(t).Kind()
	return k == reflect.Slice || k == reflect.Map
}

// prepare is the statement a run sends: the query's, with After's condition.
func (q Query[E]) prepare[_ Lazy[E]]() (Statement, error) {
	st := cloneStatement(&q.st)
	if q.after == nil {
		return st, nil
	}
	in := q.meta.info
	if len(st.OrderBy) == 0 {
		return st, fmt.Errorf("velox: %s: After needs an OrderBy", in.Name)
	}
	ordered := make(map[int]bool, len(st.OrderBy))
	for _, o := range st.OrderBy {
		col := in.Columns[o.Column]
		if col.Nullable {
			return st, fmt.Errorf("velox: %s: After cannot page over the nullable column %s", in.Name, col.Name)
		}
		// A nil slice or map is stored as NULL, which no comparison matches.
		if sliceOrMap(col.Type) {
			return st, fmt.Errorf("velox: %s: After cannot page over the column %s, which can be NULL", in.Name, col.Name)
		}
		ordered[o.Column] = true
	}
	covers := func(cols []int) bool {
		for _, c := range cols {
			if !ordered[c] {
				return false
			}
		}
		return len(cols) > 0
	}
	unique := covers(in.PrimaryKey)
	for _, ix := range in.Indexes {
		unique = unique || (ix.Unique && covers(ix.Columns))
	}
	if !unique {
		return st, fmt.Errorf("velox: %s: After needs an OrderBy that includes the primary key or a unique index", in.Name)
	}
	// (o1 > v1) OR (o1 = v1 AND o2 > v2) OR …, with < for descending columns.
	var or []node
	for i, o := range st.OrderBy {
		var and []node
		for _, prev := range st.OrderBy[:i] {
			and = append(and, node{op: opEQ, col: int32(prev.Column), v: fieldValue(q.meta, q.after, prev.Column)})
		}
		cmp := opGT
		if o.Desc {
			cmp = opLT
		}
		and = append(and, node{op: cmp, col: int32(o.Column), v: fieldValue(q.meta, q.after, o.Column)})
		if len(and) == 1 {
			or = append(or, and[0])
		} else {
			or = append(or, node{op: opAnd, kids: and})
		}
	}
	st.Where = append(st.Where, Predicate{node{op: opOr, kids: or}})
	return st, nil
}

// cloneStatement copies a query's statement for one run, with its slices
// capped so that anything velox itself appends to them (a rewrite hook's
// condition, say) cannot reach the query's slices. It does not copy their
// elements: DB.run does that before a hook, which may edit them, sees the
// statement.
func cloneStatement(st *Statement) Statement {
	c := *st
	c.Where = slices.Clip(c.Where)
	c.OrderBy = slices.Clip(c.OrderBy)
	c.Columns = slices.Clip(c.Columns)
	c.Set = slices.Clip(c.Set)
	c.Returning = slices.Clip(c.Returning)
	return c
}

func columnCount(st *Statement) int {
	if st.Columns != nil {
		return len(st.Columns)
	}
	return len(st.Entity.Columns)
}

// scanRow scans the current row into p through the entity's scan plan.
func scanRow[E any](r rows, m *Meta[E], cols []int, dst []any, p *E) error {
	for i := range dst {
		c := i
		if cols != nil {
			c = cols[i]
		}
		dst[i] = m.addr(p, c)
	}
	return r.Scan(dst...)
}

// Each way of running a query keeps its state (the statement, and what its
// rows are read into) in one struct, read by a function given a pointer to
// it. With hooks configured, the struct is allocated and run is used: hooks
// may keep what they are given, so all that the reading reaches is on the
// heap. With none, the struct is a local and plain is used, which keeps it,
// and the closure reading the rows, on the stack (design §8).

// allRun is the state of one All.
type allRun[E any] struct {
	st  Statement
	m   *Meta[E]
	out []E
}

// readAll reads every row into a.out, anew should an around hook retry.
func readAll[E any](a *allRun[E], r rows) error {
	dst := make([]any, columnCount(&a.st))
	a.out = make([]E, 0, min(a.st.Limit, 1024))
	for r.Next() {
		a.out = append(a.out, *new(E))
		if err := scanRow(r, a.m, a.st.Columns, dst, &a.out[len(a.out)-1]); err != nil {
			return err
		}
	}
	return nil
}

// All runs the query and returns every row.
func (q Query[E]) All[_ Lazy[E]](ctx context.Context) ([]E, error) {
	if q.meta.err != nil {
		return nil, q.meta.err
	}
	if q.err != nil {
		return nil, q.err
	}
	if len(q.loads) > 0 {
		if err := checkLoads(q.meta.info, q.st.Columns, q.loads); err != nil {
			return nil, err
		}
	}
	st, err := q.prepare()
	if err != nil {
		return nil, err
	}
	var out []E
	var ran []int // the columns the statement was run with, as the hooks left it
	if q.db.hooked() {
		a := &allRun[E]{st: st, m: q.meta}
		err = q.db.run(ctx, &a.st, func(r rows) error { return readAll(a, r) }, nil)
		out, ran = a.out, a.st.Columns
	} else {
		a := allRun[E]{st: st, m: q.meta}
		err = q.db.plain(ctx, &a.st, func(r rows) error { return readAll(&a, r) }, nil)
		out, ran = a.out, a.st.Columns
	}
	if err != nil {
		return nil, err
	}
	// A Rewrite hook may have narrowed the columns after the loads were checked.
	if len(q.loads) > 0 && ran != nil {
		if err := checkLoads(q.meta.info, ran, q.loads); err != nil {
			return nil, err
		}
	}
	if len(q.loads) > 0 && len(out) > 0 {
		ptrs := make([]unsafe.Pointer, len(out))
		for i := range out {
			ptrs[i] = unsafe.Pointer(&out[i])
		}
		if err := loadRelations(ctx, q.db, q.meta.info, ptrs, q.loads); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// One runs the query for exactly one row. Its error wraps ErrNotFound when no
// row matches and ErrNotSingular when more than one does.
func (q Query[E]) One[_ Lazy[E]](ctx context.Context) (*E, error) {
	q.st.Limit = 2
	out, err := q.All(ctx)
	if err != nil {
		return nil, err
	}
	switch len(out) {
	case 0:
		return nil, fmt.Errorf("%w: %s", ErrNotFound, q.meta.info.Name)
	case 1:
		return &out[0], nil
	}
	return nil, fmt.Errorf("%w: %s", ErrNotSingular, q.meta.info.Name)
}

// iterRun is the state of one Iter.
type iterRun[E any] struct {
	st      Statement
	m       *Meta[E]
	yield   func(E, error) bool
	yielded bool // a row was yielded
	stopped bool // the loop over Iter ended early
}

// errIterAgain is what Iter's reading returns when an around hook runs the
// statement again after rows were yielded, which cannot be taken back.
var errIterAgain = errors.New("an Around hook called next again after Iter had yielded rows")

// readIter yields each row until the loop over Iter ends.
func readIter[E any](it *iterRun[E], r rows) error {
	if it.yielded || it.stopped {
		return errIterAgain
	}
	dst := make([]any, columnCount(&it.st))
	for r.Next() {
		var e E
		if err := scanRow(r, it.m, it.st.Columns, dst, &e); err != nil {
			return err
		}
		it.yielded = true
		if !it.yield(e, nil) {
			it.stopped = true
			return nil
		}
	}
	return nil
}

// Iter runs the query and yields one row at a time; only the current row is
// held in memory.
//
// The rows are read while the around hooks run (see Around), so the body of
// the loop over Iter runs inside them: an around hook's timing or deadline
// covers it, and the statement's connection is held until the loop ends.
func (q Query[E]) Iter[_ Lazy[E]](ctx context.Context) iter.Seq2[E, error] {
	return func(yield func(E, error) bool) {
		var zero E
		if q.meta.err != nil {
			yield(zero, q.meta.err)
			return
		}
		if q.err != nil {
			yield(zero, q.err)
			return
		}
		if len(q.loads) > 0 {
			yield(zero, fmt.Errorf("velox: %s: Iter does not load relations; use All", q.meta.info.Name))
			return
		}
		st, err := q.prepare()
		if err != nil {
			yield(zero, err)
			return
		}
		var stopped bool
		if q.db.hooked() {
			it := &iterRun[E]{st: st, m: q.meta, yield: yield}
			err = q.db.run(ctx, &it.st, func(r rows) error { return readIter(it, r) }, nil)
			stopped = it.stopped
		} else {
			it := iterRun[E]{st: st, m: q.meta, yield: yield}
			err = q.db.plain(ctx, &it.st, func(r rows) error { return readIter(&it, r) }, nil)
			stopped = it.stopped
		}
		if err != nil && !stopped {
			yield(zero, err)
		}
	}
}

// countRun is the state of one Count.
type countRun struct {
	st Statement
	n  int64
}

func readCount(c *countRun, r rows) error {
	c.n = 0
	if r.Next() {
		return r.Scan(&c.n)
	}
	return nil
}

// Count returns the number of rows matching the query's conditions, After's
// among them (so After's rules for the ordering apply, though the ordering is
// then ignored); the query's ordering, limit, offset and columns are ignored.
func (q Query[E]) Count[_ Lazy[E]](ctx context.Context) (int64, error) {
	if q.meta.err != nil {
		return 0, q.meta.err
	}
	if q.err != nil {
		return 0, q.err
	}
	st, err := q.prepare()
	if err != nil {
		return 0, err
	}
	st.Count, st.Columns, st.OrderBy, st.Limit, st.Offset = true, nil, nil, 0, 0
	var n int64
	if q.db.hooked() {
		c := &countRun{st: st}
		err = q.db.run(ctx, &c.st, func(r rows) error { return readCount(c, r) }, nil)
		n = c.n
	} else {
		c := countRun{st: st}
		err = q.db.plain(ctx, &c.st, func(r rows) error { return readCount(&c, r) }, nil)
		n = c.n
	}
	if err != nil {
		return 0, err
	}
	return n, nil
}

// SQL returns the statement and arguments the query would send, without a
// database. Rewrite hooks have not run on it, so a condition a hook adds (a
// tenant's, for example) is not shown. It returns "" when the entity's
// metadata, a condition or After is invalid; All returns that error.
func (q Query[E]) SQL[_ Lazy[E]]() (string, []any) {
	if q.meta.err != nil {
		return "", nil
	}
	if q.err != nil {
		return "", nil
	}
	st, err := q.prepare()
	if err != nil {
		return "", nil
	}
	return render(q.db.d, &st)
}
