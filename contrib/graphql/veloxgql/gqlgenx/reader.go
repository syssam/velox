package gqlgenx

import (
	"context"
	"runtime"
	"sync"
	"weak"

	"github.com/99designs/gqlgen/graphql"

	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
)

// The package registers gqlgen's selection source with the core when it is
// linked (decision 0173), so that generated code served by gqlgen with no
// Extension finds the field it resolves and batches its edges. The code
// velox-graphql writes for gqlgen imports the package for this, blank.
func init() { gqlcore.Register(reader{}) }

// reader is gqlgen's registered selection source. The field of a context is
// gqlgen's field context, as the Extension's source reads it, but with no
// schema: a type's interfaces are those the validator's definition of the
// type lists (collect). Its table and its interned field occurrences are
// those of the field's root, the field context at the top of its Parent
// chain: a field of an operation's root type for a query or a mutation, so
// one per root field of each response, and a field of the object of one
// event for a subscription, whose events share no field context. A group is
// always the rows of one root (a page, a batch, Batch's rows), so batching
// per root batches as per operation.
//
// The roots' states are in roots, keyed by a weak pointer to the root's
// field context, and each leaves it when the garbage collector finds its
// root unreachable (runtime.AddCleanup), with the response. A state holds
// rows, gqlgen's operation context and the selections, never a field
// context, so it never keeps its own root alive.
type reader struct{}

var _ gqlcore.Reader = reader{}

// In reports whether ctx is in a gqlgen field.
func (reader) In(ctx context.Context) bool { return graphql.GetFieldContext(ctx) != nil }

// rootState is the state of one root field: its table and the source that
// interns its occurrences.
type rootState struct {
	table *gqlcore.Table
	src   source // no schema
}

// roots maps a weak.Pointer[graphql.FieldContext] to its root's *rootState.
var roots sync.Map

// Field is the field of gqlgen's field context in ctx, with its root's
// table; ok is false outside a gqlgen field.
func (reader) Field(ctx context.Context) (gqlcore.Field, bool) {
	fc := graphql.GetFieldContext(ctx)
	if fc == nil {
		return gqlcore.Field{}, false
	}
	st := rootOf(fc)
	f := st.src.field(ctx, fc)
	f.Table = st.table
	return f, true
}

// Wait does nothing: gqlgen runs every field in a goroutine of its own.
func (reader) Wait(context.Context) func() { return noWait }

// rootOf is the state of fc's root, made on its first call.
func rootOf(fc *graphql.FieldContext) *rootState {
	root := fc
	for root.Parent != nil {
		root = root.Parent
	}
	k := weak.Make(root)
	if v, ok := roots.Load(k); ok {
		return v.(*rootState)
	}
	st := &rootState{table: gqlcore.NewTable()}
	if v, loaded := roots.LoadOrStore(k, st); loaded {
		return v.(*rootState)
	}
	runtime.AddCleanup(root, dropRoot, k)
	return st
}

// dropRoot removes the state of a root the collector found unreachable.
func dropRoot(k weak.Pointer[graphql.FieldContext]) { roots.Delete(k) }
