package velox

import (
	"context"
	"fmt"
	"reflect"
	"strings"
)

// scope applies the context's tenant and the entities' policies to a
// statement (policy design §2.1, §2.2). On a tenant-scoped entity it adds the
// condition tenant = id, or fails the statement with ErrNoTenant when the
// context has neither WithTenant nor AllTenants. Unless the context says
// SkipPolicy, a SELECT gets the entity's Read policy and an UPDATE or DELETE
// its Read and Write policies, after the tenant. Every EXISTS gets its
// target's tenant and Read policy the same way. scope runs after the guard
// and the Rewrite hooks, so neither sees what it adds and no hook can remove
// it.
//
// st does not escape: scope passes it to no function value, and policy
// functions are given only ctx, so plain's statement stays on the stack.
func (db *DB) scope(ctx context.Context, st *Statement) error {
	switch st.Op {
	case OpSelect, OpUpdate, OpDelete:
	default:
		return nil
	}
	in := st.Entity
	sc := scoper{ctx: ctx, entity: in.Name}
	tenant, err := sc.tenantOf(in)
	if err != nil {
		return err
	}
	if tenant {
		if err := checkTenantAssignments(in, st.Set, sc.id); err != nil {
			return err
		}
	}
	// The entity's own policy first, so that its denial is reported as the
	// statement's, and an EXISTS on the same entity reuses its Read.
	var read, write Predicate
	if pol := in.policy; pol != nil {
		if pol.read != nil && sc.policyOn() {
			if read, err = sc.readOf(in, st.Op); err != nil {
				return err
			}
		}
		if st.Op != OpSelect && pol.write != nil && sc.policyOn() {
			p, err := pol.write(ctx)
			if write, err = sc.policyCond(in, st.Op, "Write", p, err); err != nil {
				return err
			}
		}
	}
	extra := 0
	if tenant {
		extra++
	}
	if read.n.op != 0 {
		extra++
	}
	if write.n.op != 0 {
		extra++
	}
	// The conditions are the caller's and may be shared with other runs of
	// the same query, in other goroutines: an EXISTS that gains a condition
	// is replaced by a copy, and so is every node above it, in a copy of
	// st.Where.
	copied := false
	for i, p := range st.Where {
		n, changed, err := sc.node(p.n, true)
		if err != nil {
			return err
		}
		if !changed {
			continue
		}
		if !copied {
			w := make([]Predicate, len(st.Where), len(st.Where)+extra)
			copy(w, st.Where)
			st.Where, copied = w, true
		}
		st.Where[i] = Predicate{n}
	}
	// st.Where is clipped (or a copy), so appending never writes into the
	// query's own array.
	if tenant {
		st.Where = append(st.Where, tenantPredicate(in, sc.id))
	}
	if read.n.op != 0 {
		st.Where = append(st.Where, read)
	}
	if write.n.op != 0 {
		st.Where = append(st.Where, write)
	}
	if sc.scoped {
		if sc.all != "" {
			st.TenantBypass = sc.all
		} else {
			st.Tenant = sc.id
		}
	}
	if sc.skipped {
		st.PolicyBypass = sc.skip
	}
	return nil
}

// scoper is one statement's scope: the context's tenant and SkipPolicy,
// looked up when an entity first needs them, and the Read conditions made so
// far.
type scoper struct {
	ctx    context.Context
	entity string // the statement's entity, which every error names

	tenantRead bool   // id and all are looked up
	id         any    // the context's tenant; nil when none
	all        string // the AllTenants reason
	scoped     bool   // an entity of the statement is tenant-scoped

	skipRead bool   // skip is looked up
	skip     string // the SkipPolicy reason
	skipped  bool   // a policy that would have applied was skipped

	reads []readCond
}

// readCond is an entity's Read condition, made once per statement.
type readCond struct {
	entity *EntityInfo
	p      Predicate
}

// tenantOf reports whether entity t's rows get the tenant condition: whether
// t is tenant-scoped and the context has a tenant of t's type. It fails when
// t is scoped and the context has neither a tenant nor AllTenants.
func (sc *scoper) tenantOf(t *EntityInfo) (bool, error) {
	if t.Tenant < 0 {
		return false, nil
	}
	if !sc.tenantRead {
		sc.id, sc.all = tenantOf(sc.ctx)
		sc.tenantRead = true
	}
	sc.scoped = true
	switch {
	case sc.all != "":
		return false, nil
	case sc.id == nil:
		return false, fmt.Errorf("velox: %s: %w", t.Name, ErrNoTenant)
	}
	return true, checkTenantValue(t, sc.id)
}

// policyOn reports whether a policy applies: false under SkipPolicy, which
// is then recorded as applied.
func (sc *scoper) policyOn() bool {
	if !sc.skipRead {
		sc.skip = policyBypass(sc.ctx)
		sc.skipRead = true
	}
	if sc.skip != "" {
		sc.skipped = true
		return false
	}
	return true
}

// readOf returns entity t's Read condition, calling its policy the first time
// only; a denial is a *PolicyError for op.
func (sc *scoper) readOf(t *EntityInfo, op Op) (Predicate, error) {
	for _, r := range sc.reads {
		if r.entity == t {
			return r.p, nil
		}
	}
	p, err := t.policy.read(sc.ctx)
	if p, err = sc.policyCond(t, op, "Read", p, err); err != nil {
		return Predicate{}, err
	}
	sc.reads = append(sc.reads, readCond{t, p})
	return p, nil
}

