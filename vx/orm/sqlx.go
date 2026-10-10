package orm

import (
	"context"
	stdsql "database/sql"
	"fmt"

	"github.com/syssam/velox/dialect/sql"
)

// This file is the way out of the typed API for what it cannot say: a
// condition written against the SQL builder, grouped and aggregated reads,
// and plain statements.

// Raw returns a predicate that applies f to the query's selector. Convert it
// to the entity's predicate type to use it: predicate.User(orm.Raw(f)).
func Raw(f func(*sql.Selector)) P { return P{Op: opRaw, raw: f} }

// RawOrder returns an order term written against the SQL builder, for what a
// column's Asc and Desc do not say: NULLS LAST, an expression, a column of a
// joined table. Convert it to the entity's order type to use it:
//
//	q.OrderBy(predicate.UserOrderTerm(orm.RawOrder(func(s *sql.Selector) {
//		s.OrderBy(sql.Desc(s.C(user.ColumnNickname)) + " NULLS LAST")
//	})))
//
// A query ordered this way cannot be paged with Paginate: a cursor is made of
// the columns a page is ordered by, and a function does not name them.
func RawOrder(f func(*sql.Selector)) O { return O{raw: f} }

// Compile returns the predicate as a function on a selector of its entity's
// table, for code that builds SQL itself or registers the same condition as a
// privacy filter or interceptor: q.WhereP(orm.Compile(user.Age.GT(18))).
//
// The zero predicate, which is no condition, compiles to no function: nil.
// That is what a predicate that was a function was when it was none, so code
// that asks a function whether it is nil is told the same as before; a query
// takes nil for no condition, and a mutation refuses it (see Mutation.WhereP).
func Compile[T Predicate](p T) func(*sql.Selector) {
	c := P(p)
	if c.Op == 0 {
		return nil
	}
	return compile(&c)
}

// ExecSQL runs a statement that returns no rows on the executor: on the
// transaction when ex is one, which is what a lock statement such as
// pg_advisory_xact_lock needs.
func ExecSQL(ctx context.Context, ex Executor, query string, args ...any) (stdsql.Result, error) {
	var res stdsql.Result
	if ex == nil || ex.conn() == nil {
		return nil, ErrNoExecutor
	}
	if err := ex.conn().drv.Exec(ctx, query, args, &res); err != nil {
		return nil, err
	}
	return res, nil
}

// QuerySQL runs a statement that returns rows on the executor. The caller
// closes the rows.
func QuerySQL(ctx context.Context, ex Executor, query string, args ...any) (*stdsql.Rows, error) {
	rows := &sql.Rows{}
	if ex == nil || ex.conn() == nil {
		return nil, ErrNoExecutor
	}
	if err := ex.conn().drv.Query(ctx, query, args, rows); err != nil {
		return nil, err
	}
	r, ok := rows.ColumnScanner.(*stdsql.Rows)
	if !ok {
		_ = rows.Close()
		return nil, fmt.Errorf("orm: the driver returned %T, not database/sql rows", rows.ColumnScanner)
	}
	return r, nil
}

// Lock is the row lock a query takes on what it reads. It needs a
// transaction to mean anything, and SQLite ignores it.
type Lock uint8

// Row locks.
const (
	LockNone Lock = iota
	LockUpdate
	LockShare
)

// LockOption changes what a locking read does when a row is already locked.
type LockOption uint8

// Lock options.
const (
	// LockWait waits for the other transaction, the default.
	LockWait LockOption = iota
	// SkipLocked leaves locked rows out of the result.
	SkipLocked
	// NoWait fails at once instead of waiting.
	NoWait
)

func (q *QueryCore) lock(s *sql.Selector) {
	if q.Lock == LockNone {
		return
	}
	var opts []sql.LockOption
	switch q.LockOpt {
	case SkipLocked:
		opts = append(opts, sql.WithLockAction(sql.SkipLocked))
	case NoWait:
		opts = append(opts, sql.WithLockAction(sql.NoWait))
	}
	if q.Lock == LockShare {
		s.ForShare(opts...)
	} else {
		s.ForUpdate(opts...)
	}
}

// SetLock makes the query lock the rows it reads. The generated ForUpdate
// and ForShare methods call it.
func (q *QueryCore) SetLock(l Lock, opts []LockOption) {
	q.Lock = l
	q.LockOpt = LockWait
	for _, o := range opts {
		q.LockOpt = o
	}
}

// Agg is an aggregate of a grouped read: it adds itself to the selector and
// returns the expression to select.
type Agg func(*sql.Selector) string

// The aggregates below take a column by name; the generated constants
// (order.FieldAmount) are names that cannot be misspelled.

