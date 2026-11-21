package velox

import (
	"context"
	"fmt"

	"github.com/syssam/velox/internal/engine"
)

// valuesRun is the state of one read of a single column.
type valuesRun[V any] struct{ out []V }

// readValues reads every row's one column into a.out, anew should an around
// hook retry.
func readValues[V any](a *valuesRun[V], r engine.Rows) error {
	a.out = nil
	for r.Next() {
		var v V
		if err := r.Scan(&v); err != nil {
			return err
		}
		a.out = append(a.out, v)
	}
	return nil
}

// columnValues reads column col of q's rows; what names the caller.
func columnValues[E, V any](ctx context.Context, q Query[E], col int, what string) ([]V, error) {
	if q.meta.Err != nil {
		return nil, q.meta.Err
	}
	if q.s.Err != nil {
		return nil, q.s.Err
	}
	q.s.St.Columns = nil // as in Count: After's order columns need not be read
	st, err := q.prepare()
	if err != nil {
		return nil, err
	}
	if err := engine.ColumnRead(&st, col, what); err != nil {
		return nil, err
	}
	a := &valuesRun[V]{}
	if err := q.s.DB.Run(ctx, &st, func(r engine.Rows) error { return readValues(a, r) }, nil); err != nil {
		return nil, err
	}
	return a.out, nil
}

// Values returns the values of one column of the rows q reads, in q's
// order, as All would read the rows: q's Where, After, OrderBy, Limit and
// Offset, the Read policy, the scope, the interceptors and WithFilter's
// filters apply, q needs a Limit or AllowUnbounded, and the row cap counts
// the rows. The column is a handle's Key, so V is its value type, a pointer
// for a nullable column (nil for NULL):
//
//	numbers, err := velox.Values(ctx, db.Query[veloxgen.Invoice]().Where(I.Paid.EQ(true)).Limit(100), I.Number.Key())
//
// It reads nothing else: q's Columns, Omit and With are ignored. A q that
// joins, or is a projection (Select), fails with an error before anything
// is sent; so does an encoded column, which a JSON handle is and which has
// no Key. The result is nil when no row matches. It is a function, not a
// method of Query, as Sum is (ADR 0022).
//
// It reads the column as Count reads, by setting the statement's one
// column, not through Select and Rows: a projection refuses With and After,
// which Values keeps, and would scan through RawRow by position.
func Values[E, V any](ctx context.Context, q Query[E], key GroupKey[E, V]) ([]V, error) {
	return columnValues[E, V](ctx, q, int(key.i), "Values")
}

// IDs returns the primary keys of the rows q reads, as Values reads a
// column: the same policies, filters, Limit rule and row cap apply. K is
// the key's Go type, which E's type does not say, so the caller names it:
//
//	ids, err := velox.IDs[int64](ctx, db.Query[veloxgen.User]().Where(U.Active.EQ(true)).AllowUnbounded())
//
// The result is nil when no row matches. A K other than the key field's
// type, and an entity whose primary key has more than one column, fail
// with an error before anything is sent.
func IDs[K, E any](ctx context.Context, q Query[E]) ([]K, error) {
	if q.meta.Err != nil {
		return nil, q.meta.Err
	}
	col, err := engine.KeyColumn(q.meta.Info)
	if err != nil {
		return nil, err
	}
	var e E
	if p := q.meta.Addr(&e, col); !isPtrTo[K](p) {
		return nil, fmt.Errorf("velox: %s: IDs[%T] of a key whose field is %T", q.meta.Info.Name, *new(K), p)
	}
	return columnValues[E, K](ctx, q, col, "IDs")
}

// isPtrTo reports whether p is a *K.
func isPtrTo[K any](p any) bool {
	_, ok := p.(*K)
	return ok
}
