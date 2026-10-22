package velox

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
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
// Statement.TenantBypass; with a reason that is empty or only white space
// AllTenants returns ctx unchanged, so a statement without a tenant still
// fails. It replaces an earlier WithTenant or AllTenants on ctx.
func AllTenants(ctx context.Context, reason string) context.Context {
	if noReason(reason) {
		return ctx
	}
	return context.WithValue(ctx, tenantKey{}, tenantScope{all: reason})
}

// SkipPolicy returns ctx with every entity's policy switched off. The reason
// is required and is shown to hooks as Statement.PolicyBypass; with a reason
// that is empty or only white space SkipPolicy returns ctx unchanged. It does
// not lift tenant scoping: see AllTenants.
func SkipPolicy(ctx context.Context, reason string) context.Context {
	if noReason(reason) {
		return ctx
	}
	return context.WithValue(ctx, policyKey{}, reason)
}

// RowScope is what a context scopes statements by: WithTenant's id (nil
// when none), the reasons of AllTenants and SkipPolicy ("" when none), and
// OnShard's shard, which holds other rows than another shard. A batcher
// that reads for several callers in one statement compares their scopes,
// so that callers of different scopes never share a read.
//
// RowScopes are compared with ==, which panics when Tenant holds a value
// that cannot be compared. A tenant column's type is comparable (its
// metadata is refused otherwise), but WithTenant takes any id, and an id of
// another type, which a statement refuses, may not be: a caller that
// compares guards against it with reflect.ValueOf(Tenant).Comparable(),
// which checks the value, since a struct or an array of a comparable type
// may hold an interface whose value is a slice or a map.
type RowScope struct {
	Tenant     any
	AllTenants string
	SkipPolicy string
	// Shard is OnShard's name, and OnShard whether OnShard was called: a
	// shard named "" is told from none.
	Shard   string
	OnShard bool
}

// RowScopeOf returns ctx's row scope.
func RowScopeOf(ctx context.Context) RowScope {
	id, all := tenantOf(ctx)
	rc, _ := ctx.Value(routeKey{}).(routeCtx)
	return RowScope{Tenant: id, AllTenants: all, SkipPolicy: policyBypass(ctx), Shard: rc.shard, OnShard: rc.onShard}
}

// noReason reports whether reason, a bypass's (AllTenants, SkipPolicy, Raw,
// Exec), says nothing: it is empty or only white space.
func noReason(reason string) bool { return strings.TrimSpace(reason) == "" }

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
// velox-gen binds the method in the generated metadata (Desc.Policy calls
// BindPolicy on it): a method with another signature does not compile, and
// one the metadata does not bind is refused at the entity's first use. velox calls it once, when it
// builds the metadata, on a nil *E, so the method must not read its receiver.
// Nor may its body, outside the functions it returns, read the entity's own
// metadata (a query on it, say): that waits on the build in progress and
// deadlocks. A Has or HasWhere reads none, so the body may build them, and the
// functions it returns may share them: a relation resolves when a statement
// holding the condition runs.
//
// Any field may be nil. A predicate function returns Allow[E]() for no
// restriction; its zero Pred[E]{} fails the statement, naming the entity, so
// that a code path that forgot to return a condition never lets a caller
// through. Any error it returns denies the statement.
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
	// hooks, the defaults, the tenant fill and the
	// validators.
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

// Mutation is the write that an entity's hooks (Hooks) see and may change.
// A misuse (Set on a delete, a zero or invalid condition, a handle's
// ValueIn, Set, Where or SetAffected on an insert, ValueIn of a value of
// another type, SetAffected with next) is recorded and fails the write: before next sends
// it, or once the hook returns. A hook reads an assignment with the column
// handle's ValueIn, the conditions with Conds, and once next has returned
// the rows written with Affected.
//
// A hook may make the write itself instead of calling next. A soft delete
// is a delete hook that updates the rows the delete would remove, and
// reports the update's count with SetAffected, which the delete's Exec then
// returns:
//
//	q := m.DB().Update[Task]().Set(Tasks.DeletedAt.Set(now))
//	if cs := m.Conds(); len(cs) > 0 {
//		q = q.Where(cs...)
//	} else {
//		q = q.All()
//	}
//	n, err := q.Exec(ctx)
//	m.SetAffected(n)
//	return err
//
// The update adds the tenant's and the policy's conditions itself. Under
// SQL(ctx) such a delete fails with ErrDryRun: the hook's update is a write,
// and there is no DELETE to render.
type Mutation[E any] struct{ mu mutation }

// errInsertMutation is the misuse of ValueIn, Set or Where on an insert.
var errInsertMutation = errors.New("Set and Where are for updates and deletes; change Rows")

// Op returns the write's kind: OpInsert, OpUpdate or OpDelete.
func (m *Mutation[E]) Op[_ Lazy[E]]() Op {
	if m.mu.op != 0 {
		return m.mu.op
	}
	return m.mu.st.Op
}

// Rows returns an insert's rows, which a hook may change before next; nil
// for an update or delete.
func (m *Mutation[E]) Rows[_ Lazy[E]]() []*E {
	if m.mu.rows == nil {
		return nil
	}
	out := make([]*E, len(m.mu.rows))
	for i, p := range m.mu.rows {
		out[i] = (*E)(p)
	}
	return out
}

// DB returns the DB the write runs on: the transaction's, inside db.Tx, or
// else the database's. A hook that reads or writes other rows uses it, so
// that its work commits or rolls back with the write:
//
//	err := m.DB().Insert(&audit).Exec(ctx)
//
// It is set only for the writes of entities that have hooks, which are the
// only ones that see a Mutation.
func (m *Mutation[E]) DB[_ Lazy[E]]() *DB {
	db, _ := m.mu.db.(*DB)
	return db
}

