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
// exactly. A nil id, or the type's zero value (what parsing a missing claim
// gives), scopes nothing: such statements fail with ErrNoTenant. It replaces
// an earlier WithTenant or AllTenants on ctx.
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

// dryRunKey is the context key the SQL(ctx) methods mark their context with.
type dryRunKey struct{}

// IsDryRun reports whether ctx is that of a SQL(ctx) method: the statement
// being built will not be sent. The methods mark the context before anything
// runs, so every hook and policy function sees it: entity hooks (Hooks), and
// the old BeforeInsert, BeforeUpdate and BeforeDelete, which are given no
// Statement, read it here, and a Rewrite hook may read it here or in
// Statement.DryRun.
func IsDryRun(ctx context.Context) bool {
	dry, _ := ctx.Value(dryRunKey{}).(bool)
	return dry
}

// dryRunContext returns ctx marked for IsDryRun.
func dryRunContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, dryRunKey{}, true)
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

// Policy is entity E's row-level policy, declared once: by a method on the
// entity's pointer type,
//
//	func (*E) Policy() velox.Policy[E]
//
// or, on the old path, in its Schema method with Schema.Policy. velox-gen
// binds the method in the generated metadata (Desc.Policy calls
// BindPolicy on it): a method with another signature does not compile, and
// one the metadata does not bind is refused at the entity's first use. velox calls it once, when it
// builds the metadata, on a nil *E, so the method must not read its receiver.
// Nor may its body, outside the functions it returns, read the entity's own
// metadata (a query on it, say): that waits on the build in progress and
// deadlocks. A Has or HasWhere reads none, so the body may build them, and the
// functions it returns may share them: a relation resolves when a statement
// holding the condition runs.
//
// Any field may be nil. A predicate function's zero Pred[E]{} restricts
// nothing; any error it returns denies the statement.
type Policy[E any] struct {
	// Read is ANDed into every SELECT, Count, With load and Has/HasWhere
	// subquery on the entity, into every UPDATE, DELETE and Save, and into
	// the update branch of an upsert, on the existing row.
	Read func(ctx context.Context) (Pred[E], error)
	// Write is ANDed into every UPDATE, DELETE and Save, and into an upsert's
	// update branch, with Read. It decides which existing rows may change; it
	// does not check the new values (there is no WITH CHECK), so a writer may
	// set a column the policy reads, such as an owner. Only the tenant column
	// cannot be changed.
	Write func(ctx context.Context) (Pred[E], error)
	// Insert checks every inserted or upserted row, after the entity's
	// hooks, the defaults, the tenant fill, the old BeforeInsert hooks and
	// the validators.
	Insert func(ctx context.Context, x *E) error
}

// PolicyError is a statement a policy denied; Err is the policy's error.
// Every PolicyError matches ErrDenied.
type PolicyError struct {
	Entity string // the Go type, e.g. "billing.Invoice"
	// Op is the kind of statement denied: the statement's own, or, for a
	// denial of the target of an EXISTS (a Has or HasWhere, in the statement
	// or in a policy's condition), OpSelect, since the EXISTS reads its target
	// whatever the statement does. An upsert's update branch is OpUpdate.
	Op  Op
	Err error
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

// Mutation is the write that an entity's hooks (Hooks) see and may change,
// and the UPDATE or DELETE that an old BeforeUpdate or BeforeDelete hook
// sees. A misuse (Set on a delete, a zero or invalid condition, Value, Set or
// Where on an insert) is recorded and fails the write: before next sends
// it, or once the hook returns.
type Mutation[E any] mutation

// errInsertMutation is the misuse of Value, Set or Where on an insert.
var errInsertMutation = errors.New("Set and Where are for updates and deletes; change Rows")

// Op returns the write's kind: OpInsert, OpUpdate or OpDelete.
func (m *Mutation[E]) Op[_ Lazy[E]]() Op {
	if m.op != 0 {
		return m.op
	}
	return m.st.Op
}

// Rows returns an insert's rows, which a hook may change before next; nil
// for an update or delete.
func (m *Mutation[E]) Rows[_ Lazy[E]]() []*E {
	if m.rows == nil {
		return nil
	}
	out := make([]*E, len(m.rows))
	for i, p := range m.rows {
		out[i] = (*E)(p)
	}
	return out
}

// Result returns what the write did, once next has returned. Only Affected
// is meaningful: the rows inserted, matched or deleted. Rows is always 0.
// Unlike a Statement's Result, which an Around hook reads, Affected is never
// -1: it is 0 before next returns, after next returned an error (an insert
// sets it only when every statement succeeded and no upsert was denied), and
// under SQL(ctx), which sends nothing.
func (m *Mutation[E]) Result[_ Lazy[E]]() Result { return m.res }

// Value returns the value the statement assigns to column c, and whether it
// assigns one; an Add or Sub is not an assignment of a value. On an insert it
// records an error: read the Rows.
func (m *Mutation[E]) Value[_ Lazy[E]](c Column[E]) (any, bool) {
	if m.st == nil {
		m.misuse()
		return nil, false
	}
	if c == nil {
		return nil, false
	}
	return assigned(m.st, int(c.column(nil)))
}

// Set adds assignments to an update; each replaces the statement's
// assignment of the same column, if there is one. Set on a delete or an
// insert is an error.
func (m *Mutation[E]) Set[_ Lazy[E]](ss ...Setter[E]) {
	if m.st == nil {
		m.misuse()
		return
	}
	for _, s := range ss {
		mutateSet(m.st, &m.err, s.a)
	}
}

// Where narrows the rows the statement changes: all of the conditions must
// hold, with the statement's own. Where on an insert is an error.
func (m *Mutation[E]) Where[_ Lazy[E]](ps ...Pred[E]) {
	if m.st == nil {
		m.misuse()
		return
	}
	for _, p := range ps {
		mutateWhere(m.st, &m.err, Predicate(p))
	}
}

// misuse records the error of Value, Set or Where on an insert, keeping the
// first.
func (m *Mutation[E]) misuse[_ Lazy[E]]() {
	if m.err == nil {
		m.err = errInsertMutation
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
