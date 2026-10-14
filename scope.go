package velox

import (
	"context"
	"fmt"
	"reflect"
)

// scope applies the context's tenant to a statement (policy design §2.1): on
// a tenant-scoped entity, and inside every EXISTS on a tenant-scoped target,
// it adds the condition tenant = id, or fails the statement with ErrNoTenant
// when the context has neither WithTenant nor AllTenants. It runs after the
// guard and the Rewrite hooks, so neither sees what it adds and no hook can
// remove it.
//
// st does not escape: scope passes it to no function value, so plain's
// statement stays on the stack.
func (db *DB) scope(ctx context.Context, st *Statement) error {
	switch st.Op {
	case OpSelect, OpUpdate, OpDelete:
	default:
		return nil
	}
	in := st.Entity
	name := in.Name
	if in.Tenant < 0 {
		// Only the EXISTS conditions can be scoped; most statements have
		// none, and need no context lookup.
		t := scopedTarget(st.Where)
		if t == nil {
			return nil
		}
		name = t.Name
	}
	id, all := tenantOf(ctx)
	switch {
	case all != "":
		st.TenantBypass = all
		return nil
	case id == nil:
		return fmt.Errorf("velox: %s: %w", name, ErrNoTenant)
	}
	if in.Tenant >= 0 {
		if err := checkTenantValue(in, id); err != nil {
			return err
		}
		if err := checkTenantAssignments(in, st.Set, id); err != nil {
			return err
		}
	}
	// The conditions are the caller's and may be shared with other runs of
	// the same query, in other goroutines: an EXISTS that gains the tenant
	// is replaced by a copy, and so is every node above it, in a copy of
	// st.Where.
	copied := false
	for i, p := range st.Where {
		n, changed, err := scopeNode(p.n, id)
		if err != nil {
			return err
		}
		if !changed {
			continue
		}
		if !copied {
			w := make([]Predicate, len(st.Where), len(st.Where)+1)
			copy(w, st.Where)
			st.Where, copied = w, true
		}
		st.Where[i] = Predicate{n}
	}
	if in.Tenant >= 0 {
		st.Where = append(st.Where, tenantPredicate(in, id))
	}
	st.Tenant = id
	return nil
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

// scopedTarget returns the target of the first EXISTS in ws, at any depth,
// whose entity is tenant-scoped; nil when there is none.
func scopedTarget(ws []Predicate) *EntityInfo {
	for _, p := range ws {
		if t := scopedTargetIn(p.n); t != nil {
			return t
		}
	}
	return nil
}

func scopedTargetIn(n node) *EntityInfo {
	if n.op == opExists {
		if t := n.v.(link).target; t.Tenant >= 0 {
			return t
		}
	}
	for _, k := range n.kids {
		if t := scopedTargetIn(k); t != nil {
			return t
		}
	}
	return nil
}

// scopeNode returns n with the tenant condition added inside every EXISTS on
// a tenant-scoped target, and whether that changed anything. It never edits
// n: a changed node is a copy with a new kids slice, and unchanged subtrees
// are shared with n.
func scopeNode(n node, id any) (node, bool, error) {
	switch n.op {
	case opAnd, opOr, opNot, opExists:
	default:
		return n, false, nil
	}
	var kids []node // nil until a kid changes
	for i, k := range n.kids {
		nk, changed, err := scopeNode(k, id)
		if err != nil {
			return n, false, err
		}
		if !changed {
			continue
		}
		if kids == nil {
			kids = make([]node, len(n.kids), len(n.kids)+1)
			copy(kids, n.kids)
		}
		kids[i] = nk
	}
	if n.op == opExists {
		if t := n.v.(link).target; t.Tenant >= 0 {
			// The target's tenant column may be of another type than the
			// owner's: the check names the target.
			if err := checkTenantValue(t, id); err != nil {
				return n, false, err
			}
			if kids == nil {
				kids = make([]node, len(n.kids), len(n.kids)+1)
				copy(kids, n.kids)
			}
			kids = append(kids, tenantPredicate(t, id).n)
		}
	}
	if kids == nil {
		return n, false, nil
	}
	n.kids = kids // n is a copy
	return n, true, nil
}
