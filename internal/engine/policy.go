package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// policyKey is the context key of a WithoutPolicy reason.
type policyKey struct{}

// WithoutPolicy returns ctx with every entity's policy switched off. The reason
// is required and is shown to hooks as Statement.PolicyBypass; with a reason
// that is empty or only white space WithoutPolicy returns ctx unchanged.
func WithoutPolicy(ctx context.Context, reason string) context.Context {
	if NoReason(reason) {
		return ctx
	}
	return context.WithValue(ctx, policyKey{}, reason)
}

// scopeKey is the context key of the scope values a context carries.
type scopeKey struct{}

// scopeValues are the values policies read from a context, by name: kept as
// the text RowScope.Values shows, "name=%T:%v" joined with ";" in the order of
// the names, so that it compares with ==.
type scopeValues []scopeValue

type scopeValue struct{ name, text string }

// WithScopeValue returns ctx recording that the policies read value under name,
// so that RowScopeFromContext shows it and a batcher keeps callers with
// different values apart. A later call with the same name replaces it. The
// scope package calls it; a policy that reads a context value of its own and
// is used with a batcher should too.
func WithScopeValue(ctx context.Context, name string, value any) context.Context {
	old, _ := ctx.Value(scopeKey{}).(scopeValues)
	text := fmt.Sprintf("%T:%v", value, value)
	vs := make(scopeValues, 0, len(old)+1)
	inserted := false
	for _, o := range old {
		switch {
		case o.name == name:
			vs, inserted = append(vs, scopeValue{name, text}), true
		case !inserted && o.name > name:
			vs, inserted = append(vs, scopeValue{name, text}, o), true
		default:
			vs = append(vs, o)
		}
	}
	if !inserted {
		vs = append(vs, scopeValue{name, text})
	}
	return context.WithValue(ctx, scopeKey{}, vs)
}

// ScopeValuesText is the values WithScopeValue recorded in ctx, as RowScope.Values
// shows them; "" when none.
func ScopeValuesText(ctx context.Context) string {
	vs, _ := ctx.Value(scopeKey{}).(scopeValues)
	var b strings.Builder
	for i, v := range vs {
		if i > 0 {
			b.WriteByte(';')
		}
		b.WriteString(v.name)
		b.WriteByte('=')
		b.WriteString(v.text)
	}
	return b.String()
}

// RowScopeFromContext returns ctx's row scope.
func RowScopeFromContext(ctx context.Context) RowScope {
	rc, _ := ctx.Value(routeKey{}).(routeCtx)
	return RowScope{Values: ScopeValuesText(ctx), PolicyBypass: PolicyBypassReason(ctx), Shard: rc.shard, HasShard: rc.onShard}
}

// NoReason reports whether reason, a bypass's (WithoutPolicy, Raw, Exec), says nothing: it is empty or only white space.
func NoReason(reason string) bool { return strings.TrimSpace(reason) == "" }

// dryRunKey is the context key the SQL(ctx) methods mark their context with.
type dryRunKey struct{}

// IsDryRun reports whether ctx is that of a SQL(ctx) method: the statement
// being built will not be sent. The SQL(ctx) methods mark the context before
// anything runs, so everything handed that context sees it: the entity's
// hooks (Hooks), the Policy functions and the Rewrite hooks. A Rewrite hook
// can also read it from Statement.DryRun.
//
// A write made under such a context is refused: Exec of an Insert, InsertAll,
// Update or Delete, Save, an upsert, DB.Exec (raw SQL) and Tx fail with
// ErrDryRun before anything is sent, on any *DB, so a hook's audit write through
// Mutation.DB does not happen and needs no check. Reads run as normal.
func IsDryRun(ctx context.Context) bool {
	dry, _ := ctx.Value(dryRunKey{}).(bool)
	return dry
}

// DryRunContext returns ctx marked for IsDryRun.
func DryRunContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, dryRunKey{}, true)
}

// PolicyBypassReason returns the context's WithoutPolicy reason; "" when none.
func PolicyBypassReason(ctx context.Context) string {
	r, _ := ctx.Value(policyKey{}).(string)
	return r
}

// Deny returns an error for a policy function to deny a statement with: its
// text is reason, and it matches ErrDenied.
func Deny(reason string) error { return denial(reason) }

// denial is the error Deny returns.
type denial string

func (d denial) Error() string { return string(d) }

// Is makes a denial match ErrDenied.
func (d denial) Is(target error) bool { return target == ErrDenied }

// ErrInsertMutation is the misuse of Where on an insert.
var ErrInsertMutation = errors.New("Where is for updates and deletes; an insert's rows are set with Set and Fill")

// SetAffected is SetAffected's work, not generic.
func (mu *MutationState) SetAffected(n int64) {
	switch {
	case mu.St == nil:
		mu.Record(errInsertAffected)
	case mu.Called:
		mu.Record(errOwnAndNext)
	case n < -1:
		mu.Record(errNegativeAffected)
	default:
		mu.Affected, mu.Own = n, true
	}
}

// errInsertAffected is the misuse of SetAffected on an insert.
var errInsertAffected = errors.New("SetAffected is for updates and deletes; an insert's count is its rows")

// errNegativeAffected is the misuse of SetAffected with a negative count
// other than -1, the unknown count.
var errNegativeAffected = errors.New("SetAffected of a negative count other than -1")

// Record records err as the mutation's misuse, keeping the first.
func (mu *MutationState) Record(err error) {
	if mu.Err == nil {
		mu.Err = err
	}
}

// Assigned returns the value st assigns to column col with AssignSet.
func Assigned(st *Statement, col int) (any, bool) {
	for _, a := range st.Set {
		if a.Column == col && a.Op == AssignSet {
			return a.Value, true
		}
	}
	return nil, false
}

// MutateSet replaces the assignment of a.Column in mu's statement with a, or
// appends a. The first change copies st.Set, whose elements may be the
// query's own, and sets mu.ownSet; later ones write in place. It records an
// error, keeping the first, when the statement is a delete.
func MutateSet(mu *MutationState, a Assignment) {
	st := mu.St
	if st.Op == OpDelete {
		mu.Record(errors.New("a delete assigns no columns: Set is for updates"))
		return
	}
	i := slices.IndexFunc(st.Set, func(s Assignment) bool { return s.Column == a.Column })
	switch {
	case i >= 0 && !mu.ownSet:
		st.Set = slices.Clone(st.Set)
	case i < 0 && !mu.ownSet:
		st.Set = slices.Clip(st.Set) // append copies
	}
	mu.ownSet = true
	if i >= 0 {
		st.Set[i] = a
	} else {
		st.Set = append(st.Set, a)
	}
}

// MutateWhere appends p to st's conditions, or records its error in *errp,
// keeping the first, when it is or holds the zero Predicate or is invalid.
func MutateWhere(st *Statement, errp *error, p Predicate) {
	var err error
	if hasZero(p.n) {
		err = errors.New("a condition is the zero Predicate; make conditions with column handles, And, Or and Not")
	} else {
		err = PredErr(p.n)
	}
	if err != nil {
		if *errp == nil {
			*errp = err
		}
		return
	}
	st.Where = append(slices.Clip(st.Where), p)
}
