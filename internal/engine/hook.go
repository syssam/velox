package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"unsafe"
)

// MutationState is a write in flight, without E: what Mutation[E] holds. Its DB is an
// any, a *DB that Mutation.DB converts: EntityInfo reaches mutation, and a *DB field
// made every Descriptor entity's package 14 KiB larger (compilecost_test.go).
type MutationState struct {
	DB       any              // the *DB the write runs on; set only when in has hooks
	Op       Op               // set before the hooks run: Mutation.Op reads it, since an insert has no st
	St       *Statement       // the update or delete; nil for an insert
	Rows     []unsafe.Pointer // an insert's rows
	Affected int64            // the rows the write affected, for Mutation.Affected
	Where    []Predicate      // st.Where when next was called, for Mutation.Conds
	Own      bool             // the hook called SetAffected: it made the write itself
	ownSet   bool             // st.Set is the mutation's own array, cloned once
	Err      error            // a misuse recorded by ValueIn, Set, Where or SetAffected
	Core     error            // the error next returned, to pass through unwrapped
	Called   bool             // next has been called
	Dry      bool             // under SQL(ctx): the core renders and does not send
	SQL      string
	Args     []any // what the core rendered under SQL(ctx)
}

// MutateFn is the non-generic form of a mutation hook's next step.
type MutateFn func(ctx context.Context, mu *MutationState) error

// HookFn is a Hook[E] with E erased, made once when the metadata is built.
type HookFn func(next MutateFn) MutateFn

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
	// ErrNilMutator is the error of a hook that returned a nil Mutator.
	ErrNilMutator = errors.New("a hook returned a nil Mutator")
	// errNextTwice is the error of a hook that called next twice.
	errNextTwice = errors.New("a hook called next twice")
	// errOwnAndNext is the error of a hook that called both SetAffected and
	// next.
	errOwnAndNext = errors.New("SetAffected is for a hook that makes the write itself; it called next too")
)

// BoundPolicy is an entity's Policy with E erased, made by BindPolicy for
// Descriptor.Policy. For generated code. Its fields are not exported: only
// BindPolicy makes one.
type BoundPolicy struct{ p *EntityPolicy }

// BoundHooks is an entity's hooks with E erased, made by BindHooks for
// Descriptor.Hooks. For generated code. Its fields are not exported: only
// BindHooks makes one.
type BoundHooks struct {
	hs     []HookFn
	nilAt1 int // 1 + the index of the first nil hook; 0 when none
}

// BoundInterceptors is an entity's interceptors with E erased, made by
// BindInterceptors for Descriptor.Interceptors. For generated code. Its fields
// are not exported: only BindInterceptors makes one.
type BoundInterceptors struct {
	fs     []FilterFn
	nilAt1 int // 1 + the index of the first nil Filter; 0 when none
}

// FilterFn is an Interceptor's Filter with E erased.
type FilterFn func(ctx context.Context) (Predicate, error)

// AddBoundFilter appends f to b's filters.
func AddBoundFilter(b *BoundInterceptors, f FilterFn) { b.fs = append(b.fs, f) }

// SetFilterNil records that interceptor i has a nil Filter.
func SetFilterNil(b *BoundInterceptors, i int) { b.nilAt1 = i + 1 }

// FiltersOf is b's filters, and the reason a nil Filter is refused when one
// of them is: base is the index of b's first in the entity's list.
func FiltersOf(b BoundInterceptors, base int) ([]FilterFn, string) {
	if b.nilAt1 != 0 {
		return nil, fmt.Sprintf("Interceptors()[%d] has a nil Filter", base+b.nilAt1-1)
	}
	return b.fs, ""
}

// EraseFilters calls f, which composes the Descriptor's Interceptors with E
// erased, and keeps its filters in info. A panic in f is the entity's
// *SchemaError, and so is the reason f returns when it refuses them. On the
// join entity of a bidirectional edge (Descriptor.Bidi) it refuses any.
func EraseFilters(info *EntityInfo, f func() ([]FilterFn, string)) error {
	var (
		fs     []FilterFn
		reason string
	)
	if err := recovered(info.Name, "Interceptors()", func() { fs, reason = f() }); err != nil {
		return err
	}
	if reason != "" {
		return &SchemaError{Entity: info.Name, Reason: reason}
	}
	if info.bidi && len(fs) > 0 {
		return &SchemaError{Entity: info.Name, Reason: "the join entity of a bidirectional edge cannot have interceptors: " +
			"their conditions would show or change one row of a link"}
	}
	info.filters = fs
	return nil
}

