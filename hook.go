package velox

import (
	"context"
	"slices"
	"unsafe"

	"github.com/syssam/velox/internal/engine"
)

// Mutator applies a write of entity E.
type Mutator[E any] interface {
	Mutate(ctx context.Context, m *Mutation[E]) error
}

// MutateFunc adapts a function to a Mutator.
type MutateFunc[E any] func(ctx context.Context, m *Mutation[E]) error

// Mutate calls f. It is an ordinary method, not a generic one, because
// Mutator's method set needs it.
func (f MutateFunc[E]) Mutate(ctx context.Context, m *Mutation[E]) error { return f(ctx, m) }

// Hook is middleware over an entity's writes: it returns a Mutator that does
// its work and calls next.Mutate exactly once, or not at all, to refuse or to
// make the write itself (see Mutation.SetAffected).
//
// An entity declares its hooks with a method on its pointer type,
//
//	func (*E) Hooks() []velox.Hook[E]
//
// which velox-gen binds in the generated metadata (Descriptor.Hooks calls
// BindHooks on it): a method with another signature does not compile, and one
// the metadata does not bind is refused at the entity's first use. velox
// calls it once, when it builds the entity's metadata, on a nil *E: the
// method must not read its receiver. Nor may its body, outside the hooks it returns, read the entity's
// own metadata (a query on it, say): that waits on the build in progress and
// deadlocks. A Has or HasWhere reads none, so the body may build them: a
// relation resolves when a statement holding the condition runs.
//
// The first hook listed is the outermost. Each hook function itself is
// called on every write: velox builds the chain anew for each one, so a hook
// may keep per-write state in its closure. Calling next a second time sends
// nothing and fails the write. Every insert (Insert and InsertAll, whose
// hooks run once over all its rows), update, delete and Save of the entity
// runs them, and so do their SQL(ctx) methods, where next renders the
// statement instead of sending it and IsDryRun(ctx) is true. A write a hook
// makes under that ctx, an audit row through Mutation.DB say, fails with
// ErrDryRun, and SQL(ctx) still returns the statement when the hook returns
// that error after next; reads run as normal.
//
// The guard (ErrNoWhere and the like) runs on an update or delete as the
// caller built it, before the hooks, and again inside next: a condition a
// hook adds never stands in for the caller's Where. Hooks run before next
// does any of its work: next fills an insert's defaults, adds an
// update's UpdateDefault assignments, refuses an Immutable column, runs the
// validators and the policy, and only then sends the statement. So
// validators see what hooks set.
//
// An error that velox returns from next passes back through the hooks as is
// when a hook returns it unchanged. Any other error a hook returns is wrapped
// "velox: <Entity>: hook: <err>", and errors.Is and errors.As reach it.
type Hook[E any] func(next Mutator[E]) Mutator[E]

// On returns h applied only to the given kinds of write: OpInsert, OpUpdate,
// OpDelete. Other writes skip it. On of a nil h is nil.
func On[E any](h Hook[E], ops ...Op) Hook[E] {
	if h == nil {
		return nil
	}
	return func(next Mutator[E]) Mutator[E] {
		wrapped := h(next)
		return MutateFunc[E](func(ctx context.Context, m *Mutation[E]) error {
			if slices.Contains(ops, m.Op()) {
				return wrapped.Mutate(ctx, m)
			}
			return next.Mutate(ctx, m)
		})
	}
}

// BindPolicy erases p's entity type, for Descriptor.Policy. For generated code.
// velox-gen writes
//
//	Policy: func() velox.BoundPolicy { return velox.BindPolicy((*E)(nil).Policy()) },
//
// only for an entity that declares the Policy method, so that a method of
// another signature does not compile, and an entity without one instantiates
// nothing here. Load calls the function once, when it builds the metadata;
// the method runs on a nil *E and must not read its receiver, nor, in its
// body outside the functions it returns, the entity's own metadata (see
// Policy).
func BindPolicy[E any](p Policy[E]) BoundPolicy { return engine.NewBoundPolicy(policyOf(p)) }

// policyOf is p with E erased.
func policyOf[E any](p Policy[E]) *engine.EntityPolicy {
	pol := &engine.EntityPolicy{}
	if r := p.Read; r != nil {
		pol.Read = func(ctx context.Context) (Predicate, error) { q, err := r(ctx); return Predicate(q), err }
	}
	if w := p.Write; w != nil {
		pol.Write = func(ctx context.Context) (Predicate, error) { q, err := w(ctx); return Predicate(q), err }
	}
	if ins := p.Insert; ins != nil {
		pol.Insert = func(ctx context.Context, x unsafe.Pointer) error { return ins(ctx, (*E)(x)) }
	}
	return pol
}

// BindHooks erases hs's entity type, for Descriptor.Hooks. For generated code.
// velox-gen writes
//
//	Hooks: func() velox.BoundHooks { return velox.BindHooks((*E)(nil).Hooks()) },
//
// only for an entity that declares the Hooks method (see Hook). A nil hook is
// refused when the metadata is built.
func BindHooks[E any](hs []Hook[E]) BoundHooks {
	var b BoundHooks
	for i, f := range hs {
		if f == nil {
			engine.SetBoundNil(&b, i+1)
			return b
		}
		engine.AddBoundHook(&b, erase(f))
	}
	return b
}

// erase returns h with E erased.
func erase[E any](h Hook[E]) engine.HookFn {
	return func(next engine.MutateFn) engine.MutateFn {
		typed := h(MutateFunc[E](func(ctx context.Context, m *Mutation[E]) error { return next(ctx, &m.mu) }))
		return func(ctx context.Context, mu *engine.MutationState) error {
			if typed == nil {
				return engine.ErrNilMutator
			}
			// A Mutation[E] is a mutation and nothing else, so mu is one.
			return typed.Mutate(ctx, (*Mutation[E])(unsafe.Pointer(mu)))
		}
	}
}
