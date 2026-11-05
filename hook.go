package velox

import (
	"context"
	"slices"
	"unsafe"

	"github.com/syssam/velox/internal/engine"
)

// Mutator applies a write.
type Mutator interface {
	Mutate(ctx context.Context, m *Mutation) error
}

// MutateFunc adapts a function to a Mutator.
type MutateFunc func(ctx context.Context, m *Mutation) error

// Mutate calls f.
func (f MutateFunc) Mutate(ctx context.Context, m *Mutation) error { return f(ctx, m) }

// Hook is middleware over an entity's writes: it returns a Mutator that does
// its work and calls next.Mutate exactly once, or not at all, to refuse or to
// make the write itself (see Mutation.SetAffected). It names columns by
// name (Mutation.Set, Fill, Value, Rows), so a mixin's hook serves every
// entity that lists it.
//
// A generated entity's hooks are declared in the schema package, on the
// entity's schema or on a mixin it lists (see package schema), and bound
// into the generated metadata by velox-gen. An entity written by hand
// declares them with a method on its pointer type,
// func (*E) Hooks() []velox.Hook, which its Descriptor lists; a method the
// metadata does not bind is refused at the entity's first use. velox calls
// the method once, when it builds the entity's metadata: a hand-written one
// on a nil *E, so it must not read its receiver. Nor may its body, outside
// the hooks it returns, read the entity's own metadata (a query on it, say):
// that waits on the build in progress and deadlocks. A hook that must name
// the generated types, to insert an audit row say, is the application's:
// WithHooks adds it.
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
// does any of its work: next fills an insert's defaults, adds an update's
// UpdateDefault assignments, refuses an Immutable column, runs the
// validators and the policy, and only then sends the statement. So
// validators and Policy.Insert see what hooks set.
//
// An error that velox returns from next passes back through the hooks as is
// when a hook returns it unchanged. Any other error a hook returns is wrapped
// "velox: <Entity>: hook: <err>", and errors.Is and errors.As reach it.
type Hook func(next Mutator) Mutator

// On returns h applied only to the given kinds of write: OpInsert, OpUpdate,
// OpDelete. Other writes skip it. On of a nil h is nil.
func On(h Hook, ops ...Op) Hook {
	if h == nil {
		return nil
	}
	return func(next Mutator) Mutator {
		wrapped := h(next)
		return MutateFunc(func(ctx context.Context, m *Mutation) error {
			if slices.Contains(ops, m.Op()) {
				return wrapped.Mutate(ctx, m)
			}
			return next.Mutate(ctx, m)
		})
	}
}

// Before returns the hook that runs f, then next when f returned nil, on
// the given kinds of write, as On applies a hook. With no op it runs on no
// write. It is the hook that fills or checks a write before velox makes it:
//
//	velox.Before(func(ctx context.Context, m *velox.Mutation) error {
//		return m.Fill("created_by", userFrom(ctx))
//	}, velox.OpInsert)
//
// Before of a nil f is nil.
func Before(f MutateFunc, ops ...Op) Hook {
	if f == nil {
		return nil
	}
	return On(func(next Mutator) Mutator {
		return MutateFunc(func(ctx context.Context, m *Mutation) error {
			if err := f(ctx, m); err != nil {
				return err
			}
			return next.Mutate(ctx, m)
		})
	}, ops...)
}

// WithHooks adds hs to the hooks of entity E, for the DB and every Tx and
// shard it opens, as ent's client.User.Use does. They run after the
// entity's own hooks, the schema's, innermost: a mixin's Fill has run when
// they see the rows, and they cannot skip it.
//
// It is for a hook that must name the generated types, which the schema
// cannot: one that inserts an audit row, or reads E's rows as *E with
// Row.Entity. A rule that names columns only is the schema's.
//
//	db, err := velox.Connect("sqlite", dsn, velox.WithHooks[gen.Task](velox.On(audit, velox.OpUpdate)))
//
// A nil hook panics, as a nil Rewrite does. WithHooks builds E's metadata;
// an E whose metadata failed fails its statements, and gets no hooks.
func WithHooks[E any, PE Entity[E]](hs ...Hook) Option {
	for _, h := range hs {
		if h == nil {
			panic("velox: WithHooks: a nil hook")
		}
	}
	return engine.WithEntityHooks(PE(nil).VeloxMeta().info, hookFns(hs))
}

// bind keeps in info the interceptors, policy and hooks that ps, hs and is
// compose. It is not generic, so that Load, compiled for every entity,
// instantiates nothing for them. The policies are ANDed in order, the hooks
// run in order, the first outermost, and the interceptors' conditions are
// ANDed in order.
func bind(info *EntityInfo, ps []func() Policy, hs []func() []Hook, is []func() []Interceptor) error {
	if len(is) > 0 {
		err := engine.EraseFilters(info, func() ([]engine.FilterFn, string) {
			var fs []engine.FilterFn
			for i, f := range is {
				if f == nil {
					return nil, engine.NilBinding("Interceptors", i)
				}
				own, reason := engine.FiltersFor(f(), len(fs))
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
	if len(ps) > 0 {
		err := engine.ErasePolicy(info, func() (*engine.EntityPolicy, string) {
			all := make([]*engine.EntityPolicy, len(ps))
			for i, p := range ps {
				if p == nil {
					return nil, engine.NilBinding("Policy", i)
				}
				all[i] = engine.PolicyFor(info, p())
			}
			return engine.AndPolicies(all), ""
		})
		if err != nil {
			return err
		}
	}
	if len(hs) == 0 {
		return nil
	}
	return engine.EraseHooks(info, func() ([]engine.HookFn, string) {
		var all []engine.HookFn
		for i, h := range hs {
			if h == nil {
				return nil, engine.NilBinding("Hooks", i)
			}
			own := h()
			for j, x := range own {
				if x == nil {
					return nil, engine.NilHook(len(all) + j)
				}
			}
			all = append(all, hookFns(own)...)
		}
		return all, ""
	})
}

// hookFns is hs as the engine runs them.
func hookFns(hs []Hook) []engine.HookFn {
	out := make([]engine.HookFn, len(hs))
	for i, h := range hs {
		out[i] = erase(h)
	}
	return out
}

// erase returns h as the engine runs it.
func erase(h Hook) engine.HookFn {
	return func(next engine.MutateFn) engine.MutateFn {
		m := h(MutateFunc(func(ctx context.Context, m *Mutation) error { return next(ctx, &m.mu) }))
		return func(ctx context.Context, mu *engine.MutationState) error {
			if m == nil {
				return engine.ErrNilMutator
			}
			// A Mutation is a mutation and nothing else, so mu is one.
			return m.Mutate(ctx, (*Mutation)(unsafe.Pointer(mu)))
		}
	}
}
