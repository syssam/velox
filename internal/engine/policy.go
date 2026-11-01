package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// tenantKey is the context key of a tenantScope: WithTenant and WithAllTenants
// share it, so that the later of the two on a context wins.
type tenantKey struct{}

// tenantScope is the tenant state a context carries: an ID, or the reason
// for reading every tenant.
type tenantScope struct {
	id  any
	all string
}

// policyKey is the context key of a WithoutPolicy reason.
type policyKey struct{}

// WithTenant returns ctx scoped to tenant id: statements on a tenant-scoped
// entity read and change only that tenant's rows, and inserts fill or check
// the tenant column. id is converted to the tenant column's Go type when it
// is of a predeclared integer type (an untyped constant is an int) and the
// column's type, predeclared or named such as an OrgID int64, is an integer
// type whose range holds it; or when it is a string and the column's kind is
// string. An id of a named type is never converted: it must be the column's
// type, so a UserID never serves an OrgID column. Any other id fails the
// statement with ErrTenantMismatch. A nil id, or the type's zero value (what
// parsing a missing claim gives), scopes nothing: such statements fail with
// ErrNoTenant. It replaces an earlier WithTenant or WithAllTenants on ctx.
func WithTenant(ctx context.Context, id any) context.Context {
	return context.WithValue(ctx, tenantKey{}, tenantScope{id: id})
}

// WithAllTenants returns ctx unscoped: statements on a tenant-scoped entity
// reach every tenant's rows. The reason is required and is shown to hooks as
// Statement.TenantBypass; with a reason that is empty or only white space
// WithAllTenants returns ctx unchanged, so a statement without a tenant still
// fails. It replaces an earlier WithTenant or WithAllTenants on ctx.
func WithAllTenants(ctx context.Context, reason string) context.Context {
	if NoReason(reason) {
		return ctx
	}
	return context.WithValue(ctx, tenantKey{}, tenantScope{all: reason})
}

// WithoutPolicy returns ctx with every entity's policy switched off. The reason
// is required and is shown to hooks as Statement.PolicyBypass; with a reason
// that is empty or only white space WithoutPolicy returns ctx unchanged. It
// does not lift tenant scoping: see WithAllTenants.
func WithoutPolicy(ctx context.Context, reason string) context.Context {
	if NoReason(reason) {
		return ctx
	}
	return context.WithValue(ctx, policyKey{}, reason)
}

// RowScopeFromContext returns ctx's row scope.
func RowScopeFromContext(ctx context.Context) RowScope {
	id, all := TenantFrom(ctx)
	rc, _ := ctx.Value(routeKey{}).(routeCtx)
	return RowScope{Tenant: id, TenantBypass: all, PolicyBypass: PolicyBypassReason(ctx), Shard: rc.shard, HasShard: rc.onShard}
}

// NoReason reports whether reason, a bypass's (WithAllTenants, WithoutPolicy,
// Raw, Exec), says nothing: it is empty or only white space.
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

// TenantFrom returns the context's tenant state: the tenant's id (nil when
// none), and the WithAllTenants reason ("" when none). At most one is set.
func TenantFrom(ctx context.Context) (id any, all string) {
	s, _ := ctx.Value(tenantKey{}).(tenantScope)
	return s.id, s.all
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

// ErrInsertMutation is the misuse of ValueIn, Set or Where on an insert.
var ErrInsertMutation = errors.New("Set and Where are for updates and deletes; change Rows")

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

// WrongType records that column col is assigned got, which is not of the
// type want points to. It is not generic, so that fmt and reflect are not
// compiled into every package that calls ValueIn.
func (mu *MutationState) WrongType(col int, got, want any) {
	name := fmt.Sprintf("%d", col)
	if in := mu.St.Entity; in != nil && col < len(in.Columns) {
		name = strconv.Quote(in.Columns[col].Name)
	}
	mu.Record(fmt.Errorf("column %s is assigned a value of type %T, not %v", name, got, reflect.TypeOf(want).Elem()))
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
