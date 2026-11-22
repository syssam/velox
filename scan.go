package velox

import (
	"context"
	"fmt"
	"reflect"

	"github.com/syssam/velox/internal/engine"
)

// Scan runs q, a projection (Select), and reads each row into a T, as ent's
// Select(...).Scan(ctx, &v) does. T is a struct, one exported field per
// result column, or, for a projection of one term, the value itself:
//
//	type orgTotal struct {
//		OrgID int64
//		Sum   float64 `velox:"total"`
//	}
//	rows, err := velox.Scan[orgTotal](ctx, db.Query[veloxgen.Invoice]().
//		Select(I.OrgID, I.Total.Sum().As("total")).
//		GroupBy(I.OrgID).Limit(100))
//
// A result column's name is the column's for a column handle, or the one As
// gives a Term; an aggregate, Calc and a JSON path's Term have none until As
// names them. A field takes the column its tag velox:"name" names, else the
// one of its own name, with case and underscores ignored, or of the column's
// Go field: OrgID takes org_id. A field tagged velox:"-" takes none.
//
// Each column is read as its term says: a JSON column is decoded into its
// field as an entity's is, NULL into a pointer field is nil (into any other
// a failure), and a time, an enum and a number are converted as the
// driver's Scan converts them. A result column with no field, two fields
// that would take one column, one field for two, and a field whose type
// cannot hold its column's (a string into an int) fail before anything is
// sent, naming the column and the field.
//
// It runs as Rows does: Where, the Read policy, the scope, the interceptors,
// Limit and the row cap apply, and q needs a Limit or AllowUnbounded. The
// result is nil when no row matches. It is a function, not a method of
// Query, as Values is: T is not E.
func Scan[T, E any](ctx context.Context, q Query[E]) ([]T, error) {
	var out []T
	err := scanRows(ctx, q, reflect.TypeFor[T](), func() any {
		var zero T
		out = append(out, zero)
		return &out[len(out)-1]
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ScanOne is Scan of the one row q reads, as One is All of one: its error
// wraps ErrNotFound when no row matches and ErrNotSingular when more than
// one does. It needs no Limit, which it sets. A projection of aggregates
// without GroupBy reads one row:
//
//	sums, err := velox.ScanOne[struct{ N, Total int64 }](ctx, db.Query[veloxgen.Invoice]().
//		Select(velox.CountAll[veloxgen.Invoice]().As("n"), I.Total.Sum().As("total")))
func ScanOne[T, E any](ctx context.Context, q Query[E]) (T, error) {
	var zero T
	q.s.St.Limit = 2
	out, err := Scan[T](ctx, q)
	switch {
	case err != nil:
		return zero, err
	case len(out) == 0:
		return zero, fmt.Errorf("velox: %s: %w", q.meta.Info.Name, ErrNotFound)
	case len(out) > 1:
		return zero, fmt.Errorf("velox: %s: %w", q.meta.Info.Name, ErrNotSingular)
	}
	return out[0], nil
}

// scanRows runs q and reads each row by a plan for typ into the value next
// returns a pointer to. The plan is made before anything is sent.
func scanRows[E any](ctx context.Context, q Query[E], typ reflect.Type, next func() any) error {
	switch {
	case q.meta.Err != nil:
		return q.meta.Err
	case q.s.Err != nil:
		return q.s.Err
	}
	plan, err := engine.NewScanPlan(q.meta.Info, q.s.St.Project, typ)
	if err != nil {
		return err
	}
	return engine.ScanRows(q.Rows(ctx), plan, next)
}
