package velox

import (
	"context"
	"fmt"
	"iter"
	"slices"
	"time"
	"unsafe"

	"github.com/syssam/velox/internal/engine"
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
	err   error             // a condition's error, returned by every way of running the query
	loads []engine.LoadSpec // the relations All loads
	after *E                // After's row, copied
}

// Query starts a query on entity E:
//
//	db.Query[billing.Invoice]().Where(billing.Invoices.Paid.EQ(false)).Limit(50).All(ctx)
func (db *DB) Query[E any, PE Entity[E]]() Query[E] {
	m := PE(nil).VeloxMeta()
	return Query[E]{db: db, meta: m, st: Statement{Op: OpSelect, Entity: m.info, InTx: db.e.InTx}}
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
			if err := engine.PredErr(engine.PredNode(Predicate(p))); err != nil {
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
		q.err = engine.SpecErr(sp)
	}
	q.loads = append(slices.Clip(q.loads), sp)
	return q
}

// OrderBy returns the query with orderings added, applied in the order given.
// A nullable column's NULLs sort after every value ascending and before them
// descending, as PostgreSQL sorts them, on every dialect: on MySQL and SQLite
// a term "col IS NULL" goes before the column's, which an index on it does not
// serve.
func (q Query[E]) OrderBy[_ Lazy[E]](os ...Order[E]) Query[E] {
	q.st.OrderBy = slices.Clip(q.st.OrderBy) // as in Where
	for _, o := range os {
		q.st.OrderBy = append(q.st.OrderBy, o.t)
	}
	return q
}

// Ordered reports whether OrderBy was called on the query with at least one
// ordering. A caller that must order the query itself, such as a paginator,
// uses it to refuse a query already ordered.
func (q Query[E]) Ordered[_ Lazy[E]]() bool { return len(q.st.OrderBy) > 0 }

// Limit returns the query returning at most n rows. n <= 0 removes the limit;
// a query with no limit fails with ErrUnbounded unless it says Unbounded.
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

// Unbounded returns the query declared to have no Limit on purpose. A query
// with neither a Limit nor Unbounded fails with ErrUnbounded.
func (q Query[E]) Unbounded[_ Lazy[E]]() Query[E] {
	q.st.Unbounded = true
	return q
}

// Writable returns the query limited to the rows an Update or a Delete of E
// may change: after the tenant and the Read policy, the scope adds the
// entity's Write policy, as it does to an UPDATE. A Count or Exists of it
// checks a row before writes that do not go through E's own UPDATE, such as
// another entity's rows that point at it. The query stays a read: a Write
// policy's denial is a select's, and WithoutPolicy skips it as it skips Read.
// It does not reach the queries of a With load.
func (q Query[E]) Writable[_ Lazy[E]]() Query[E] {
	q.st.Writable = true
	return q
}

// ForUpdate returns the query locking the rows it reads until the
// transaction ends, as SELECT … FOR UPDATE: another transaction cannot
// change or lock them. A locking read runs only inside Tx, on PostgreSQL
// and MySQL; SQLite has none, as its transactions lock the database. It
// loads no relations, and Count, Sum, Avg, Min and Max refuse it.
//
// MySQL's InnoDB locks every row a locking read scans, not only those it
// returns, so on MySQL the filter wants an index: without one the read locks
// the table. One with ForUpdate locks up to 2 rows, the two it reads to tell
// a single row from several.
func (q Query[E]) ForUpdate[_ Lazy[E]]() Query[E] {
	q.st.Lock = LockForUpdate
	return q
}

// ForShare returns the query taking a shared lock on the rows it reads, as
// SELECT … FOR SHARE: other transactions may read and share-lock them, but
// not change them. The rules of ForUpdate apply.
func (q Query[E]) ForShare[_ Lazy[E]]() Query[E] {
	q.st.Lock = LockForShare
	return q
}

