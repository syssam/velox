package velox

import (
	"context"

	"github.com/syssam/velox/internal/engine"
)

// valuesRun is the state of one read of a single column.
type valuesRun[V any] struct {
	out  []V
	hint int // the capacity the first read starts with: engine.FirstCap, as All's
}

// readValues reads every row's one column into a.out, anew should an around
// hook retry. Each row is scanned into its own element of a.out through one
// reused argument slice: a local, or a fresh variadic slice, passed through
// Scan's interface would cost a heap allocation per row.
func readValues[V any](a *valuesRun[V], r engine.Rows) error {
	if a.out == nil {
		a.out = make([]V, 0, a.hint)
	}
	a.out = a.out[:0]
	var zero V
	dst := make([]any, 1) // one Scan argument slice for every row
	for r.Next() {
		a.out = append(a.out, zero)
		dst[0] = &a.out[len(a.out)-1]
		if err := r.Scan(dst...); err != nil {
			return err
		}
	}
	if len(a.out) == 0 {
		a.out = nil // no row: nil, as documented
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
	a := &valuesRun[V]{hint: engine.FirstCap(&st)}
	if err := q.s.DB.Run(ctx, &st, func(r engine.Rows) error { return readValues(a, r) }, nil); err != nil {
		return nil, err
	}
	return a.out, nil
}

// Values returns the values of column h of the rows q reads, in q's
// order, as All would read the rows: q's Where, After, OrderBy, Limit and
// Offset, the Read policy, the scope, the interceptors and WithFilter's
// filters apply, q needs a Limit or AllowUnbounded, and the row cap counts
// the rows. h is one of E's column handles, and V is its value type, a
// pointer for a nullable column (nil for NULL):
//
//	numbers, err := db.Query[veloxgen.Invoice]().Where(I.Paid.EQ(true)).Limit(100).Values(ctx, I.Number)
//
// It reads nothing else: q's Columns, Omit and With are ignored. A q that
// joins, or is a projection (Select), fails with an error before anything
// is sent. A JSON column's stored bytes are not its value, so its handle
// does not compile here, nor does another entity's handle. The result is
// nil when no row matches.
//
// It reads the column as Count reads, by setting the statement's one
// column, not through Select and Rows: a projection refuses With and After,
// which Values keeps, and would scan through RawRow by position.
func (q Query[E]) Values[H valueColumn[E, V], V any](ctx context.Context, h H) ([]V, error) {
	return columnValues[E, V](ctx, q, int(valueShape[V](h).I), "Values")
}

// valueColumn is satisfied by each column handle of E that Values reads
// and that keys a grouped aggregate: every kind but JSONCol and
// NullJSONCol. Its struct names V, the type of the column's values, in a
// field that is always nil, from which a call infers V.
type valueColumn[E, V any] interface {
	Column[E]
	~valueShape[V]
}

// valueShape is the struct of a handle whose values are V.
type valueShape[V any] = struct {
	_ *V
	I int32
}

// IDs returns the primary keys of the rows q reads, as Values reads a
// column: the same policies, filters, Limit rule and row cap apply. K is
// the key's Go type, which E's type does not say, so the caller names it:
//
//	ids, err := db.Query[veloxgen.User]().Where(U.Active.EQ(true)).AllowUnbounded().IDs[int64](ctx)
//
// The result is nil when no row matches. A K other than the key field's
// type, and an entity whose primary key has more than one column, fail
// with an error before anything is sent.
func (q Query[E]) IDs[K any](ctx context.Context) ([]K, error) {
	if q.meta.Err != nil {
		return nil, q.meta.Err
	}
	col, err := engine.KeyColumn(q.meta.Info)
	if err != nil {
		return nil, err
	}
	var e E
	if p := q.meta.Addr(&e, col); !isPtrTo[K](p) {
		return nil, engine.KeyTypeError(q.meta.Info.Name, (*K)(nil), p)
	}
	return columnValues[E, K](ctx, q, col, "IDs")
}

// isPtrTo reports whether p is a *K.
func isPtrTo[K any](p any) bool {
	_, ok := p.(*K)
	return ok
}
