package velox

import (
	"context"
	"errors"
	"fmt"
	"iter"
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
func (q Query[E]) With[T any](r Rel[E, T]) Query[E] {
	q.loads = append(slices.Clip(q.loads), r.spec())
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
	var out []E
	var err error
	if q.db.hooked() {
		a := &allRun[E]{st: cloneStatement(&q.st), m: q.meta}
		err = q.db.run(ctx, &a.st, func(r rows) error { return readAll(a, r) }, nil)
		out = a.out
	} else {
		a := allRun[E]{st: cloneStatement(&q.st), m: q.meta}
		err = q.db.plain(ctx, &a.st, func(r rows) error { return readAll(&a, r) }, nil)
		out = a.out
	}
	if err != nil {
		return nil, err
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
		var err error
		var stopped bool
		if q.db.hooked() {
			it := &iterRun[E]{st: cloneStatement(&q.st), m: q.meta, yield: yield}
			err = q.db.run(ctx, &it.st, func(r rows) error { return readIter(it, r) }, nil)
			stopped = it.stopped
		} else {
			it := iterRun[E]{st: cloneStatement(&q.st), m: q.meta, yield: yield}
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

// Count returns the number of rows matching the query's conditions; its
// ordering, limit, offset and columns are ignored.
func (q Query[E]) Count[_ Lazy[E]](ctx context.Context) (int64, error) {
	if q.meta.err != nil {
		return 0, q.meta.err
	}
	if q.err != nil {
		return 0, q.err
	}
	st := cloneStatement(&q.st)
	st.Count, st.Columns, st.OrderBy, st.Limit, st.Offset = true, nil, nil, 0, 0
	var n int64
	var err error
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
// metadata or a condition is invalid; All returns that error.
func (q Query[E]) SQL[_ Lazy[E]]() (string, []any) {
	if q.meta.err != nil {
		return "", nil
	}
	if q.err != nil {
		return "", nil
	}
	st := cloneStatement(&q.st)
	return render(q.db.d, &st)
}
