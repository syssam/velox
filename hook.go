package velox

import (
	"context"
	"fmt"
	"slices"
	"unsafe"

	"github.com/syssam/velox/internal/engine"
	"github.com/syssam/velox/schema"
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
// entity's schema or on a mixin it lists (see package schema), and read
// from the schema's value that the generated descriptor names. An entity
// written by hand declares them with a method on its pointer type,
// func (*E) Hooks() []velox.Hook, and names itself in its Descriptor,
// Schema: (*E)(nil); a method the metadata does not reach is refused at
// the entity's first use. velox calls
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

// Condition reports whether a hook applies to a write: If and Unless take
// one. HasOp, HasColumns and HasCleared make the common ones; any function
// of the Mutation is one, as in ent's hook package.
type Condition func(ctx context.Context, m *Mutation) bool

// If returns h applied only to the writes cond holds for; the others skip
// it, as ent's hook.If does:
//
//	velox.If(audit, velox.HasColumns("status"))
//
// It works alike for a schema's hooks and WithHooks's. cond runs before h,
// on the write as the hooks outside h left it. If of a nil h, or with a nil
// cond, is nil, which a schema's Hooks and WithHooks refuse.
func If(h Hook, cond Condition) Hook {
	if h == nil || cond == nil {
		return nil
	}
	return func(next Mutator) Mutator {
		wrapped := h(next)
		return MutateFunc(func(ctx context.Context, m *Mutation) error {
			if cond(ctx, m) {
				return wrapped.Mutate(ctx, m)
			}
			return next.Mutate(ctx, m)
		})
	}
}

// Unless returns h applied only to the writes cond does not hold for, as
// ent's hook.Unless does. Unless of a nil h, or with a nil cond, is nil.
func Unless(h Hook, cond Condition) Hook {
	if cond == nil {
		return nil
	}
	return If(h, func(ctx context.Context, m *Mutation) bool { return !cond(ctx, m) })
}

// HasOp is the condition that the write is of one of the given kinds:
// OpInsert, OpUpdate, OpDelete. With no op it holds for none.
func HasOp(ops ...Op) Condition {
	return func(_ context.Context, m *Mutation) bool { return slices.Contains(ops, m.Op()) }
}

// HasColumns is the condition that the write assigns every one of the
// given columns, by name, as Mutation.Columns reports them, as ent's
// hook.HasFields is. With no name it holds for every write.
func HasColumns(names ...string) Condition {
	return func(_ context.Context, m *Mutation) bool {
		cols := m.Columns()
		for _, n := range names {
			if !slices.Contains(cols, n) {
				return false
			}
		}
		return true
	}
}

// HasCleared is the condition that an update sets every one of the given
// columns to NULL, as Mutation.Cleared reports it, as ent's
// hook.HasClearedFields is. A name the entity has no column of is a
// misuse that fails the write.
func HasCleared(names ...string) Condition {
	return func(_ context.Context, m *Mutation) bool {
		for _, n := range names {
			if !m.Cleared(n) {
				return false
			}
		}
		return true
	}
}

// Reject returns the hook that refuses the given kinds of write, as ent's
// hook.Reject does: such a write fails before anything is sent, with an
// error that matches ErrDenied. Other writes pass.
//
//	func (Ledger) Hooks() []velox.Hook { return []velox.Hook{velox.Reject(velox.OpUpdate, velox.OpDelete)} }
func Reject(ops ...Op) Hook {
	return func(next Mutator) Mutator {
		return MutateFunc(func(ctx context.Context, m *Mutation) error {
			if slices.Contains(ops, m.Op()) {
				return fmt.Errorf("%v is rejected: %w", m.Op(), ErrDenied)
			}
			return next.Mutate(ctx, m)
		})
	}
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
//	db, err := velox.Connect("sqlite", dsn, velox.WithHooks[veloxgen.Task](velox.On(audit, velox.OpUpdate)))
//
// A nil hook panics, as a nil Rewrite does. WithHooks builds E's metadata;
// an E whose metadata failed fails its statements, and gets no hooks.
func WithHooks[E any, PE Entity[E]](hs ...Hook) Option {
	for _, h := range hs {
		if h == nil {
			panic("velox: WithHooks: a nil hook")
		}
	}
	return engine.WithEntityHooks(stateOf(PE(nil).VeloxMeta()).Info, hookFns(hs))
}

// WithHooksAll adds hs to the hooks of every entity, for the DB and every
// Tx and shard it opens, as ent's client.Use does. They run before the
// entity's own hooks and WithHooks's, outermost, the first given first, and
// a later WithHooksAll's after an earlier one's. Mutation.Entity names the
// entity a hook is called for, so one hook serves some entities and passes
// the others to next:
//
//	audit := velox.Before(func(ctx context.Context, m *velox.Mutation) error {
//		if m.Entity() != "Invoice" {
//			return nil
//		}
//		return m.Fill("updated_by", userFrom(ctx))
//	}, velox.OpInsert, velox.OpUpdate)
//	db, err := velox.Connect("postgres", dsn, velox.WithHooksAll(audit))
//
// They follow every rule of Hook: the same transaction, errors and change
// recording as WithHooks's. A join entity of a many-to-many edge is an
// entity too: the writes of its rows that Rel.Add and the like make run
// them. A nil hook panics.
func WithHooksAll(hs ...Hook) Option {
	for _, h := range hs {
		if h == nil {
			panic("velox: WithHooksAll: a nil hook")
		}
	}
	return engine.WithAllHooks(hookFns(hs))
}

// bind keeps in info the interceptors, policy and hooks that s declares:
// those of each value its Mixin method returns, in order, then its own. It
// is not generic, so that Load, compiled for every entity, instantiates
// nothing for them. The policies are ANDed in order, the hooks run in
// order, the first outermost, and the interceptors' conditions are ANDed in
// order.
func bind(info *EntityInfo, s any) error {
	srcs, err := behaviourSources(info, s)
	if err != nil {
		return err
	}
	b, err := declaredBehaviour(info, srcs)
	if err != nil {
		return err
	}
	if len(b.policies) == 0 && len(b.hooks) == 0 && len(b.interceptors) == 0 {
		return engine.NoBehaviour(info, s)
	}
	if err := bindInterceptors(info, b.interceptors); err != nil {
		return err
	}
	if err := bindPolicies(info, b.policies); err != nil {
		return err
	}
	return bindHooks(info, b.hooks)
}

// behaviourSources returns the values that may declare s's behaviour: each
// non-nil value its Mixin method returns, in order, then s.
func behaviourSources(info *EntityInfo, s any) ([]any, error) {
	var srcs []any
	err := engine.Recover(info, "Mixin()", func() {
		if m, ok := s.(interface{ Mixin() []schema.Mixin }); ok {
			for _, x := range m.Mixin() {
				if x != nil {
					srcs = append(srcs, engine.Addr(x))
				}
			}
		}
	})
	if err != nil {
		return nil, err
	}
	return append(srcs, engine.Addr(s)), nil
}

// behaviour is what a schema and its mixins declare, in order.
type behaviour struct {
	policies     []interface{ Policy() Policy }
	hooks        []interface{ Hooks() []Hook }
	interceptors []interface{ Interceptors() []Interceptor }
}

// declaredBehaviour returns the behaviour that srcs declare. It refuses a
// source with a Policy, Hooks or Interceptors method of the wrong type.
func declaredBehaviour(info *EntityInfo, srcs []any) (behaviour, error) {
	var b behaviour
	for _, x := range srcs {
		if v, ok := x.(interface{ Policy() Policy }); ok {
			b.policies = append(b.policies, v)
		} else if err := engine.WrongBehaviour(info, x, "Policy", "func() velox.Policy"); err != nil {
			return behaviour{}, err
		}
		if v, ok := x.(interface{ Hooks() []Hook }); ok {
			b.hooks = append(b.hooks, v)
		} else if err := engine.WrongBehaviour(info, x, "Hooks", "func() []velox.Hook"); err != nil {
			return behaviour{}, err
		}
		if v, ok := x.(interface{ Interceptors() []Interceptor }); ok {
			b.interceptors = append(b.interceptors, v)
		} else if err := engine.WrongBehaviour(info, x, "Interceptors", "func() []velox.Interceptor"); err != nil {
			return behaviour{}, err
		}
	}
	return b, nil
}

// bindInterceptors keeps in info the conditions of is, ANDed in order.
func bindInterceptors(info *EntityInfo, is []interface{ Interceptors() []Interceptor }) error {
	if len(is) == 0 {
		return nil
	}
	return engine.EraseFilters(info, func() ([]engine.FilterFn, string) {
		var fs []engine.FilterFn
		for _, x := range is {
			own, reason := engine.FiltersFor(x.Interceptors(), len(fs))
			if reason != "" {
				return nil, reason
			}
			fs = append(fs, own...)
		}
		return fs, ""
	})
}

// bindPolicies keeps in info the policies of ps, ANDed in order.
func bindPolicies(info *EntityInfo, ps []interface{ Policy() Policy }) error {
	if len(ps) == 0 {
		return nil
	}
	return engine.ErasePolicy(info, func() (*engine.EntityPolicy, string) {
		all := make([]*engine.EntityPolicy, len(ps))
		for i, x := range ps {
			all[i] = engine.PolicyFor(info, erasePolicy(x.Policy()))
		}
		return engine.AndPolicies(all), ""
	})
}

// bindHooks keeps in info the hooks of hs, in order, the first outermost.
func bindHooks(info *EntityInfo, hs []interface{ Hooks() []Hook }) error {
	if len(hs) == 0 {
		return nil
	}
	return engine.EraseHooks(info, func() ([]engine.HookFn, string) {
		var all []engine.HookFn
		for _, x := range hs {
			own := x.Hooks()
			for j, h := range own {
				if h == nil {
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
