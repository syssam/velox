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
// A generated entity's hooks are declared in the schema package, on the
// entity's schema (see package schema), and bound into the generated
// metadata by velox-gen. An entity written by hand
// declares them with a method on its pointer type,
//
//	func (*E) Hooks() []velox.Hook[E]
//
// which its Descriptor binds with BindHooks; a
// method the metadata does not bind is refused at the entity's first use.
// velox calls the method once, when it builds the entity's metadata: a hand
// written one on a nil *E, so it must not read its receiver. Nor may its
// body, outside the hooks it returns, read the entity's
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

// BindPolicy erases the entity type of the policy functions fs, for
// Descriptor.Policy: one entry per function, in order. For generated code.
// It does not call them: velox calls each once, when it builds the metadata.
// Package runtime binds an entity's sources, its mixins' first, and an
// entity written by hand binds its own method:
//
//	Policy: velox.BindPolicy((*E)(nil).Policy),
//
// A function of another signature does not compile. A nil one stays nil,
// which the metadata refuses.
//
//go:noinline
func BindPolicy[E any](fs ...func() Policy[E]) []func() BoundPolicy {
	// It is not inlined, nor are BindHooks and BindInterceptors: inlined,
	// the loop and its closure are compiled again into each caller.
	out := make([]func() BoundPolicy, len(fs))
	for i, f := range fs {
		if f != nil {
			out[i] = func() BoundPolicy { return engine.NewBoundPolicy(policyOf(f())) }
		}
	}
	return out
}

// BindHooks erases the entity type of the hook functions fs, for
// Descriptor.Hooks, as BindPolicy erases policies. For generated code. A
// nil hook a function returns is refused, by its index, when the metadata
// is built.
//
//go:noinline
func BindHooks[E any](fs ...func() []Hook[E]) []func() BoundHooks {
	out := make([]func() BoundHooks, len(fs))
	for i, f := range fs {
		if f != nil {
			out[i] = func() BoundHooks { return bindHooks(f()) }
		}
	}
	return out
}

// bindHooks erases hs's entity type.
func bindHooks[E any](hs []Hook[E]) BoundHooks {
	var b BoundHooks
	for i, f := range hs {
		if f == nil {
			engine.SetBoundNil(&b, i)
			return b
		}
		engine.AddBoundHook(&b, erase(f))
	}
	return b
}

// Scoper is what Descriptor.Scope holds. For generated code. It is a
// scope.Scope, or another type that embeds ScopeKit[E] and has the methods
//
//	Wrap(p velox.Policy[E]) velox.Policy[E]
//	Fill() velox.Hook[E]
//
// velox wraps the entity's policy, or an empty one, in Wrap, and runs the
// hook Fill returns before the entity's hooks. It calls each once, when it
// builds the metadata. A Scoper without Wrap or Fill, or of another entity,
// is refused then with a *SchemaError, and so is a nil hook from Fill.
type Scoper interface {
	scopePolicy(x, scope any, own *engine.EntityPolicy) (*engine.EntityPolicy, string)
	scopeFill(x, scope any) (engine.HookFn, string)
}

// ScopeKit makes the type that embeds it a Scoper of entity E. For generated
// code. It holds nothing. Package scope embeds it in scope.Scope.
//
// A scope's bindings are ScopeKit's methods, so that only an entity that has
// a scope compiles them.
type ScopeKit[E any] struct{}

// bind keeps in info the interceptors, policy and hooks that s, ps, hs and
// is compose, for the entity whose new row is x. It is not generic, so that
// Load, compiled for every entity, instantiates nothing for them: BindPolicy,
// BindHooks and BindInterceptors erase them where they are bound, and a
// scope's bindings are ScopeKit's methods. The policies are ANDed in order,
// the hooks run in order, the first outermost, and the interceptors' are
// ANDed in order. The scope is outermost: its Wrap holds the AND of ps, and
// its Fill runs before hs's hooks. retry says the failure is a
// NotLinkedError's, which Load does not keep.
func bind(info *EntityInfo, x any, s Scoper, ps []func() BoundPolicy, hs []func() BoundHooks, is []func() BoundInterceptors) (retry bool, err error) {
	return engine.Unlinked(bindAll(info, x, s, ps, hs, is))
}

