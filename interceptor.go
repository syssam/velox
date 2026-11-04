package velox

import (
	"context"

	"github.com/syssam/velox/internal/engine"
)

// Interceptor filters entity E's rows for the application. Filter's
// condition is ANDed into every statement that reads or changes E's rows:
// SELECT, Count, the aggregates, Each and Iter, a With load of E, an EXISTS
// on E (Has, HasWhere), a join of E, UPDATE, DELETE, Save and an upsert's
// update branch. Raw SQL is not filtered.
//
// The interceptors' conditions come before the Read policy's in the WHERE.
// WithoutPolicy does not lift them: an interceptor offers its own way to be
// skipped, such as a context value its Filter reads. A Filter that reads a
// context value records it with WithScopeValue, so that the GraphQL batcher
// keeps callers apart.
//
// Filter returns Allow[E]() for no restriction. Its zero Pred fails the
// statement, as a policy's does. An error fails the statement and is not a
// denial: errors.Is(err, ErrDenied) holds only when the error is a Deny.
//
// A generated entity's interceptors are declared in the schema package, on
// the entity's schema, and bound by velox-gen. An entity written by hand
// declares them with a method on its pointer type,
//
//	func (*E) Interceptors() []velox.Interceptor[E]
//
// which its Descriptor binds with BindInterceptors; a method the metadata
// does not bind is refused at the entity's first use. velox calls the method once, when it builds the metadata, under
// the rules of Descriptor.Policy. An Interceptor with a nil Filter is refused
// then, by its index.
//
// A join entity of a many-to-many edge cannot have interceptors, as it
// cannot have a Read policy: it is read through its two sides. On MySQL, an
// upsert that updates an entity with interceptors is refused with
// ErrPolicyUpsert.
type Interceptor[E any] struct {
	Filter func(ctx context.Context) (Pred[E], error)
}

// BindInterceptors erases the entity type of the interceptor functions fs,
// for Descriptor.Interceptors, as BindPolicy erases policies. For generated
// code. An Interceptor with a nil Filter is refused, by its index, when the
// metadata is built.
//
//go:noinline
func BindInterceptors[E any](fs ...func() []Interceptor[E]) []func() BoundInterceptors {
	out := make([]func() BoundInterceptors, len(fs))
	for i, f := range fs {
		if f != nil {
			out[i] = func() BoundInterceptors { return bindInterceptors(f()) }
		}
	}
	return out
}

// bindInterceptors erases is's entity type.
func bindInterceptors[E any](is []Interceptor[E]) BoundInterceptors {
	var b BoundInterceptors
	for i, x := range is {
		f := x.Filter
		if f == nil {
			engine.SetFilterNil(&b, i)
			return b
		}
		engine.AddBoundFilter(&b, func(ctx context.Context) (Predicate, error) { p, err := f(ctx); return Predicate(p), err })
	}
	return b
}