// AndPolicies is the policy that ANDs ps, in order: Read is the AND of each
// non-nil Read, and so is Write, and Insert runs each non-nil Insert. A nil
// in ps is skipped; nil when none is left. It returns the one policy left
// as is.
func AndPolicies(ps []*EntityPolicy) *EntityPolicy {
	ps = slices.DeleteFunc(slices.Clone(ps), func(p *EntityPolicy) bool { return p == nil })
	switch len(ps) {
	case 0:
		return nil
	case 1:
		return ps[0]
	}
	var reads, writes []func(ctx context.Context) (Predicate, error)
	var inserts []func(ctx context.Context, p unsafe.Pointer) error
	for _, p := range ps {
		if p.Read != nil {
			reads = append(reads, p.Read)
		}
		if p.Write != nil {
			writes = append(writes, p.Write)
		}
		if p.Insert != nil {
			inserts = append(inserts, p.Insert)
		}
	}
	out := &EntityPolicy{Read: andConds(reads), Write: andConds(writes)}
	switch len(inserts) {
	case 0:
	case 1:
		out.Insert = inserts[0]
	default:
		out.Insert = func(ctx context.Context, p unsafe.Pointer) error {
			for _, f := range inserts {
				if err := f(ctx, p); err != nil {
					return err
				}
			}
			return nil
		}
	}
	return out
}

// andConds is the condition that ANDs what each of fs returns, in order; nil
// for none. The first error, or the first zero Pred, ends it: PolicyCond
// denies the one and fails the other. An Allow adds nothing.
func andConds(fs []func(ctx context.Context) (Predicate, error)) func(ctx context.Context) (Predicate, error) {
	switch len(fs) {
	case 0:
		return nil
	case 1:
		return fs[0]
	}
	return func(ctx context.Context) (Predicate, error) {
		var kids []Node
		for _, f := range fs {
			p, err := f(ctx)
			switch {
			case err != nil:
				return Predicate{}, err
			case p.n.Op == opZero:
				return Predicate{}, nil
			case p.n.Op == OpAllow:
				continue
			}
			kids = append(kids, p.n)
		}
		switch len(kids) {
		case 0:
			return Predicate{Node{Op: OpAllow}}, nil
		case 1:
			return Predicate{kids[0]}, nil
		}
		return Predicate{Group(OpAnd, kids)}, nil
	}
}

// NewBoundPolicy wraps p.
func NewBoundPolicy(p *EntityPolicy) BoundPolicy { return BoundPolicy{p} }

// PolicyOf is the policy b wraps; nil for the zero BoundPolicy.
func PolicyOf(b BoundPolicy) *EntityPolicy { return b.p }

// AddBoundHook appends h to b's hooks.
func AddBoundHook(b *BoundHooks, h HookFn) { b.hs = append(b.hs, h) }

// SetBoundNil records that hook i is nil.
func SetBoundNil(b *BoundHooks, i int) { b.nilAt1 = i + 1 }

// HooksOf is b's hooks, and NilHook's reason when one of them is nil: base
// is the index of b's first in the entity's list.
func HooksOf(b BoundHooks, base int) ([]HookFn, string) {
	if b.nilAt1 != 0 {
		return nil, NilHook(base + b.nilAt1 - 1)
	}
	return b.hs, ""
}

// ErasePolicy calls f, which composes the Descriptor's Policy with E erased,
// and keeps its result in info. A panic in f is the entity's *SchemaError,
// and so is the reason f returns when it refuses them, such as NilBinding's. On the join entity of a bidirectional edge (Descriptor.Bidi)
// it refuses a Read or Write.
func ErasePolicy(info *EntityInfo, f func() (*EntityPolicy, string)) error {
	var (
		p      *EntityPolicy
		reason string
	)
	if err := recovered(info.Name, "Policy()", func() { p, reason = f() }); err != nil {
		return err
	}
	if reason != "" {
		return &SchemaError{Entity: info.Name, Reason: reason}
	}
	info.policy = p
	if info.bidi && p != nil && (p.Read != nil || p.Write != nil) {
		return &SchemaError{Entity: info.Name, Reason: "the join entity of a bidirectional edge cannot have a Read or Write policy: " +
			"its conditions would show or delete one row of a link; check links in Policy.Insert and deletes in a hook"}
	}
	return nil
}

