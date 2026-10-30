package velox

import (
	"context"

	"github.com/syssam/velox2/internal/engine"
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
