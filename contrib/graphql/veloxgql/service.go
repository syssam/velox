package veloxgql

import (
	"context"
	"fmt"
	"reflect"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
)

// Service returns ctx with db, DefaultLimits() and a selection source that
// selects nothing, for generated code a service calls outside a GraphQL
// request: a list method, a call from another service, a test.
//
//	ctx = veloxgql.Service(ctx, db)
//	conn, err := db.Query[veloxgen.Post]().Paginate(ctx, after, first, before, last,
//		veloxgen.WithPostOrder(orderBy), veloxgen.WithPostFilter(where))
//
// Under it, a connection, an edge's connection, gqlgraph.EdgeOne and
// gqlgraph.EdgeList read every column of their rows, and an edge reads
// alone for each row: there is no field to batch for. DB and DBFromContext
// return db. A
// caller without it still fails with ErrNoSelectionSource.
//
// It replaces the state an adapter put in ctx, so a resolver does not call
// it: under the adapter's context, the same code reads only what the field
// selects, and batches. It panics on a nil db.
func Service(ctx context.Context, db *velox.DB) context.Context {
	return ServiceWithLimits(ctx, db, Limits{})
}

// ServiceWithLimits is Service with the limits l, each zero field of l
// taken from DefaultLimits(): a service that pages by more than MaxPage
// rows, or reads a list edge of more than MaxList, raises them here. It
// panics on a nil db and on a negative limit.
func ServiceWithLimits(ctx context.Context, db *velox.DB, l Limits) context.Context {
	if db == nil {
		panic("veloxgql.Service: nil DB")
	}
	l = serviceLimits(l)
	return gqlcore.Install(ctx, db, &l, serviceSource{})
}

// serviceLimits is l with each zero field taken from DefaultLimits(). It
// panics on a negative field, naming it.
func serviceLimits(l Limits) Limits {
	v := reflect.ValueOf(&l).Elem()
	d := reflect.ValueOf(DefaultLimits())
	for i := range v.NumField() {
		f := v.Field(i)
		if f.Int() < 0 {
			panic(fmt.Sprintf("veloxgql.ServiceWithLimits: Limits.%s is %v, and a limit must not be negative (zero is the default)", v.Type().Field(i).Name, f.Interface()))
		}
		if f.IsZero() {
			f.Set(d.Field(i))
		}
	}
	return l
}

// serviceSource is Service's selection source: no field is resolving, so
// generated code reads every column, and alone.
type serviceSource struct{}

var _ gqlcore.SelectionSource = serviceSource{}

// Field is never a field: a service's call resolves none.
func (serviceSource) Field(context.Context) (gqlcore.Field, bool) {
	return gqlcore.Field{}, false
}

// Wait does nothing: nothing batches, so nothing waits on another's read.
func (serviceSource) Wait(context.Context) func() { return func() {} }