// Count counts rows.
func Count() Agg { return func(*sql.Selector) string { return sql.Count("*") } }

// Sum adds up a column.
func Sum(column string) Agg { return func(s *sql.Selector) string { return sql.Sum(s.C(column)) } }

// Min is the smallest value of a column.
func Min(column string) Agg { return func(s *sql.Selector) string { return sql.Min(s.C(column)) } }

// Max is the largest value of a column.
func Max(column string) Agg { return func(s *sql.Selector) string { return sql.Max(s.C(column)) } }

// Mean is the average of a column.
func Mean(column string) Agg { return func(s *sql.Selector) string { return sql.Avg(s.C(column)) } }

// As names the aggregate in the result, so that it can be scanned into a
// struct field tagged with that name.
func As(a Agg, name string) Agg {
	return func(s *sql.Selector) string { return sql.As(a(s), name) }
}

// Selection reads columns and aggregates of a query instead of entities. The
// generated Select method of a query returns one.
type Selection struct {
	q     *QueryCore
	cols  []string
	group []string
	aggs  []Agg
}

// Select returns a read of the given columns of the rows the query matches.
// The entity's policy and the interceptors apply to it as to any read.
func (q *QueryCore) Select(columns ...string) *Selection {
	return &Selection{q: q, cols: columns}
}

// GroupBy groups the rows by the given columns, which are selected too.
func (s *Selection) GroupBy(columns ...string) *Selection {
	s.group = append(s.group, columns...)
	return s
}

// Aggregate adds aggregates to the selected columns.
func (s *Selection) Aggregate(aggs ...Agg) *Selection {
	s.aggs = append(s.aggs, aggs...)
	return s
}

// Scan runs the read and stores the rows in dest: a pointer to a slice of
// structs (fields matched by their sql or json tag), or of a basic type when
// one column is selected.
//
//go:noinline
func (s *Selection) Scan(ctx context.Context, dest any) error {
	return s.run(ctx, func(rows *sql.Rows) error { return sql.ScanSlice(rows, dest) })
}

// Int runs the read and returns its single integer, e.g. a count or a
// maximum: 0 when no row matches or the value is NULL.
func (s *Selection) Int(ctx context.Context) (int, error) {
	var v stdsql.NullInt64
	err := s.run(ctx, func(rows *sql.Rows) error {
		if !rows.Next() {
			return rows.Err()
		}
		return rows.Scan(&v)
	})
	return int(v.Int64), err
}

// statement renders the read of q, the query as the policy and the
// interceptors left it.
func (s *Selection) statement(ctx context.Context, c *conn, q *QueryCore) (string, []any, error) {
	cols := append(append([]string(nil), s.group...), s.cols...)
	for _, col := range cols {
		if q.M.col(col) < 0 {
			return "", nil, fmt.Errorf("orm: %s has no column %q to select", q.M.Name, col)
		}
	}
	sel := q.selector(ctx, c, cols)
	if len(cols) == 0 && len(s.aggs) > 0 {
		sel.Select()
	}
	for _, a := range s.aggs {
		sel.AppendSelect(a(sel))
	}
	if len(s.group) > 0 {
		sel.GroupBy(sel.Columns(s.group...)...)
	}
	query, args := sel.Query()
	return query, args, sel.Err()
}

// SQL returns the statement the read would run and its arguments, without
// running it: what the query is after the entity's policy and the handle's
// interceptors, in the dialect of the handle it was built from. Of a query's
// own rows, Select().SQL(ctx) is the statement All would run, before its
// eager loads.
func (s *Selection) SQL(ctx context.Context) (query string, args []any, err error) {
	c, cerr := s.q.conn()
	if cerr != nil {
		return "", nil, cerr
	}
	_, err = s.q.guarded(ctx, c, func(ctx context.Context, q *QueryCore) (any, error) {
		query, args, err = s.statement(ctx, c, q)
		return nil, err
	})
	return query, args, err
}

// run executes the read under the entity's policy and the interceptors and
// hands the rows to scan.
func (s *Selection) run(ctx context.Context, scan func(*sql.Rows) error) error {
	c, cerr := s.q.conn()
	if cerr != nil {
		return cerr
	}
	_, err := s.q.guarded(ctx, c, func(ctx context.Context, q *QueryCore) (any, error) {
		query, args, err := s.statement(ctx, c, q)
		if err != nil {
			return nil, err
		}
		rows := &sql.Rows{}
		if err := c.drv.Query(ctx, query, args, rows); err != nil {
			return nil, err
		}
		defer func() { _ = rows.Close() }()
		return nil, scan(rows)
	})
	return err
}
