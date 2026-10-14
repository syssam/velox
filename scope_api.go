package velox

import (
	"context"
	"errors"
	"slices"
)

// tenantKey is the context key of a tenantScope: WithTenant and AllTenants
// share it, so that the later of the two on a context wins.
type tenantKey struct{}

// tenantScope is the tenant state a context carries: an ID, or the reason
// for reading every tenant.
type tenantScope struct {
	id  any
	all string
}

// policyKey is the context key of a SkipPolicy reason.
type policyKey struct{}

// WithTenant returns ctx scoped to tenant id: statements on a tenant-scoped
// entity read and change only that tenant's rows, and inserts fill or check
// the tenant column. id's dynamic type must be the tenant column's Go type
// exactly; a nil id scopes nothing, so such statements fail with ErrNoTenant.
// It replaces an earlier WithTenant or AllTenants on ctx.
func WithTenant(ctx context.Context, id any) context.Context {
	return context.WithValue(ctx, tenantKey{}, tenantScope{id: id})
}

// AllTenants returns ctx unscoped: statements on a tenant-scoped entity reach
// every tenant's rows. The reason is required and is shown to hooks as
// Statement.TenantBypass; with an empty reason AllTenants returns ctx
// unchanged, so a statement without a tenant still fails. It replaces an
// earlier WithTenant or AllTenants on ctx.
func AllTenants(ctx context.Context, reason string) context.Context {
	if reason == "" {
		return ctx
	}
	return context.WithValue(ctx, tenantKey{}, tenantScope{all: reason})
}

// SkipPolicy returns ctx with every entity's policy switched off. The reason
// is required and is shown to hooks as Statement.PolicyBypass; with an empty
// reason SkipPolicy returns ctx unchanged. It does not lift tenant scoping:
// see AllTenants.
func SkipPolicy(ctx context.Context, reason string) context.Context {
	if reason == "" {
		return ctx
	}
	return context.WithValue(ctx, policyKey{}, reason)
}

// tenantOf returns the context's tenant state: the tenant's id (nil when
// none), and the AllTenants reason ("" when none). At most one is set.
func tenantOf(ctx context.Context) (id any, all string) {
	s, _ := ctx.Value(tenantKey{}).(tenantScope)
	return s.id, s.all
}

// policyBypass returns the context's SkipPolicy reason; "" when none.
func policyBypass(ctx context.Context) string {
	r, _ := ctx.Value(policyKey{}).(string)
	return r
}

// Policy is entity E's row-level policy, declared once in its Schema method
// with Schema.Policy. Any field may be nil. A predicate function's zero
// Pred[E]{} restricts nothing; any error it returns denies the statement.
type Policy[E any] struct {
	// Read is ANDed into every SELECT, Count, With load and Has/HasWhere
	// subquery on the entity, and into every UPDATE, DELETE and Save.
	Read func(ctx context.Context) (Pred[E], error)
	// Write is ANDed into every UPDATE, DELETE and Save, with Read.
	Write func(ctx context.Context) (Pred[E], error)
	// Insert checks every inserted or upserted row, after the BeforeInsert
	// hooks and the tenant fill.
	Insert func(ctx context.Context, x *E) error
}

// PolicyError is a statement a policy denied; Err is the policy's error.
// Every PolicyError matches ErrDenied.
type PolicyError struct {
	Entity string // the Go type, e.g. "billing.Invoice"
	Op     Op
	Err    error
}

func (e *PolicyError) Error() string {
	s := "velox: " + e.Entity + ": " + e.Op.String() + " denied by policy"
	if e.Err == nil {
		return s
	}
	return s + ": " + e.Err.Error()
}

// Unwrap returns the policy's error.
func (e *PolicyError) Unwrap() error { return e.Err }

// Is makes every PolicyError match ErrDenied.
func (e *PolicyError) Is(target error) bool { return target == ErrDenied }

// Deny returns an error for a policy function to deny a statement with: its
// text is reason, and it matches ErrDenied.
func Deny(reason string) error { return denial(reason) }

// denial is the error Deny returns.
type denial string

func (d denial) Error() string { return string(d) }

// Is makes a denial match ErrDenied.
func (d denial) Is(target error) bool { return target == ErrDenied }

// Mutation is the UPDATE or DELETE that a BeforeUpdate or BeforeDelete hook
// of entity E sees and may change. A misuse (Set on a delete, a zero or
// invalid condition) is recorded and fails the statement once the hook
// returns.
type Mutation[E any] struct {
	st  *Statement
	err error
}

// Op returns the statement's kind, OpUpdate or OpDelete.
func (m *Mutation[E]) Op[_ Lazy[E]]() Op { return m.st.Op }

// Value returns the value the statement assigns to column c, and whether it
// assigns one; an Add or Sub is not an assignment of a value.
func (m *Mutation[E]) Value[_ Lazy[E]](c Column[E]) (any, bool) {
	if c == nil {
		return nil, false
	}
	return assigned(m.st, int(c.column(nil)))
}

// Set adds assignments to an update; each replaces the statement's
// assignment of the same column, if there is one. Set on a delete is an
// error.
func (m *Mutation[E]) Set[_ Lazy[E]](ss ...Setter[E]) {
	for _, s := range ss {
		mutateSet(m.st, &m.err, s.a)
	}
}

// Where narrows the rows the statement changes: all of the conditions must
// hold, with the statement's own.
func (m *Mutation[E]) Where[_ Lazy[E]](ps ...Pred[E]) {
	for _, p := range ps {
		mutateWhere(m.st, &m.err, Predicate(p))
	}
}

// assigned returns the value st assigns to column col with AssignSet.
func assigned(st *Statement, col int) (any, bool) {
	for _, a := range st.Set {
		if a.Column == col && a.Op == AssignSet {
			return a.Value, true
		}
	}
	return nil, false
}

// mutateSet replaces st's assignment of a.Column with a, or appends a. It
// records an error in *errp, keeping the first, when st is a delete.
func mutateSet(st *Statement, errp *error, a Assignment) {
	if st.Op == OpDelete {
		if *errp == nil {
			*errp = errors.New("a delete assigns no columns: Set is for updates")
		}
		return
	}
	for i := range st.Set {
		if st.Set[i].Column == a.Column {
			// Copied before the change: the elements may be the query's own.
			st.Set = slices.Clone(st.Set)
			st.Set[i] = a
			return
		}
	}
	st.Set = append(slices.Clip(st.Set), a)
}

// mutateWhere appends p to st's conditions, or records its error in *errp,
// keeping the first, when it is or holds the zero Predicate or is invalid.
func mutateWhere(st *Statement, errp *error, p Predicate) {
	var err error
	if hasZero(p.n) {
		err = errors.New("a condition is the zero Predicate; make conditions with column handles, And, Or and Not")
	} else {
		err = predErr(p.n)
	}
	if err != nil {
		if *errp == nil {
			*errp = err
		}
		return
	}
	st.Where = append(slices.Clip(st.Where), p)
}
