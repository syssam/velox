package velox

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"unsafe"
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
// its work and calls next.Mutate exactly once (or not at all, to refuse).
//
// An entity declares its hooks with a method on its pointer type,
//
//	func (*E) Hooks() []velox.Hook[E]
//
// which velox-gen binds in the generated metadata (Desc.Hooks is (*E).Hooks):
// a method with another signature does not compile, and one the metadata does
// not bind is refused at the entity's first use. velox calls it once, when it
// builds the entity's metadata, on a nil *E: the method must not read its
// receiver. Nor may its body, outside the hooks it returns, read the entity's
// own metadata (a Has or HasWhere on a relation from or to it, say): that
// waits on the build in progress and deadlocks. Build such predicates inside
// the hook functions.
//
// The first hook listed is the outermost. Each hook function itself is
// called on every write: velox builds the chain anew for each one, so a hook
// may keep per-write state in its closure. Calling next a second time sends
// nothing and fails the write. Every insert (Insert and InsertAll, whose
// hooks run once over all its rows), update, delete and Save of the entity
// runs them, and so do their SQL(ctx) methods, where next renders the
// statement instead of sending it and IsDryRun(ctx) is true.
//
// The guard (ErrNoWhere and the like) runs on an update or delete as the
// caller built it, before the hooks, and again inside next: a condition a
// hook adds never stands in for the caller's Where. Hooks run before next
// does any of its work: next fills an insert's defaults and tenant, adds an
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

// mutation is a write in flight, without E: what Mutation[E] is. It holds no
// *DB: EntityInfo reaches it, and every entity's package would then carry
// DB's export data, its generic methods' bodies included.
type mutation struct {
	op     Op               // set before the hooks run: Mutation.Op reads it, since an insert has no st
	st     *Statement       // the update or delete; nil for an insert
	rows   []unsafe.Pointer // an insert's rows
	res    Result
	err    error // a misuse recorded by Value, Set or Where
	core   error // the error next returned, to pass through unwrapped
	called bool  // next has been called
	dry    bool  // under SQL(ctx): the core renders and does not send
	sql    string
	args   []any // what the core rendered under SQL(ctx)
}

type mutateFn func(ctx context.Context, mu *mutation) error

// hookFn is a Hook[E] with E erased, made once when the metadata is built.
type hookFn func(next mutateFn) mutateFn

// mutate runs in's hooks around core. With no hooks it calls core alone. The
// writes call core directly when in has no hooks, so that none of them pays
// for the closure and the mutation this takes.
func mutate(ctx context.Context, in *EntityInfo, mu *mutation, core mutateFn) error {
	if len(in.hooks) == 0 {
		return core(ctx, mu)
	}
	next := func(ctx context.Context, mu *mutation) error {
		if mu.called {
			// Running the core again would send the write again.
			if mu.err == nil {
				mu.err = errNextTwice
			}
			mu.core = &hookError{in.Name, "hook", errNextTwice}
			return mu.core
		}
		mu.called = true
		if mu.err != nil {
			// Refuse before sending: the misuse would otherwise be
			// reported only after the write was made.
			mu.core = &hookError{in.Name, "hook", mu.err}
			return mu.core
		}
		mu.core = core(ctx, mu)
		return mu.core
	}
	for i := len(in.hooks) - 1; i >= 0; i-- {
		next = in.hooks[i](next)
	}
	err := next(ctx, mu)
	switch {
	case err == nil && mu.err != nil:
		return &hookError{in.Name, "hook", mu.err}
	case err == nil, identical(err, mu.core):
		return err
	}
	return &hookError{in.Name, "hook", err}
}

// identical reports whether err is core itself. An error whose type cannot be
// compared is not, rather than a panic.
func identical(err, core error) bool {
	if core == nil {
		return false
	}
	t := reflect.TypeOf(err)
	return t == reflect.TypeOf(core) && t.Comparable() && err == core
}

var (
	// errNilMutator is the error of a hook that returned a nil Mutator.
	errNilMutator = errors.New("a hook returned a nil Mutator")
	// errNextTwice is the error of a hook that called next twice.
	errNextTwice = errors.New("a hook called next twice")
)

// erasedPolicy is Desc.Policy, a func(*E) Policy[E], with E erased. The types
// have one memory layout and one calling convention: *E is an unsafe.Pointer,
// and Policy[E] is policy with Pred[E] for Predicate and *E for
// unsafe.Pointer, all of them one pointer or one struct of pointers.
// TestErasedLayouts checks it. Load converts the function's address rather
// than instantiating a conversion of Policy[E] and every closure in it for
// each entity, which cost about 25 KiB in every entity package, used or not.
type erasedPolicy = func(unsafe.Pointer) policy

// erasedHooks is Desc.Hooks, a func(*E) []Hook[E], with E erased: a Hook[E]
// is a func(Mutator[E]) Mutator[E], and Mutator[E] an interface of the one
// method Mutate(ctx, *Mutation[E]), which is called through the interface's
// method table and so does not care which interface type holds it.
type erasedHooks = func(unsafe.Pointer) []func(rawMutator) rawMutator

// rawMutator is a Mutator[E] with E erased, as the hook functions see it.
type rawMutator interface {
	Mutate(ctx context.Context, m *mutation) error
}

// rawNext is a mutateFn as a rawMutator.
type rawNext mutateFn

func (f rawNext) Mutate(ctx context.Context, m *mutation) error { return f(ctx, m) }

// bindPolicy calls f, the Desc's Policy, on a nil *E and keeps its result in
// info.
func bindPolicy(info *EntityInfo, f erasedPolicy) error {
	var pol policy
	if err := recovered(info.Name, "Policy()", func() { pol = f(nil) }); err != nil {
		return err
	}
	info.policy = &pol
	return nil
}

// bindHooks calls f, the Desc's Hooks, on a nil *E and keeps its hooks, with
// the mutation type erased, in info. A nil hook is refused.
func bindHooks(info *EntityInfo, f erasedHooks) error {
	var hs []func(rawMutator) rawMutator
	if err := recovered(info.Name, "Hooks()", func() { hs = f(nil) }); err != nil {
		return err
	}
	for i, h := range hs {
		if h == nil {
			return errNilHook(info.Name, i)
		}
		info.hooks = append(info.hooks, func(next mutateFn) mutateFn {
			typed := h(rawNext(next))
			return func(ctx context.Context, mu *mutation) error {
				if typed == nil {
					return errNilMutator
				}
				return typed.Mutate(ctx, mu)
			}
		})
	}
	return nil
}

// recovered calls f, recovering a panic as the entity's *SchemaError.
func recovered(entity, what string, f func()) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = &SchemaError{Entity: entity, Reason: fmt.Sprintf("%s panicked: %v; it is called on a nil receiver", what, p)}
		}
	}()
	f()
	return nil
}

func errNilHook(entity string, i int) error {
	return &SchemaError{Entity: entity, Reason: fmt.Sprintf("Hooks()[%d] is nil", i)}
}