// bindAll is bind, with a NotLinkedError's failure wrapped.
func bindAll(info *EntityInfo, x any, s Scoper, ps []func() BoundPolicy, hs []func() BoundHooks, is []func() BoundInterceptors) error {
	if len(is) > 0 {
		err := engine.EraseFilters(info, func() ([]engine.FilterFn, string) {
			var fs []engine.FilterFn
			for i, f := range is {
				if f == nil {
					return nil, engine.NilBinding("Interceptors", i)
				}
				own, reason := engine.FiltersOf(f(), len(fs))
				if reason != "" {
					return nil, reason
				}
				fs = append(fs, own...)
			}
			return fs, ""
		})
		if err != nil {
			return err
		}
	}
	if s != nil || len(ps) > 0 {
		err := engine.ErasePolicy(info, func() (*engine.EntityPolicy, string) {
			var own *engine.EntityPolicy
			if len(ps) > 0 {
				all := make([]*engine.EntityPolicy, len(ps))
				for i, p := range ps {
					if p == nil {
						return nil, engine.NilBinding("Policy", i)
					}
					all[i] = engine.PolicyOf(p())
				}
				own = engine.AndPolicies(all)
			}
			if s == nil {
				return own, ""
			}
			return s.scopePolicy(x, s, own)
		})
		if err != nil {
			return err
		}
	}
	if s == nil && len(hs) == 0 {
		return nil
	}
	return engine.EraseHooks(info, func() ([]engine.HookFn, string) {
		var all []engine.HookFn
		if s != nil {
			fill, reason := s.scopeFill(x, s)
			if reason != "" {
				return nil, reason
			}
			all = append(all, fill)
		}
		base := 0
		for i, h := range hs {
			if h == nil {
				return nil, engine.NilBinding("Hooks", i)
			}
			own, reason := engine.HooksOf(h(), base)
			if reason != "" {
				return nil, reason
			}
			all = append(all, own...)
			base += len(own)
		}
		return all, ""
	})
}

// wrapper and filler are a Scoper's methods.
type (
	wrapper[E any] interface{ Wrap(p Policy[E]) Policy[E] }
	filler[E any]  interface{ Fill() Hook[E] }
)

// scopePolicy is scope's Wrap around own, or around an empty Policy when
// own is nil, with E erased. scope is the Scoper that embeds k.
func (k ScopeKit[E]) scopePolicy(x, scope any, own *engine.EntityPolicy) (*engine.EntityPolicy, string) {
	if _, ok := x.(*E); !ok {
		return nil, engine.OtherEntity(scope)
	}
	w, ok := scope.(wrapper[E])
	if !ok {
		return nil, engine.ScopeLacks(scope, "Wrap(velox.Policy[E]) velox.Policy[E]")
	}
	return policyOf(w.Wrap(typedPolicy[E](own))), ""
}

// typedPolicy is pol as a Policy[E], undoing policyOf; the zero Policy when
// pol is nil. A field nil in pol is nil in the result.
func typedPolicy[E any](pol *engine.EntityPolicy) Policy[E] {
	var p Policy[E]
	if pol == nil {
		return p
	}
	if r := pol.Read; r != nil {
		p.Read = func(ctx context.Context) (Pred[E], error) { q, err := r(ctx); return Pred[E](q), err }
	}
	if w := pol.Write; w != nil {
		p.Write = func(ctx context.Context) (Pred[E], error) { q, err := w(ctx); return Pred[E](q), err }
	}
	if ins := pol.Insert; ins != nil {
		p.Insert = func(ctx context.Context, x *E) error { return ins(ctx, unsafe.Pointer(x)) }
	}
	return p
}

// scopeFill is scope's Fill hook with E erased. scope is the Scoper that
// embeds k.
func (k ScopeKit[E]) scopeFill(x, scope any) (engine.HookFn, string) {
	if _, ok := x.(*E); !ok {
		return nil, engine.OtherEntity(scope)
	}
	s, ok := scope.(filler[E])
	if !ok {
		return nil, engine.ScopeLacks(scope, "Fill() velox.Hook[E]")
	}
	fill := s.Fill()
	if fill == nil {
		return nil, engine.NilHook(-1)
	}
	return erase(fill), ""
}

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