// policyCond checks what entity t's policy (which, "Read" or "Write")
// returned for a statement of kind op: an error denies the statement, and a
// condition is refused when it holds the zero Pred or is invalid. An EXISTS
// in the condition gets its target's tenant, but no other policy: a policy is
// the schema's own code, and its subqueries are as it wrote them.
func (sc *scoper) policyCond(t *EntityInfo, op Op, which string, p Predicate, err error) (Predicate, error) {
	switch {
	case err != nil:
		return Predicate{}, &PolicyError{Entity: t.Name, Op: op, Err: err}
	case p.n.op == 0:
		return p, nil // no restriction
	case hasZero(p.n):
		return Predicate{}, fmt.Errorf("velox: %s: the %s policy's condition holds the zero Pred", t.Name, which)
	}
	if err := predErr(p.n); err != nil {
		return Predicate{}, fmt.Errorf("velox: %s: the %s policy's condition: %w", t.Name, which, err)
	}
	n, _, err := sc.node(p.n, false)
	return Predicate{n}, err
}

// tenantPredicate is the condition that scopes entity in's rows to tenant id.
func tenantPredicate(in *EntityInfo, id any) Predicate { return ColumnEQ(in.Tenant, id) }

// checkTenantValue refuses a tenant id whose dynamic type is not exactly the
// Go type of in's tenant column: a named OrgID is not an int64, and an
// untyped constant is an int.
func checkTenantValue(in *EntityInfo, id any) error {
	c := in.Columns[in.Tenant]
	if t := GoType(c); reflect.TypeOf(id) != t {
		return fmt.Errorf("velox: %s: %w: the context's tenant is a %T, the column %s is a %s", in.Name, ErrTenantMismatch, id, c.Name, t)
	}
	return nil
}

// checkTenantAssignments refuses an assignment that would move a row to
// another tenant: one that sets the tenant column to anything but id, or adds
// to or subtracts from it.
func checkTenantAssignments(in *EntityInfo, set []Assignment, id any) error {
	for _, a := range set {
		if a.Column != in.Tenant {
			continue
		}
		col := in.Columns[in.Tenant].Name
		switch {
		case a.Op != AssignSet:
			return fmt.Errorf("velox: %s: %w: the statement does arithmetic on %s", in.Name, ErrTenantImmutable, col)
		case a.Value != id:
			// id's type is the column's, which is comparable, so this cannot
			// panic.
			return fmt.Errorf("velox: %s: %w: the statement sets %s to %v, the tenant is %v", in.Name, ErrTenantImmutable, col, a.Value, id)
		}
	}
	return nil
}

// node returns n with the tenant condition, and with policy the Read
// condition, added inside every EXISTS on a target that has them, and
// whether that changed anything. It never edits n: a changed node is a copy
// with a new kids slice, and unchanged subtrees are shared with n.
func (sc *scoper) node(n node, policy bool) (node, bool, error) {
	switch n.op {
	case opAnd, opOr, opNot, opExists:
	default:
		return n, false, nil
	}
	var kids []node // nil until a kid changes
	for i, k := range n.kids {
		nk, changed, err := sc.node(k, policy)
		if err != nil {
			return n, false, err
		}
		if !changed {
			continue
		}
		if kids == nil {
			kids = make([]node, len(n.kids), len(n.kids)+2)
			copy(kids, n.kids)
		}
		kids[i] = nk
	}
	if n.op == opExists {
		t := n.v.(link).target
		tenant, read, err := sc.target(t, policy)
		if err != nil {
			// An error from an EXISTS in t's policy names its own target.
			if _, ok := err.(*existsError); !ok {
				err = &existsError{entity: sc.entity, target: t.Name, err: err}
			}
			return n, false, err
		}
		if (tenant || read.n.op != 0) && kids == nil {
			kids = make([]node, len(n.kids), len(n.kids)+2)
			copy(kids, n.kids)
		}
		if tenant {
			kids = append(kids, tenantPredicate(t, sc.id).n)
		}
		if read.n.op != 0 {
			kids = append(kids, read.n)
		}
	}
	if kids == nil {
		return n, false, nil
	}
	n.kids = kids // n is a copy
	return n, true, nil
}

// target returns what an EXISTS on entity t gets: whether the tenant
// condition, and with policy t's Read condition (zero for none).
func (sc *scoper) target(t *EntityInfo, policy bool) (bool, Predicate, error) {
	tenant, err := sc.tenantOf(t)
	if err != nil {
		return false, Predicate{}, err
	}
	if !policy || t.policy == nil || t.policy.read == nil || !sc.policyOn() {
		return tenant, Predicate{}, nil
	}
	// An EXISTS reads its target, whatever the statement's kind.
	read, err := sc.readOf(t, OpSelect)
	return tenant, read, err
}

// existsError is an error of the scope inside an EXISTS on target: its text
// names the statement's entity, then the target, and drops err's own
// "velox: <target>: " so that the target is named once; errors.Is and As
// still see err.
type existsError struct {
	entity, target string
	err            error
}

func (e *existsError) Error() string {
	return "velox: " + e.entity + ": in the EXISTS on " + e.target + ": " + strings.TrimPrefix(e.err.Error(), "velox: "+e.target+": ")
}

func (e *existsError) Unwrap() error { return e.err }
