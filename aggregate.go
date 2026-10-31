package velox

import (
	"context"

	"github.com/syssam/velox/internal/engine"
)

// aggRun is the state of one aggregate read: the value scanned, nil for NULL.
type aggRun[T any] struct {
	st Statement
	v  *T
}

func readAggregate[T any](a *aggRun[T], r engine.Rows) error {
	a.v = nil
	if r.Next() {
		return r.Scan(&a.v)
	}
	return nil
}

// aggregate runs fn over column c of q's rows and scans it into a *T; nil
// means no row, or only NULLs.
func aggregate[E, T any](ctx context.Context, q Query[E], fn Aggregate, c int32) (*T, error) {
	if q.meta.err != nil {
		return nil, q.meta.err
	}
	if q.err != nil {
		return nil, q.err
	}
	st, err := q.prepare()
	if err != nil {
		return nil, err
	}
	st.Aggregate, st.Columns, st.OrderBy, st.Limit, st.Offset = fn, []int{int(c)}, nil, 0, 0
	var v *T
	if q.db.e.Hooked() {
		a := &aggRun[T]{st: st}
		err = q.db.e.Run(ctx, &a.st, func(r engine.Rows) error { return readAggregate(a, r) }, nil)
		v = a.v
	} else {
		a := aggRun[T]{st: st}
		err = q.db.e.Plain(ctx, &a.st, func(r engine.Rows) error { return readAggregate(&a, r) }, nil)
		v = a.v
	}
	if err != nil {
		return nil, err
	}
	return v, nil
}

// Sum returns the sum of column c over the rows matching q's conditions,
// After's among them, as Count counts them: the tenant scope and the policy
// apply, and the ordering, limit, offset, columns and relations are
// ignored. With no row, or only NULLs, it is 0.
//
// A sum that does not fit V is an error, not a wrapped value.
func Sum[E, V any](ctx context.Context, q Query[E], c NumberCol[E, V]) (V, error) {
	v, err := aggregate[E, V](ctx, q, AggregateSum, c.I)
	if err != nil || v == nil {
		var zero V
		return zero, err
	}
	return *v, nil
}

// Avg returns the average of column c over the rows Sum would add up, and
// false when there is none. The database computes it: an integer column's
// average is a decimal that MySQL rounds to four places. A float64 holds
// integers exactly only up to 2^53, so an average above that loses precision.
func Avg[E, V any](ctx context.Context, q Query[E], c NumberCol[E, V]) (float64, bool, error) {
	v, err := aggregate[E, float64](ctx, q, AggregateAvg, c.I)
	if err != nil || v == nil {
		return 0, false, err
	}
	return *v, true, nil
}

// Min returns the least value of column c over the rows Sum would add up,
// and false when there is none.
func Min[E, V any](ctx context.Context, q Query[E], c NumberCol[E, V]) (V, bool, error) {
	return extreme[E, V](ctx, q, AggregateMin, c)
}

// Max returns the greatest value of column c, as Min does.
func Max[E, V any](ctx context.Context, q Query[E], c NumberCol[E, V]) (V, bool, error) {
	return extreme[E, V](ctx, q, AggregateMax, c)
}

func extreme[E, V any](ctx context.Context, q Query[E], fn Aggregate, c NumberCol[E, V]) (V, bool, error) {
	v, err := aggregate[E, V](ctx, q, fn, c.I)
	if err != nil || v == nil {
		var zero V
		return zero, false, err
	}
	return *v, true, nil
}

// Group is one group of a grouped aggregate: the rows whose key column holds
// Key, and the aggregate over them.
type Group[K, V any] struct {
	Key   K
	Value V
}

// GroupKey is the key column of a grouped aggregate, made by a handle's Key
// method. K is the column's value type, *V for a nullable column. A JSON
// column has no Key: its values compare differently by dialect.
type GroupKey[E, K any] struct{ i int32 }

// groupRun is the state of one grouped aggregate. sum says that a NULL is V's
// zero, as for Sum; otherwise a group with no value has no entry, as Avg, Min
// and Max report none.
type groupRun[K, V any] struct {
	st  Statement
	out []Group[K, V]
	sum bool
}

