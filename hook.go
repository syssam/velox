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
// which its Descriptor binds (Descriptor.Hooks calls BindHooks on it); a
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

// BindPolicy erases p's entity type, for Descriptor.Policy. For generated
// code. The generated package binds the schema's method in a function that
// velox calls once, when it builds the metadata,
//
//	func() velox.BoundPolicy { return velox.BindPolicy(r.CommentPolicy()) }
//
// and an entity written by hand binds its own the same way, with
// (*E)(nil).Policy(). A method of another signature does not compile.
func BindPolicy[E any](p Policy[E]) BoundPolicy { return engine.NewBoundPolicy(policyOf(p)) }

// BindHooks erases hs's entity type, for Descriptor.Hooks, as BindPolicy
// erases a Policy. For generated code. A nil hook is refused, by its index
// in hs, when the metadata is built.
func BindHooks[E any](hs []Hook[E]) BoundHooks {
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

// BindHooksAfter is BindHooks of first followed by hs, and a nil hook in hs is
// reported at its index in hs: first is not counted. For generated code.
func BindHooksAfter[E any](first Hook[E], hs []Hook[E]) BoundHooks {
	var b BoundHooks
	engine.AddBoundHook(&b, erase(first))
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

// bind keeps in info the policy and hooks that s, p and h compose, for the
// entity whose new row is x. It is not generic, so that Load, compiled for
// every entity, instantiates nothing for them: BindPolicy and BindHooks
// erase a policy and hooks where they are bound, and a scope's bindings are
// ScopeKit's methods. The scope is outermost: its Wrap holds p's policy, and
// its Fill runs before h's hooks.
func bind(info *EntityInfo, x any, s Scoper, p func() BoundPolicy, h func() BoundHooks) error {
	if s != nil || p != nil {
		err := engine.ErasePolicy(info, func() (*engine.EntityPolicy, string) {
			var own *engine.EntityPolicy
			if p != nil {
				own = engine.PolicyOf(p())
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
	if s == nil && h == nil {
		return nil
	}
	return engine.EraseHooks(info, func() ([]engine.HookFn, string) {
		var hs []engine.HookFn
		if s != nil {
			fill, reason := s.scopeFill(x, s)
			if reason != "" {
				return nil, reason
			}
			hs = append(hs, fill)
		}
		if h != nil {
			own, reason := engine.HooksOf(h())
			if reason != "" {
				return nil, reason
			}
			hs = append(hs, own...)
		}
		return hs, ""
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
