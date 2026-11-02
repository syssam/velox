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
// Descriptor.Policy. For generated code. Its fields are not exported: only BindPolicy
// makes one.
type BoundPolicy struct{ p *EntityPolicy }

// BoundHooks is an entity's hooks with E erased, made by BindHooks for
// Descriptor.Hooks. For generated code. Its fields are not exported: only BindHooks
// makes one.
type BoundHooks struct {
	hs     []HookFn
	nilAt1 int // 1 + the index of the first nil hook; 0 when none
}

// ErasePolicy calls f, the Descriptor's Policy, and keeps its result in info. On the
// join entity of a bidirectional edge (Descriptor.Bidi) it refuses a Read or Write.
func ErasePolicy(info *EntityInfo, f func() BoundPolicy) error {
	var b BoundPolicy
	if err := recovered(info.Name, "Policy()", func() { b = f() }); err != nil {
		return err
	}
	info.policy = b.p
	if info.bidi && b.p != nil && (b.p.Read != nil || b.p.Write != nil) {
		return &SchemaError{Entity: info.Name, Reason: "the join entity of a bidirectional edge cannot have a Read or Write policy: " +
			"its conditions would show or delete one row of a link; check links in Policy.Insert and deletes in a hook"}
	}
	return nil
}

// EraseHooks calls f, the Descriptor's Hooks, and keeps its hooks in info. A nil
// hook is refused.
func EraseHooks(info *EntityInfo, f func() BoundHooks) error {
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

// SetBoundNil records that hook at1-1 is nil: at1 is 1 + its index, and 0 means none.
func SetBoundNil(b *BoundHooks, at1 int) { b.nilAt1 = at1 }

// AddBoundHook appends h to b's hooks.
func AddBoundHook(b *BoundHooks, h HookFn) { b.hs = append(b.hs, h) }

// NewBoundPolicy wraps p.
func NewBoundPolicy(p *EntityPolicy) BoundPolicy { return BoundPolicy{p} }

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