// readGroups reads every group into g.out, anew should an around hook retry.
func readGroups[K, V any](g *groupRun[K, V], r engine.Rows) error {
	g.out = nil
	for r.Next() {
		var x Group[K, V]
		var v *V
		if err := r.Scan(&x.Key, &v); err != nil {
			return err
		}
		switch {
		case v != nil:
			x.Value = *v
		case !g.sum:
			continue
		}
		g.out = append(g.out, x)
	}
	return nil
}

// grouped runs fn, or COUNT(*) with fn 0, over column c of q's rows per value
// of key, in the order of the key.
func grouped[E, K, V any](ctx context.Context, q Query[E], fn Aggregate, key GroupKey[E, K], c int32) ([]Group[K, V], error) {
	if q.meta.err != nil {
		return nil, q.meta.err
	}
	if q.err != nil {
		return nil, q.err
	}
	st, err := q.prepare()
	if err != nil {
		return nil, err
	}
	st.Columns, st.Limit, st.Offset = nil, 0, 0
	st.GroupBy, st.OrderBy = int(key.i)+1, []OrderTerm{{Column: int(key.i)}}
	if fn == 0 {
		st.Count = true
	} else {
		st.Aggregate, st.Columns = fn, []int{int(c)}
	}
	g := &groupRun[K, V]{st: st, sum: fn == AggregateSum}
	read := func(r engine.Rows) error { return readGroups(g, r) }
	if q.db.e.Hooked() {
		err = q.db.e.Run(ctx, &g.st, read, nil)
	} else {
		err = q.db.e.Plain(ctx, &g.st, read, nil)
	}
	if err != nil {
		return nil, err
	}
	return g.out, nil
}

// CountBy returns the number of rows matching q's conditions per value of the
// key column, one Group per value, ordered by the key: SELECT key, COUNT(*)
// ... GROUP BY key ORDER BY key. It reads q as Count does: the tenant scope,
// the policy and After's condition apply, and the ordering, limit, offset,
// columns and relations are ignored.
//
// The groups can be as many as the rows, so q must say Unbounded, and a Limit
// does not stand in for it, or CountBy fails with ErrUnbounded. They count
// against WithMaxRows and q's MaxRows: more fails with ErrTooManyRows.
//
// A nullable column's Key makes K a pointer, and NULL one group with a nil
// Key: first on SQLite and MySQL, last on PostgreSQL. Text keys order by the
// database's collation.
func CountBy[E, K any](ctx context.Context, q Query[E], key GroupKey[E, K]) ([]Group[K, int64], error) {
	return grouped[E, K, int64](ctx, q, 0, key, 0)
}

// SumBy returns the sum of column c per value of the key column, as CountBy
// groups. A group with only NULLs sums to 0, as for Sum. A sum that does not
// fit V is an error, not a wrapped value.
func SumBy[E, K, V any](ctx context.Context, q Query[E], key GroupKey[E, K], c NumberCol[E, V]) ([]Group[K, V], error) {
	return grouped[E, K, V](ctx, q, AggregateSum, key, c.I)
}

// AvgBy returns the average of column c per value of the key column, as
// CountBy groups and as Avg computes it. A group with only NULLs has no
// average, and no entry.
func AvgBy[E, K, V any](ctx context.Context, q Query[E], key GroupKey[E, K], c NumberCol[E, V]) ([]Group[K, float64], error) {
	return grouped[E, K, float64](ctx, q, AggregateAvg, key, c.I)
}

// MinBy returns the least value of column c per value of the key column, as
// CountBy groups. A group with only NULLs has no least value, and no entry.
func MinBy[E, K, V any](ctx context.Context, q Query[E], key GroupKey[E, K], c NumberCol[E, V]) ([]Group[K, V], error) {
	return grouped[E, K, V](ctx, q, AggregateMin, key, c.I)
}

// MaxBy returns the greatest value of column c per value of the key column,
// as MinBy does.
func MaxBy[E, K, V any](ctx context.Context, q Query[E], key GroupKey[E, K], c NumberCol[E, V]) ([]Group[K, V], error) {
	return grouped[E, K, V](ctx, q, AggregateMax, key, c.I)
}