// EraseHooks calls f, which composes the Descriptor's Hooks with E
// erased, and keeps its hooks in info. A panic in f is the entity's
// *SchemaError, and so is the reason f returns when it refuses them, such as
// NilHook's.
func EraseHooks(info *EntityInfo, f func() ([]HookFn, string)) error {
	var (
		hs     []HookFn
		reason string
	)
	if err := recovered(info.Name, "Hooks()", func() { hs, reason = f() }); err != nil {
		return err
	}
	if reason != "" {
		return &SchemaError{Entity: info.Name, Reason: reason}
	}
	info.hooks = append(info.hooks, hs...)
	return nil
}

// NilHook is the reason a nil hook is refused: Hooks()[i]. It is not inlined, so that the generic
// bindings that call it do not compile its body
// (TestGenericTypesCompileSmall).
//
//go:noinline
func NilHook(i int) string {
	return fmt.Sprintf("Hooks()[%d] is nil", i)
}

// NilBinding is the reason a nil function at index i of the Descriptor's
// field is refused. It is not inlined, as NilHook is not.
//
//go:noinline
func NilBinding(field string, i int) string {
	return fmt.Sprintf("Descriptor.%s[%d] is nil", field, i)
}

// recovered calls f, recovering a panic as the entity's *SchemaError. A
// NotLinkedError's is held in an unlinkedError, which Unlinked unwraps, so
// that the build is retried.
func recovered(entity, what string, f func()) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = &SchemaError{Entity: entity, Reason: fmt.Sprintf("%s panicked: %v; it is called on a nil receiver", what, p)}
			if _, ok := p.(NotLinkedError); ok {
				err = unlinkedError{err.(*SchemaError)}
			}
		}
	}()
	f()
	return nil
}

// unlinkedError is the *SchemaError of a binding that panicked with a
// NotLinkedError: one that found another entity's columns by name before
// that entity's package had run its init.
type unlinkedError struct{ *SchemaError }

// Unlinked reports whether err is a binding's failure that a NotLinkedError
// caused, which Load does not keep, and returns err unwrapped.
func Unlinked(err error) (bool, error) {
	if u, ok := err.(unlinkedError); ok {
		return true, u.SchemaError
	}
	return false, err
}

// Mutate runs in's hooks around core, which runs on db: the hooks reach it
// as Mutation.DB. With no hooks it calls core alone. The writes call core
// directly when in has no hooks, so that none of them pays for the closure
// and the mutation this takes, or for setting mu.db.
func Mutate(ctx context.Context, db *DB, in *EntityInfo, mu *MutationState, core MutateFn) error {
	if len(InfoHooks(in)) == 0 {
		return core(ctx, mu)
	}
	mu.DB = db
	next := func(ctx context.Context, mu *MutationState) error {
		if mu.Called {
			// Running the core again would send the write again.
			if mu.Err == nil {
				mu.Err = errNextTwice
			}
			mu.Core = newStageError(in.Name, "hook", errNextTwice)
			return mu.Core
		}
		mu.Called = true
		if mu.St != nil {
			// The conditions as the caller and the hooks gave them: prepare
			// only appends to st.Where, or replaces it with a copy.
			mu.Where = mu.St.Where
		}
		if mu.Own && mu.Err == nil {
			mu.Err = errOwnAndNext
		}
		if mu.Err != nil {
			// Refuse before sending: the misuse would otherwise be
			// reported only after the write was made.
			mu.Core = newStageError(in.Name, "hook", mu.Err)
			return mu.Core
		}
		mu.Core = core(ctx, mu)
		return mu.Core
	}
	for _, h := range slices.Backward(InfoHooks(in)) {
		next = h(next)
	}
	err := next(ctx, mu)
	switch {
	case err == nil && mu.Err != nil:
		return newStageError(in.Name, "hook", mu.Err)
	case err == nil, identical(err, mu.Core):
		return err
	}
	return newStageError(in.Name, "hook", err)
}
