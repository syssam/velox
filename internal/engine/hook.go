package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"unsafe"
)

// MutationState is a write in flight: what Mutation holds. Its DB is an
// any, a *DB that Mutation.DB converts: EntityInfo reaches mutation, and a *DB field
// made every Descriptor entity's package 14 KiB larger (compilecost_test.go).
type MutationState struct {
	DB       any              // the *DB the write runs on; set only when in has hooks
	In       *EntityInfo      // the write's entity; set only when it has hooks
	Op       Op               // set before the hooks run: Mutation.Op reads it, since an insert has no st
	St       *Statement       // the update or delete; nil for an insert
	Rows     []unsafe.Pointer // an insert's rows
	Affected int64            // the rows the write affected, for Mutation.Affected
	Where    []Predicate      // st.Where when next was called, for Mutation.Conds
	Own      bool             // the hook called SetAffected: it made the write itself
	ownSet   bool             // st.Set is the mutation's own array, cloned once
	Err      error            // a misuse recorded by Value, Set, Fill, Where or SetAffected
	Core     error            // the error next returned, to pass through unwrapped
	Called   bool             // next has been called
	Dry      bool             // under SQL(ctx): the core renders and does not send
	SQL      string
	Args     []any // what the core rendered under SQL(ctx)
}

// MutateFn is the non-generic form of a mutation hook's next step.
type MutateFn func(ctx context.Context, mu *MutationState) error

// HookFn is a Hook as the engine runs it, made once when the metadata is built.
type HookFn func(next MutateFn) MutateFn

// identical reports whether err is core itself. An error whose type cannot be
// compared is not, rather than a panic.
func identical(err, core error) bool {
	if core == nil {
		return false
	}
	t := reflect.TypeOf(err)
	return t == reflect.TypeOf(core) && t.Comparable() && err == core //nolint:errorlint // identity, not wrapping, is what this asks
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

// FilterFn is an Interceptor's Filter as the engine runs it.
type FilterFn func(ctx context.Context) (Predicate, error)

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
	var reads, queries []func(ctx context.Context) (Predicate, error)
	var writes []func(ctx context.Context, op Op) (Predicate, error)
	var inserts []func(ctx context.Context, p unsafe.Pointer) error
	for _, p := range ps {
		if p.Read != nil {
			reads = append(reads, p.Read)
		}
		if p.Query != nil {
			queries = append(queries, p.Query)
		}
		if p.Write != nil {
			writes = append(writes, p.Write)
		}
		if p.Insert != nil {
			inserts = append(inserts, p.Insert)
		}
	}
	out := &EntityPolicy{Read: andConds(reads), Query: andConds(queries), Write: andWriteConds(writes)}
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
		return andEach(len(fs), func(i int) (Predicate, error) { return fs[i](ctx) })
	}
}

// andWriteConds is andConds of Write conditions, which are given the
// write's kind. A nil in fs is skipped.
func andWriteConds(fs []func(ctx context.Context, op Op) (Predicate, error)) func(ctx context.Context, op Op) (Predicate, error) {
	fs = slices.DeleteFunc(slices.Clone(fs), func(f func(context.Context, Op) (Predicate, error)) bool { return f == nil })
	switch len(fs) {
	case 0:
		return nil
	case 1:
		return fs[0]
	}
	return func(ctx context.Context, op Op) (Predicate, error) {
		return andEach(len(fs), func(i int) (Predicate, error) { return fs[i](ctx, op) })
	}
}

// andEach ANDs what cond returns for 0 to n-1, as andConds says.
func andEach(n int, cond func(i int) (Predicate, error)) (Predicate, error) {
	var kids []Node
	for i := range n {
		p, err := cond(i)
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

// ErasePolicy calls f, which composes the Descriptor's Policy with E erased,
// and keeps its result in info. A panic in f is the entity's *SchemaError,
// and so is the reason f returns when it refuses them. On the join entity of a bidirectional edge (Descriptor.Bidi)
// it refuses a Read, Query or Write (Mutation sets Write).
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
	if info.bidi && p != nil && (p.reads() || p.Write != nil) {
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

// Addr returns v when it is a pointer or nil, and else a pointer to a copy
// of v, whose method set holds the methods of both receivers: a schema's or
// a mixin's behaviour method may have either.
func Addr(v any) any {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() || rv.Kind() == reflect.Pointer {
		return v
	}
	p := reflect.New(rv.Type())
	p.Elem().Set(rv)
	return p.Interface()
}

// WrongBehaviour is the error of x, a schema or a mixin, that has a method
// of the name of a behaviour kind (Policy, Hooks or Interceptors) whose
// signature is not want, so that velox would not call it; nil when x has
// no method of that name. The names are constants, so that the linker can
// still drop the methods nobody calls.
func WrongBehaviour(info *EntityInfo, x any, kind, want string) error {
	t := reflect.TypeOf(x)
	var ok bool
	switch kind {
	case "Policy":
		_, ok = t.MethodByName("Policy")
	case "Hooks":
		_, ok = t.MethodByName("Hooks")
	default:
		_, ok = t.MethodByName("Interceptors")
	}
	if !ok {
		return nil
	}
	return &SchemaError{Entity: info.Name, Reason: fmt.Sprintf("%v's %s method is not %s: velox would not call it", t, kind, want)}
}

// NoBehaviour is the error of a Descriptor whose Schema s, and the mixins
// it lists, declare no Policy, Hooks or Interceptors method: a Schema that
// is set by mistake, such as a database schema's name.
func NoBehaviour(info *EntityInfo, s any) error {
	return &SchemaError{Entity: info.Name, Reason: fmt.Sprintf("Descriptor.Schema, a %T, and its mixins declare no Policy, Hooks or Interceptors method; "+
		"a database schema's name is TableSchema", s)}
}

// Recover calls f, recovering a panic as the entity's *SchemaError, which
// names what f called.
func Recover(info *EntityInfo, what string, f func()) error {
	return recovered(info.Name, what, f)
}

// recovered calls f, recovering a panic as the entity's *SchemaError.
func recovered(entity, what string, f func()) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = &SchemaError{Entity: entity, Reason: fmt.Sprintf("%s panicked: %v", what, p)}
		}
	}()
	f()
	return nil
}

// Mutate runs in's hooks around core, which runs on db: the hooks reach it
// as Mutation.DB. With no hooks it calls core alone. The writes call core
// directly when in has no hooks, so that none of them pays for the closure
// and the mutation this takes, or for setting mu.db.
func Mutate(ctx context.Context, db *DB, in *EntityInfo, mu *MutationState, core MutateFn) error {
	if !db.HasHooks(in) {
		return core(ctx, mu)
	}
	mu.DB, mu.In = db, in
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
	for _, h := range slices.Backward(db.hooksOf(in)) {
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
