package velox

import (
	"context"
	"fmt"
	"iter"
	"slices"
)

// Query is a SELECT on entity E, built by chaining and run by All, One, Iter
// or Count. Running a query does not change it, so it can run again.
type Query[E any] struct {
	db   *DB
	meta *Meta[E]
	st   Statement
}

// Query starts a query on entity E:
//
//	db.Query[billing.Invoice]().Where(billing.Invoices.Paid.EQ(false)).Limit(50).All(ctx)
func (db *DB) Query[E any, PE Entity[E]]() *Query[E] {
	m := PE(nil).VeloxMeta()
	return &Query[E]{db: db, meta: m, st: Statement{Op: OpSelect, Entity: m.info, InTx: db.inTx}}
}

// Where adds conditions. All of a query's conditions must hold.
func (q *Query[E]) Where[_ Lazy[E]](ps ...Pred[E]) *Query[E] {
	for _, p := range ps {
		q.st.Where = append(q.st.Where, Predicate{p.n})
	}
	return q
}

// OrderBy adds orderings, applied in the order given.
func (q *Query[E]) OrderBy[_ Lazy[E]](os ...Order[E]) *Query[E] {
	for _, o := range os {
		q.st.OrderBy = append(q.st.OrderBy, o.t)
	}
	return q
}

// Limit returns at most n rows. n <= 0 removes the limit.
func (q *Query[E]) Limit[_ Lazy[E]](n int) *Query[E] {
	q.st.Limit = max(n, 0)
	return q
}

// Offset skips the first n rows. n <= 0 removes the offset.
func (q *Query[E]) Offset[_ Lazy[E]](n int) *Query[E] {
	q.st.Offset = max(n, 0)
	return q
}

// Unbounded declares that the query has no Limit on purpose. The guardrails
// refuse a query with neither (sub-project 2).
func (q *Query[E]) Unbounded[_ Lazy[E]]() *Query[E] {
	q.st.Unbounded = true
	return q
}

// Columns selects only the given columns; the other fields keep their zero
// values. With no columns it selects every column.
func (q *Query[E]) Columns[_ Lazy[E]](cs ...Column[E]) *Query[E] {
	if len(cs) == 0 {
		q.st.Columns = nil
		return q
	}
	q.st.Columns = make([]int, len(cs))
	for i, c := range cs {
		q.st.Columns[i] = int(c.column(nil))
	}
	return q
}

// Omit selects every column except the given ones.
func (q *Query[E]) Omit[_ Lazy[E]](cs ...Column[E]) *Query[E] {
	if q.meta.err != nil {
		return q
	}
	skip := make(map[int32]bool, len(cs))
	for _, c := range cs {
		skip[c.column(nil)] = true
	}
	q.st.Columns = make([]int, 0, len(q.meta.info.Columns))
	for i := range q.meta.info.Columns {
		if !skip[int32(i)] {
			q.st.Columns = append(q.st.Columns, i)
		}
	}
	return q
}

// cloneStatement copies a query's statement for one run, so that running it
// leaves the query as it was.
func cloneStatement(st *Statement) Statement {
	c := *st
	c.Where = slices.Clip(c.Where)
	c.OrderBy = slices.Clip(c.OrderBy)
	c.Columns = slices.Clip(c.Columns)
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

// All runs the query and returns every row.
func (q *Query[E]) All[_ Lazy[E]](ctx context.Context) ([]E, error) {
	if q.meta.err != nil {
		return nil, q.meta.err
	}
	st := cloneStatement(&q.st)
	r, err := q.db.run(ctx, &st)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	dst := make([]any, columnCount(&st))
	out := make([]E, 0, min(st.Limit, 1024))
	for r.Next() {
		out = append(out, *new(E))
		if err := scanRow(r, q.meta, st.Columns, dst, &out[len(out)-1]); err != nil {
			return nil, dbErr(st.Entity, err)
		}
	}
	if err := r.Err(); err != nil {
		return nil, dbErr(st.Entity, err)
	}
	return out, nil
}

// One runs the query for exactly one row. Its error wraps ErrNotFound when no
// row matches and ErrNotSingular when more than one does.
func (q *Query[E]) One[_ Lazy[E]](ctx context.Context) (*E, error) {
	c := *q
	c.st.Limit = 2
	out, err := c.All(ctx)
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

// Iter runs the query and yields one row at a time; only the current row is
// held in memory.
func (q *Query[E]) Iter[_ Lazy[E]](ctx context.Context) iter.Seq2[E, error] {
	return func(yield func(E, error) bool) {
		var zero E
		if q.meta.err != nil {
			yield(zero, q.meta.err)
			return
		}
		st := cloneStatement(&q.st)
		r, err := q.db.run(ctx, &st)
		if err != nil {
			yield(zero, err)
			return
		}
		defer r.Close()
		dst := make([]any, columnCount(&st))
		for r.Next() {
			var e E
			if err := scanRow(r, q.meta, st.Columns, dst, &e); err != nil {
				yield(zero, dbErr(st.Entity, err))
				return
			}
			if !yield(e, nil) {
				return
			}
		}
		if err := r.Err(); err != nil {
			yield(zero, dbErr(st.Entity, err))
		}
	}
}

// Count returns the number of rows matching the query's conditions; its
// ordering, limit, offset and columns are ignored.
func (q *Query[E]) Count[_ Lazy[E]](ctx context.Context) (int64, error) {
	if q.meta.err != nil {
		return 0, q.meta.err
	}
	st := cloneStatement(&q.st)
	st.Count, st.Columns, st.OrderBy, st.Limit, st.Offset = true, nil, nil, 0, 0
	r, err := q.db.run(ctx, &st)
	if err != nil {
		return 0, err
	}
	defer r.Close()
	var n int64
	if r.Next() {
		if err := r.Scan(&n); err != nil {
			return 0, dbErr(st.Entity, err)
		}
	}
	if err := r.Err(); err != nil {
		return 0, dbErr(st.Entity, err)
	}
	return n, nil
}

// SQL returns the statement and arguments the query would send, without a
// database. Rewrite hooks have not run on it, so a condition a hook adds (a
// tenant's, for example) is not shown. It returns "" when the entity's
// metadata is invalid; All returns that error.
func (q *Query[E]) SQL[_ Lazy[E]]() (string, []any) {
	if q.meta.err != nil {
		return "", nil
	}
	st := cloneStatement(&q.st)
	return render(q.db.d, &st)
}