// SkipLocked returns the locking query leaving out the rows another
// transaction has locked, as SKIP LOCKED: a queue's workers each take rows
// no other worker holds. It needs ForUpdate or ForShare. On MySQL the
// rows the read scans are locked, so the filter wants an index here too.
func (q Query[E]) SkipLocked[_ Lazy[E]]() Query[E] {
	q.st.LockWait = LockSkipLocked
	return q
}

// NoWait returns the locking query failing at once, rather than waiting,
// when a row it reads is locked, as NOWAIT. DriverCode gives the failure's
// code: PostgreSQL's 55P03, MySQL's 3572. On PostgreSQL and MySQL the error
// matches ErrLocked. It needs ForUpdate or ForShare.
func (q Query[E]) NoWait[_ Lazy[E]]() Query[E] {
	q.st.LockWait = LockNoWait
	return q
}

// MaxRows returns the query allowed to return up to n rows, in place of the
// DB's WithMaxRows cap; n <= 0 restores the DB's cap. It covers the
// statements of the query's With loads too, nested ones included, except a
// load with Rel.LimitEach or Rel.MaxRows (see Rel.MaxRows). Say it where
// reading many rows is meant, so the exception is visible where the query is
// written. One reads up to 2 rows, so under MaxRows(1) a second matching row
// fails One with ErrTooManyRows rather than ErrNotSingular.
func (q Query[E]) MaxRows[_ Lazy[E]](n int) Query[E] {
	q.st.MaxRows = max(n, 0)
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

// Selects reports whether the query reads every one of cs: true for a query
// with no Columns or Omit.
func (q Query[E]) Selects[_ Lazy[E]](cs ...Column[E]) bool {
	if q.st.Columns == nil {
		return true
	}
	for _, c := range cs {
		if !slices.Contains(q.st.Columns, int(c.column(nil))) {
			return false
		}
	}
	return true
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
// unique index of columns that are not nullable, and no column that is encoded
// (a json field) or a slice. A nullable column pages too: NULL sorts after every
// value ascending and before them descending, on every dialect (see OrderBy),
// and After from a row whose value is NULL continues among the NULLs. With
// Columns or Omit, the query must select every column of its
// OrderBy, or it fails: a row without one holds its zero value there. x must
// be a row a query returned: its values carry the columns' stored precision,
// which a value made in memory (a time with nanoseconds in a column that keeps
// microseconds) may not, and the comparison would then skip or repeat a row.
// Count applies After's condition too.
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

// prepare is the statement a run sends: the query's, with After's condition.
func (q Query[E]) prepare[_ Lazy[E]]() (Statement, error) {
	st := engine.CloneStatement(&q.st)
	if q.after == nil {
		return st, nil
	}
	err := engine.AddAfter(&st, q.meta.info, unsafe.Pointer(q.after))
	return st, err
}

// scanRow scans the current row into p through the entity's scan plan; a
// JSON column through a decoder (ScanDst), and one of nilColumns through nilScanner.
func scanRow[E any](r engine.Rows, m *Meta[E], cols []int, dst []any, p *E) error {
	for i := range dst {
		c := i
		if cols != nil {
			c = cols[i]
		}
		if m.nilCols != nil && m.nilCols[c] {
			dst[i] = &nilScanner{dst: m.addr(p, c)}
			continue
		}
		dst[i] = engine.ScanDst(m.info, c, m.addr(p, c))
	}
	return r.Scan(dst...)
}

// Each way of running a query keeps its state (the statement, and what its rows
// are read into) in one struct, read by a function given a pointer to it. With
// hooks configured, the struct is allocated and run is used: hooks may keep
// what they are given, so all that the reading reaches is on the heap. With
// none, the struct is a local and plain is used, which keeps it, and the
// closure reading the rows, on the stack.

// firstRows is the most rows All allocates ahead, and rowGrowth how much further
// each time they fill, up to Limit. BenchmarkLimitBound on a 168-byte entity:
// Limit(1000) returning one row costs 3.5 KB at 16, 6.6 KB at 32, 11 KB at 64,
// against 173 KB at 1024. Growing by append, all 1000 rows cost 603 KB against 196
// KB at 1024; a read past 16 rows is likely a long one, so it grows eightfold.
const (
	firstRows = 16
	rowGrowth = 8
)

// allRun is the state of one All.
type allRun[E any] struct {
	st  Statement
	m   *Meta[E]
	out []E
}

// readAll reads every row into a.out, anew should an around hook retry.
func readAll[E any](a *allRun[E], r engine.Rows) error {
	dst := make([]any, engine.ColumnCount(&a.st))
	// Limit is mandatory, so it is a bound and not a likely size: at most firstRows
	// are allocated ahead for it, then rowGrowth times as many as they fill. The
	// Limit of an Unbounded read is the row cap's (cap+1): nothing is allocated
	// ahead for it, and append grows it.
	n := 0
	if !a.st.Unbounded {
		n = min(a.st.Limit, firstRows)
	}
	a.out = make([]E, 0, n)
	for r.Next() {
		if l := len(a.out); l == cap(a.out) && !a.st.Unbounded {
			a.out = slices.Grow(a.out, max(min(a.st.Limit, l*rowGrowth)-l, 1))
		}
		a.out = append(a.out, *new(E))
		if err := scanRow(r, a.m, a.st.Columns, dst, &a.out[len(a.out)-1]); err != nil {
			return err
		}
	}
	return nil
}

// read runs the query for its rows, without loading their relations. It
// returns the rows, the columns the statement ran with, as the hooks left
// them, and the connection it ran on.
func (q Query[E]) read[_ Lazy[E]](ctx context.Context) (out []E, ran []int, at engine.Target, err error) {
	if q.meta.err != nil {
		return nil, nil, at, q.meta.err
	}
	if q.err != nil {
		return nil, nil, at, q.err
	}
	if len(q.loads) > 0 {
		if err := q.lockedLoads(); err != nil {
			return nil, nil, at, err
		}
		if err := engine.CheckLoads(q.meta.info, q.st.Columns, q.loads); err != nil {
			return nil, nil, at, err
		}
	}
	st, err := q.prepare()
	if err != nil {
		return nil, nil, at, err
	}
	if q.db.e.Hooked() {
		a := &allRun[E]{st: st, m: q.meta}
		err = q.db.e.Run(ctx, &a.st, func(r engine.Rows) error { return readAll(a, r) }, nil)
		out, ran, at = a.out, a.st.Columns, engine.StmtRan(&a.st)
	} else {
		a := allRun[E]{st: st, m: q.meta}
		err = q.db.e.Plain(ctx, &a.st, func(r engine.Rows) error { return readAll(&a, r) }, nil)
		out, ran, at = a.out, a.st.Columns, engine.StmtRan(&a.st)
	}
	if err != nil {
		return nil, nil, at, err
	}
	return out, ran, at, nil
}

// lockedLoads refuses a locking query with With: the loads' rows would not
// be locked.
func (q Query[E]) lockedLoads[_ Lazy[E]]() error {
	if q.st.Lock != 0 {
		return fmt.Errorf("velox: %s: a locking read loads no relations; load them in another query", q.meta.info.Name)
	}
	return nil
}

// load loads the query's relations for rows, which ran on at with the columns
// ran.
func (q Query[E]) load[_ Lazy[E]](ctx context.Context, rows []E, ran []int, at engine.Target) error {
	if len(q.loads) == 0 || len(rows) == 0 {
		return nil
	}
	// A Rewrite hook may have narrowed the columns after the loads were checked.
	if ran != nil {
		if err := engine.CheckLoads(q.meta.info, ran, q.loads); err != nil {
			return err
		}
	}
	ptrs := make([]unsafe.Pointer, len(rows))
	for i := range rows {
		ptrs[i] = unsafe.Pointer(&rows[i])
	}
	return engine.LoadRelations(ctx, &q.db.e, q.db.e.LoadPin(at), q.meta.info, ptrs, q.loads, q.st.MaxRows)
}

// All runs the query and returns every row.
func (q Query[E]) All[_ Lazy[E]](ctx context.Context) ([]E, error) {
	out, ran, at, err := q.read(ctx)
	if err != nil {
		return nil, err
	}
	if err := q.load(ctx, out, ran, at); err != nil {
		return nil, err
	}
	return out, nil
}

// One runs the query for exactly one row. Its error wraps ErrNotFound when no
// row matches and ErrNotSingular when more than one does; it loads the
// query's relations only for a single row.
func (q Query[E]) One[_ Lazy[E]](ctx context.Context) (*E, error) {
	q.st.Limit = 2
	out, ran, at, err := q.read(ctx)
	if err != nil {
		return nil, err
	}
	switch len(out) {
	case 0:
		return nil, fmt.Errorf("velox: %s: %w", q.meta.info.Name, ErrNotFound)
	case 1:
		if err := q.load(ctx, out, ran, at); err != nil {
			return nil, err
		}
		return &out[0], nil
	}
	return nil, fmt.Errorf("velox: %s: %w", q.meta.info.Name, ErrNotSingular)
}

// First runs the query for its first row, in the query's order: a query
// without OrderBy returns any row that matches. It loads the query's
// relations for that row. Its error wraps ErrNotFound when no row matches.
func (q Query[E]) First[_ Lazy[E]](ctx context.Context) (*E, error) {
	q.st.Limit = 1
	out, ran, at, err := q.read(ctx)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("velox: %s: %w", q.meta.info.Name, ErrNotFound)
	}
	if err := q.load(ctx, out, ran, at); err != nil {
		return nil, err
	}
	return &out[0], nil
}

// iterRun is the state of one Iter.
type iterRun[E any] struct {
	st      Statement
	m       *Meta[E]
	yield   func(E, error) bool
	timed   bool // the DB has an Around hook: time the loop body
	yielded bool // a row was yielded
	stopped bool // the loop over Iter ended early
}

// readIter yields each row until the loop over Iter ends.
func readIter[E any](it *iterRun[E], r engine.Rows) error {
	if it.yielded || it.stopped {
		return engine.ErrIterAgain
	}
	dst := make([]any, engine.ColumnCount(&it.st))
	for r.Next() {
		var e E
		if err := scanRow(r, it.m, it.st.Columns, dst, &e); err != nil {
			return err
		}
		it.yielded = true
		engine.SetStmtHanded(&it.st)
		if !yieldRow(it.yield, e, it.timed, &it.st.Result.LoopBody) {
			it.stopped = true
			return nil
		}
	}
	return nil
}

// yieldRow yields e and, when timed, adds the time the loop body took to
// *body, also when the loop body panics.
func yieldRow[E any](yield func(E, error) bool, e E, timed bool, body *time.Duration) bool {
	if !timed {
		return yield(e, nil)
	}
	defer engine.AddSince(body, time.Now())
	return yield(e, nil)
}

// Iter runs the query and yields one row at a time; only the current row is
// held in memory.
//
// The rows are read while the around hooks run (see Around), so the body of
// the loop over Iter runs inside them: an around hook's timing or deadline
// covers it, and the statement's connection is held until the loop ends.
// WithDeadlines' Statement and MaxStatement bound the loop body too, and no
// query can lift them: a long export needs a DB opened without MaxStatement,
// or a context whose deadline is within it.
//
// On a pool of one connection, as Connect gives on SQLite, the loop holds that
// connection. A statement or a Tx on the DB in the body of the loop fails rather
// than wait for the loop that waits for it. Read the rows with All first, or run
// both in a Tx, whose statements share its connection. A statement on another
// goroutine waits for the loop to end, and is not refused; so one the body
// starts on a goroutine and waits for hangs until a deadline ends it, as do a
// body driven by iter.Pull and a statement on another DB over the same *sql.DB (a
// second Open, or OpenConn with SQLConn).
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
		engine.SetStmtIter(&st)
		var stopped bool
		if q.db.e.Hooked() {
			it := &iterRun[E]{st: st, m: q.meta, yield: yield, timed: len(q.db.e.Cfg.Arounds) > 0}
			err = q.db.e.Run(ctx, &it.st, func(r engine.Rows) error { return readIter(it, r) }, nil)
			stopped = it.stopped
		} else {
			it := iterRun[E]{st: st, m: q.meta, yield: yield}
			err = q.db.e.Plain(ctx, &it.st, func(r engine.Rows) error { return readIter(&it, r) }, nil)
			stopped = it.stopped
		}
		if err != nil && !stopped {
			yield(zero, err)
		}
	}
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
	q.st.Columns = nil // ignored, so After does not need its order columns in them
	st, err := q.prepare()
	if err != nil {
		return 0, err
	}
	st.Count, st.Columns, st.OrderBy, st.Limit, st.Offset = true, nil, nil, 0, 0
	var n int64
	if q.db.e.Hooked() {
		c := &engine.CountRun{St: st}
		err = q.db.e.Run(ctx, &c.St, func(r engine.Rows) error { return engine.ReadCount(c, r) }, nil)
		n = c.N
	} else {
		c := engine.CountRun{St: st}
		err = q.db.e.Plain(ctx, &c.St, func(r engine.Rows) error { return engine.ReadCount(&c, r) }, nil)
		n = c.N
	}
	if err != nil {
		return 0, err
	}
	return n, nil
}