// Conds returns the conditions of an update or a delete as the caller and
// the hooks gave them: the caller's Where and those the hooks before this one
// added, in order, as copies (changing the slice changes nothing). Once the
// innermost hook has called next, it returns them as they were then, so an
// outer hook whose next has returned also sees the conditions the hooks
// inside it added. It never holds the tenant's, the policy's or a Rewrite
// hook's conditions, which next adds. It is empty only when the statement
// said All, and nil for an insert. Mutation shows a soft delete built on it.
func (m *Mutation[E]) Conds[_ Lazy[E]]() []Pred[E] {
	if m.mu.st == nil {
		return nil
	}
	w := m.mu.st.Where
	if m.mu.called {
		w = m.mu.where
	}
	out := make([]Pred[E], len(w))
	for i, p := range w {
		out[i] = Pred[E](p)
	}
	return out
}

// Affected returns the rows the write inserted, matched or deleted, once
// next has returned, or the count the hook gave SetAffected. It is 0 before
// then, after next returned an error (an insert sets it only when every
// statement succeeded and no upsert was denied), under SQL(ctx), which sends
// nothing, and when a hook skipped next without calling SetAffected.
func (m *Mutation[E]) Affected[_ Lazy[E]]() int64 { return m.mu.affected }

// SetAffected is for a hook that makes an update or a delete itself instead
// of calling next, such as a soft delete (see Mutation): n is the rows its
// own write affected, which the update's or delete's Exec returns. A Save's
// Exec returns no count and stores nothing in its entity, as velox wrote
// nothing. Calling it and next both, in either order, with a negative n, or
// on an insert, whose count is its rows, is a misuse.
func (m *Mutation[E]) SetAffected[_ Lazy[E]](n int64) {
	m.mu.setAffected(n)
}

// setAffected is SetAffected's work, not generic.
func (mu *mutation) setAffected(n int64) {
	switch {
	case mu.st == nil:
		mu.record(errInsertAffected)
	case mu.called:
		mu.record(errOwnAndNext)
	case n < 0:
		mu.record(errNegativeAffected)
	default:
		mu.affected, mu.own = n, true
	}
}

// errInsertAffected is the misuse of SetAffected on an insert.
var errInsertAffected = errors.New("SetAffected is for updates and deletes; an insert's count is its rows")

// errNegativeAffected is the misuse of SetAffected with a negative count.
var errNegativeAffected = errors.New("SetAffected of a negative count")

// Set adds assignments to an update; each replaces the statement's
// assignment of the same column, if there is one. Set on a delete or an
// insert is an error.
func (m *Mutation[E]) Set[_ Lazy[E]](ss ...Setter[E]) {
	if m.mu.st == nil {
		m.misuse()
		return
	}
	for _, s := range ss {
		mutateSet(m.mu.st, &m.mu.err, s.a)
	}
}

// Where narrows the rows the statement changes: all of the conditions must
// hold, with the statement's own. Where on an insert is an error.
func (m *Mutation[E]) Where[_ Lazy[E]](ps ...Pred[E]) {
	if m.mu.st == nil {
		m.misuse()
		return
	}
	for _, p := range ps {
		mutateWhere(m.mu.st, &m.mu.err, Predicate(p))
	}
}

// misuse records the error of ValueIn, Set or Where on an insert, keeping
// the first.
func (m *Mutation[E]) misuse[_ Lazy[E]]() { m.mu.record(errInsertMutation) }

// record records err as the mutation's misuse, keeping the first.
func (mu *mutation) record(err error) {
	if mu.err == nil {
		mu.err = err
	}
}

// valueIn is a handle's ValueIn: the value m's update assigns to column col
// with AssignSet, as a V. On an insert it records the misuse; a value of
// another type (an untyped UpdateDefault, a Rewrite hook's) it records as
// one too, and reports no value.
func valueIn[E, V any](m *Mutation[E], col int32) (V, bool) {
	var zero V
	if m.mu.st == nil {
		m.misuse()
		return zero, false
	}
	a, ok := assigned(m.mu.st, int(col))
	if !ok {
		return zero, false
	}
	v, ok := a.(V)
	if !ok {
		m.mu.wrongType(int(col), a, (*V)(nil))
		return zero, false
	}
	return v, true
}

// nullValueIn is a nullable handle's ValueIn: nil for SetNull, and else a
// pointer to a copy of the value, which Set gives as a V and Save as a *V.
// Any other value it records as valueIn does.
func nullValueIn[E, V any](m *Mutation[E], col int32) (*V, bool) {
	if m.mu.st == nil {
		m.misuse()
		return nil, false
	}
	a, ok := assigned(m.mu.st, int(col))
	if !ok {
		return nil, false
	}
	switch v := a.(type) {
	case nil:
		return nil, true
	case V:
		return &v, true
	case *V:
		if v == nil {
			return nil, true
		}
		c := *v
		return &c, true
	}
	m.mu.wrongType(int(col), a, (*V)(nil))
	return nil, false
}

// wrongType records that column col is assigned got, which is not of the
// type want points to. It is not generic, so that fmt and reflect are not
// compiled into every package that calls ValueIn.
func (mu *mutation) wrongType(col int, got, want any) {
	name := fmt.Sprintf("%d", col)
	if in := mu.st.Entity; in != nil && col < len(in.Columns) {
		name = strconv.Quote(in.Columns[col].Name)
	}
	mu.record(fmt.Errorf("column %s is assigned a value of type %T, not %v", name, got, reflect.TypeOf(want).Elem()))
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
