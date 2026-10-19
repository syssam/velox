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
// which velox-gen binds in the generated metadata (Desc.Hooks calls
// BindHooks on it): a method with another signature does not compile, and one
// the metadata does not bind is refused at the entity's first use. velox calls it once, when it
// builds the entity's metadata, on a nil *E: the method must not read its
// receiver. Nor may its body, outside the hooks it returns, read the entity's
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

// mutation is a write in flight, without E: what Mutation[E] is. Its db is an
// any, converted by Mutation.DB: EntityInfo reaches mutation, and a *DB field
// made every Desc entity's package 79 KiB larger (compilecost_test.go).
type mutation struct {
	db     any              // the *DB the write runs on; set only when in has hooks
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

// mutate runs in's hooks around core, which runs on db: the hooks reach it
// as Mutation.DB. With no hooks it calls core alone. The writes call core
// directly when in has no hooks, so that none of them pays for the closure
// and the mutation this takes, or for setting mu.db.
func mutate(ctx context.Context, db *DB, in *EntityInfo, mu *mutation, core mutateFn) error {
	if len(in.hooks) == 0 {
		return core(ctx, mu)
	}
	mu.db = db
	next := func(ctx context.Context, mu *mutation) error {
		if mu.called {
			// Running the core again would send the write again.
			if mu.err == nil {
				mu.err = errNextTwice
			}
			mu.core = &hookError{in.Name, errNextTwice}
			return mu.core
		}
		mu.called = true
		if mu.err != nil {
			// Refuse before sending: the misuse would otherwise be
			// reported only after the write was made.
			mu.core = &hookError{in.Name, mu.err}
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
		return &hookError{in.Name, mu.err}
	case err == nil, identical(err, mu.core):
		return err
	}
	return &hookError{in.Name, err}
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

// BoundPolicy is an entity's Policy with E erased, made by BindPolicy for
// Desc.Policy. Its fields are not exported: only BindPolicy makes one.
type BoundPolicy struct{ p *policy }

// BoundHooks is an entity's hooks with E erased, made by BindHooks for
// Desc.Hooks. Its fields are not exported: only BindHooks makes one.
type BoundHooks struct {
	hs     []hookFn
	nilAt1 int // 1 + the index of the first nil hook; 0 when none
}

// BindPolicy erases p's entity type, for Desc.Policy. velox-gen writes
//
//	Policy: func() velox.BoundPolicy { return velox.BindPolicy((*E)(nil).Policy()) },
//
// only for an entity that declares the Policy method, so that a method of
// another signature does not compile, and an entity without one instantiates
// nothing here. Load calls the function once, when it builds the metadata;
// the method runs on a nil *E and must not read its receiver, nor, in its
// body outside the functions it returns, the entity's own metadata (see
// Policy).
func BindPolicy[E any](p Policy[E]) BoundPolicy { return BoundPolicy{policyOf(p)} }

// policyOf is p with E erased.
func policyOf[E any](p Policy[E]) *policy {
	pol := &policy{}
	if r := p.Read; r != nil {
		pol.read = func(ctx context.Context) (Predicate, error) { q, err := r(ctx); return Predicate(q), err }
	}
	if w := p.Write; w != nil {
		pol.write = func(ctx context.Context) (Predicate, error) { q, err := w(ctx); return Predicate(q), err }
	}
	if ins := p.Insert; ins != nil {
		pol.insert = func(ctx context.Context, x unsafe.Pointer) error { return ins(ctx, (*E)(x)) }
	}
	return pol
}

// BindHooks erases hs's entity type, for Desc.Hooks. velox-gen writes
//
//	Hooks: func() velox.BoundHooks { return velox.BindHooks((*E)(nil).Hooks()) },
//
// only for an entity that declares the Hooks method (see Hook). A nil hook is
// refused when the metadata is built.
func BindHooks[E any](hs []Hook[E]) BoundHooks {
	var b BoundHooks
	for i, f := range hs {
		if f == nil {
			b.nilAt1 = i + 1
			return b
		}
		b.hs = append(b.hs, erase(f))
	}
	return b
}

// erase returns h with E erased.
func erase[E any](h Hook[E]) hookFn {
	return func(next mutateFn) mutateFn {
		typed := h(MutateFunc[E](func(ctx context.Context, m *Mutation[E]) error { return next(ctx, (*mutation)(m)) }))
		return func(ctx context.Context, mu *mutation) error {
			if typed == nil {
				return errNilMutator
			}
			return typed.Mutate(ctx, (*Mutation[E])(mu))
		}
	}
}

// bindPolicy calls f, the Desc's Policy, and keeps its result in info.
func bindPolicy(info *EntityInfo, f func() BoundPolicy) error {
	var b BoundPolicy
	if err := recovered(info.Name, "Policy()", func() { b = f() }); err != nil {
		return err
	}
	info.policy = b.p
	return nil
}

// bindHooks calls f, the Desc's Hooks, and keeps its hooks in info. A nil
// hook is refused.
func bindHooks(info *EntityInfo, f func() BoundHooks) error {
	var b BoundHooks
	if err := recovered(info.Name, "Hooks()", func() { b = f() }); err != nil {
		return err
	}
	if b.nilAt1 != 0 {
		return errNilHook(info.Name, b.nilAt1-1)
	}
	info.hooks = append(info.hooks, b.hs...)
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