// Exists reports whether a row matches the query's conditions, After's
// among them, with SELECT 1 … LIMIT 1. Like Count it ignores the query's
// ordering, limit, offset, columns and relations.
func (q Query[E]) Exists[_ Lazy[E]](ctx context.Context) (bool, error) {
	if q.meta.err != nil {
		return false, q.meta.err
	}
	if q.err != nil {
		return false, q.err
	}
	q.st.Columns = nil // as in Count
	st, err := q.prepare()
	if err != nil {
		return false, err
	}
	st.Exists, st.Columns, st.OrderBy, st.Limit, st.Offset = true, nil, nil, 1, 0
	var found bool
	if q.db.e.Hooked() {
		x := &engine.ExistsRun{St: st}
		err = q.db.e.Run(ctx, &x.St, func(r engine.Rows) error { return engine.ReadExists(x, r) }, nil)
		found = x.Found
	} else {
		x := engine.ExistsRun{St: st}
		err = q.db.e.Plain(ctx, &x.St, func(r engine.Rows) error { return engine.ReadExists(&x, r) }, nil)
		found = x.Found
	}
	if err != nil {
		return false, err
	}
	return found, nil
}

// SQL returns the statement and arguments All would send for ctx, or the
// error All would fail with before sending it; it sends nothing. It runs
// every stage All runs before execution: the guard, the Rewrite hooks, the
// tenant and policy scope, the row cap and rendering. So the hooks and the
// policy run, and can tell from IsDryRun(ctx), or a Rewrite hook from
// Statement.DryRun, that the statement will not be sent. Around hooks, which
// wrap execution, do not run.
func (q Query[E]) SQL[_ Lazy[E]](ctx context.Context) (string, []any, error) {
	ctx = engine.DryRunContext(ctx) // first, so that every hook sees it
	if q.meta.err != nil {
		return "", nil, q.meta.err
	}
	if q.err != nil {
		return "", nil, q.err
	}
	if len(q.loads) > 0 {
		if err := q.lockedLoads(); err != nil {
			return "", nil, err
		}
		if err := engine.CheckLoads(q.meta.info, q.st.Columns, q.loads); err != nil {
			return "", nil, err
		}
	}
	st, err := q.prepare()
	if err != nil {
		return "", nil, err
	}
	text, args, err := q.db.e.DryRun(ctx, &st)
	if err != nil {
		return "", nil, err
	}
	// As in All: a Rewrite hook may have narrowed the columns.
	if len(q.loads) > 0 && st.Columns != nil {
		if err := engine.CheckLoads(q.meta.info, st.Columns, q.loads); err != nil {
			return "", nil, err
		}
	}
	return text, args, nil
}
