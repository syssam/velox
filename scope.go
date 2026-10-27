package velox

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
)

// scope applies the context's tenant and the entities' policies to a statement.
// On a tenant-scoped entity it adds the condition tenant = id, or fails the
// statement with ErrNoTenant when the context has neither WithTenant nor
// WithAllTenants. Unless the context says WithoutPolicy, a SELECT gets the
// entity's Read policy and an UPDATE or DELETE, or a Writable SELECT, its Read
// and Write policies, after the tenant. Every EXISTS gets its target's tenant
// and Read policy the same way. scope runs after the guard and the Rewrite
// hooks, so neither sees what it adds and no hook can remove it.
//
// Raw SQL (OpRawQuery and OpRawExec) is never scoped: prepare does not call
// scope for it, as its SQL is the caller's, and its Statement.TenantBypass
// holds the reason.
//
// st does not escape: scope passes it to no function value, and policy
// functions are given only ctx, so plain's statement stays on the stack.
func (db *DB) scope(ctx context.Context, st *Statement) error {
	switch st.Op {
	case OpSelect, OpUpdate, OpDelete:
	case OpInsert:
		return db.scopeInsert(ctx, st)
	default:
		return nil
	}
	in := st.Entity
	sc := scoper{ctx: ctx, entity: in.Name}
	id, tenant, err := sc.tenantOf(in)
	if err != nil {
		return err
	}
	if tenant {
		if err := checkTenantAssignments(in, st.Set, id); err != nil {
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
		if (st.Op != OpSelect || st.Writable) && pol.write != nil && sc.policyOn() {
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
	if st.Where, err = sc.walk(st.Where, extra); err != nil {
		return err
	}
	// st.Where is now the scope's own array, or nothing is appended to it.
	if tenant {
		st.Where = append(st.Where, tenantPredicate(in, id))
	}
	if read.n.op != 0 {
		st.Where = append(st.Where, read)
	}
	if write.n.op != 0 {
		st.Where = append(st.Where, write)
	}
	sc.record(st)
	return nil
}

// scopeInsert applies the context's tenant and the entity's policy to an
// INSERT. prepareRow has filled and checked each row's tenant, and
// insertAllowed has asked the Insert policy; scopeInsert checks that the Rewrite hooks left every row in
// the tenant. An upsert that updates must reach neither another tenant's row
// nor one the policy hides from an update. On PostgreSQL and SQLite its
// conflict columns must hold the tenant column, and its update branch gets
// the Read and Write conditions; an EXISTS a Rewrite hook put in that
// branch's condition gets its target's tenant and Read, as one in a Where
// does. MySQL's ON DUPLICATE KEY UPDATE resolves a conflict on any unique key
// and takes no condition: every unique key must hold the tenant column, and
// an entity with a Read or Write policy is refused. Keeping the existing row
// (DoNothing) changes nothing, so it is exempt.
func (db *DB) scopeInsert(ctx context.Context, st *Statement) error {
	in := st.Entity
	sc := scoper{ctx: ctx, entity: in.Name}
	id, tenant, err := sc.tenantOf(in)
	if err != nil {
		return err
	}
	if tenant && len(db.cfg.rewrites) > 0 {
		if err := checkTenantRows(in, st, id); err != nil {
			return err
		}
	}
	pol := in.policy
	if pol != nil && pol.insert != nil {
		sc.policyOn() // records a WithoutPolicy that insertAllowed obeyed
	}
	if cf := st.Conflict; cf != nil && !cf.DoNothing && len(cf.Update) > 0 {
		if tenant {
			if err := db.checkTenantUpsert(in, cf); err != nil {
				return err
			}
		}
		conditioned := pol != nil && (pol.read != nil || pol.write != nil) && sc.policyOn()
		if conditioned && db.d == MySQL {
			return fmt.Errorf("velox: %s: %w: MySQL's ON DUPLICATE KEY UPDATE takes no condition, and the entity has a Read or Write policy; keep the existing row with DoNothing, or update it with Update or Save", in.Name, ErrPolicyUpsert)
		}
		if err := sc.conditionUpsert(in, st, conditioned); err != nil {
			return err
		}
		if st.upsertPolicy && len(st.Rows) > 1 && !st.InTx {
			// conditionedUpsert runs every such InsertAll in a transaction:
			// only a Rewrite hook gets here.
			return fmt.Errorf("velox: %s: a Rewrite hook gave a multi-row upsert outside a transaction an update branch the policy conditions; a row the policy hides would fail it after the others were written", in.Name)
		}
	}
	sc.record(st)
	return nil
}

// conditionUpsert scopes the condition of the update branch of st's upsert:
// every EXISTS in it, a Rewrite hook's included, gets its target's tenant and
// Read condition, as in a Where, and with policy it gains entity in's Read
// and Write conditions. It works on a copy of the clause: the caller's
// InsertQuery may share it with other runs. A denial is the upsert's, as an
// update's.
func (sc *scoper) conditionUpsert(in *EntityInfo, st *Statement, policy bool) error {
	var read, write Predicate
	var err error
	// The entity's own policy first, as in scope.
	if policy && in.policy.read != nil {
		if read, err = sc.readOf(in, OpUpdate); err != nil {
			return err
		}
	}
	if policy && in.policy.write != nil {
		p, err := in.policy.write(sc.ctx)
		if write, err = sc.policyCond(in, OpUpdate, "Write", p, err); err != nil {
			return err
		}
	}
	extra := 0
	if read.n.op != 0 {
		extra++
	}
	if write.n.op != 0 {
		extra++
	}
	if extra == 0 && len(st.Conflict.Where) == 0 {
		return nil
	}
	cf := *st.Conflict
	where, err := sc.walk(cf.Where, extra)
	if err != nil {
		return err
	}
	cf.Where = where // the scope's own array, or nothing is appended to it
	if read.n.op != 0 {
		cf.Where = append(cf.Where, read)
	}
	if write.n.op != 0 {
		cf.Where = append(cf.Where, write)
	}
	st.upsertPolicy = extra > 0
	st.upsertRows = len(st.Rows)
	st.Conflict = &cf
	return nil
}

// record shows hooks the scope that applied to st: the tenant as the type of
// the statement entity's tenant column, which scope converted first; when
// that entity is unscoped but an EXISTS target is scoped, the id as
// WithTenant was given it, since the targets' column types may differ.
func (sc *scoper) record(st *Statement) {
	if sc.scoped {
		switch {
		case sc.all != "":
			st.TenantBypass = sc.all
		case st.Entity.Tenant >= 0:
			st.Tenant = sc.conv.id
		default:
			st.Tenant = sc.id
		}
	}
	if sc.skipped {
		st.PolicyBypass = sc.skip
	}
}

// checkTenantRows refuses an INSERT whose rows, as the Rewrite hooks left
// them, are not all in tenant id.
func checkTenantRows(in *EntityInfo, st *Statement, id any) error {
	col := in.Columns[in.Tenant].Name
	j := slices.Index(st.Columns, in.Tenant)
	if j < 0 {
		return fmt.Errorf("velox: %s: %w: the statement does not write %s", in.Name, ErrTenantMismatch, col)
	}
	for _, r := range st.Rows {
		// id's type is the column's, which is comparable, so this cannot
		// panic.
		if r[j] != id {
			return fmt.Errorf("velox: %s: %w: a row's %s is %v, the tenant is %v", in.Name, ErrTenantMismatch, col, r[j], id)
		}
	}
	return nil
}

// checkTenantUpsert refuses an upsert that updates and whose conflict could
// be with another tenant's row. On PostgreSQL and SQLite the conflict columns
// must hold the tenant column. MySQL resolves a conflict on any unique key,
// so the primary key and every unique index must hold it.
func (db *DB) checkTenantUpsert(in *EntityInfo, cf *ConflictClause) error {
	col := in.Columns[in.Tenant].Name
	if db.d != MySQL {
		if slices.Contains(cf.Columns, in.Tenant) {
			return nil
		}
		return fmt.Errorf("velox: %s: %w: the conflict columns (%s) do not hold the tenant column %s; name the tenant column in OnConflict", in.Name, ErrTenantUpsert, columnNames(in, cf.Columns), col)
	}
	what, cols := "the primary key", in.PrimaryKey
	if slices.Contains(cols, in.Tenant) {
		what, cols = "", nil
		for _, ix := range in.Indexes {
			if ix.Unique && !slices.Contains(ix.Columns, in.Tenant) {
				what, cols = "the unique index", ix.Columns
				break
			}
		}
	}
	if what == "" {
		return nil
	}
	return fmt.Errorf("velox: %s: %w: MySQL's ON DUPLICATE KEY UPDATE resolves a conflict on any unique key, and %s (%s) does not hold the tenant column %s", in.Name, ErrTenantUpsert, what, columnNames(in, cols), col)
}

// columnNames lists the names of in's columns cols, separated by commas.
func columnNames(in *EntityInfo, cols []int) string {
	var b strings.Builder
	for i, c := range cols {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(in.Columns[c].Name)
	}
	return b.String()
}

// scoper is one statement's scope: the context's tenant and WithoutPolicy,
// looked up when an entity first needs them, and the Read conditions made so
// far.
type scoper struct {
	ctx    context.Context
	entity string // the statement's entity, which every error names

	tenantRead bool   // id and all are looked up
	id         any    // the context's tenant, as given; nil when none
	all        string // the WithAllTenants reason
	scoped     bool   // an entity of the statement is tenant-scoped

	// conv is id converted for the first tenant column type the statement
	// met, and convs for the others: id converts once per type, however
	// many EXISTS reach entities of that type.
	conv  tenantConv
	convs []tenantConv

	skipRead bool   // skip is looked up
	skip     string // the WithoutPolicy reason
	skipped  bool   // a policy that would have applied was skipped

	reads     []readCond
	computing []*EntityInfo // the entities whose Read is being made, outermost first
}

// tenantConv is the context's tenant converted to tenant column type t.
type tenantConv struct {
	t  reflect.Type
	id any
}

// readCond is an entity's Read condition, made once per statement.
type readCond struct {
	entity *EntityInfo
	p      Predicate
}

// tenantOf reports whether entity t's rows get the tenant condition, and
// returns the tenant converted to t's tenant column type (tenantFor): ok when
// t is tenant-scoped and the context has a tenant that serves t. It fails
// when t is scoped and the context has neither a tenant nor WithAllTenants,
// or a tenant that does not serve t.
func (sc *scoper) tenantOf(t *EntityInfo) (id any, ok bool, err error) {
	if t.Tenant < 0 {
		return nil, false, nil
	}
	if !sc.tenantRead {
		sc.id, sc.all = tenantOf(sc.ctx)
		sc.tenantRead = true
	}
	sc.scoped = true
	switch {
	case sc.all != "":
		return nil, false, nil
	case sc.id == nil:
		return nil, false, fmt.Errorf("velox: %s: %w", t.Name, ErrNoTenant)
	}
	ct := GoType(t.Columns[t.Tenant])
	if sc.conv.t == ct {
		return sc.conv.id, true, nil
	}
	for _, c := range sc.convs {
		if c.t == ct {
			return c.id, true, nil
		}
	}
	if id, err = tenantFor(t, sc.id); err != nil {
		return nil, false, err
	}
	if sc.conv.t == nil {
		sc.conv = tenantConv{ct, id}
	} else {
		sc.convs = append(sc.convs, tenantConv{ct, id})
	}
	return id, true, nil
}

// policyOn reports whether a policy applies: false under WithoutPolicy, which
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
// only; a denial is a *PolicyError for op. The EXISTS in the condition get
// their targets' Read conditions in turn; a condition that reaches t's own
// Read again, directly or through other entities, cannot be made, and the
// statement is refused.
func (sc *scoper) readOf(t *EntityInfo, op Op) (Predicate, error) {
	for _, r := range sc.reads {
		if r.entity == t {
			return r.p, nil
		}
	}
	if slices.Contains(sc.computing, t) {
		return Predicate{}, &cycleError{entity: sc.entity, policy: t.Name}
	}
	sc.computing = append(sc.computing, t)
	p, err := t.policy.read(sc.ctx)
	p, err = sc.policyCond(t, op, "Read", p, err)
	sc.computing = sc.computing[:len(sc.computing)-1]
	if err != nil {
		return Predicate{}, err
	}
	sc.reads = append(sc.reads, readCond{t, p})
	return p, nil
}

// cycleError is a Read policy that reaches its own Read through an EXISTS:
// applying it would never end. It is the schema's bug, not a denial.
type cycleError struct {
	entity, policy string // the statement's entity; the entity whose Read loops
}

func (e *cycleError) Error() string {
	return "velox: " + e.entity + ": the Read policy of " + e.policy + " reaches its own Read through an EXISTS; such a policy cannot be applied"
}

// policyCond checks what entity t's policy (which, "Read" or "Write")
// returned for a statement of kind op: an error denies the statement, a zero
// Pred fails it (a policy says Allow for no restriction), Allow is no
// condition, and a condition is refused when it holds the zero Pred or is
// invalid. An EXISTS in the condition gets its target's tenant and Read
// condition, as one in the statement's own conditions does: a policy that
// inherits another entity's with Has sees only the rows that one lets through.
func (sc *scoper) policyCond(t *EntityInfo, op Op, which string, p Predicate, err error) (Predicate, error) {
	switch {
	case err != nil:
		return Predicate{}, &PolicyError{Entity: t.Name, Op: op, Err: err}
	case p.n.op == 0:
		return Predicate{}, fmt.Errorf("velox: %s: the %s policy returned the zero Pred; return velox.Allow[E]() for no restriction", t.Name, which)
	case p.n.op == opAllow:
		return Predicate{}, nil // no restriction
	case hasZero(p.n):
		return Predicate{}, fmt.Errorf("velox: %s: the %s policy's condition holds the zero Pred", t.Name, which)
	}
	if err := predErr(p.n); err != nil {
		return Predicate{}, fmt.Errorf("velox: %s: the %s policy's condition: %w", t.Name, which, err)
	}
	n, _, err := sc.node(p.n)
	return Predicate{n}, err
}

// tenantPredicate is the condition that scopes entity in's rows to tenant id.
func tenantPredicate(in *EntityInfo, id any) Predicate { return ColumnEQ(in.Tenant, id) }

// tenantFor returns id as the Go type of in's tenant column: unchanged when it
// is of that type; converted when it is of a predeclared integer type and the
// column's kind is an integer kind, or it is a string and the column's kind
// is string, whether the column's type is predeclared or named; refused with
// ErrTenantMismatch otherwise, or when it does not fit; and refused with
// ErrNoTenant when the result is zero.
//
// A named id is never converted: a UserID does not serve an OrgID column,
// even when both are int64s. The zero value is no tenant: that is what
// middleware gets when it parses a claim that is missing, and an insert could
// not tell it from a tenant field left unset.
//
// The exact type allocates nothing. A conversion to a predeclared type makes
// its value with a conversion of the kind's, which the runtime serves without
// allocating below 256; only a named column type goes through reflect.
func tenantFor(in *EntityInfo, id any) (any, error) {
	c := in.Columns[in.Tenant]
	t := GoType(c)
	if reflect.TypeOf(id) == t {
		if reflect.ValueOf(id).IsZero() {
			return nil, zeroTenant(in)
		}
		return id, nil
	}
	var (
		s        int64  // a signed id
		u        uint64 // an unsigned id
		unsigned bool
	)
	switch x := id.(type) {
	case int:
		s = int64(x)
	case int8:
		s = int64(x)
	case int16:
		s = int64(x)
	case int32:
		s = int64(x)
	case int64:
		s = x
	case uint:
		u, unsigned = uint64(x), true
	case uint8:
		u, unsigned = uint64(x), true
	case uint16:
		u, unsigned = uint64(x), true
	case uint32:
		u, unsigned = uint64(x), true
	case uint64:
		u, unsigned = x, true
	case string:
		if t.Kind() != reflect.String {
			return nil, tenantMismatch(in, id)
		}
		if x == "" {
			return nil, zeroTenant(in)
		}
		// A predeclared string column took the exact path: t is named.
		return reflect.ValueOf(x).Convert(t).Interface(), nil
	default:
		return nil, tenantMismatch(in, id)
	}
	var out any
	switch k := t.Kind(); k {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if unsigned {
			if u > math.MaxInt64 {
				return nil, tenantMisfit(in, id)
			}
			s = int64(u)
		}
		if t.OverflowInt(s) {
			return nil, tenantMisfit(in, id)
		}
		if s == 0 {
			return nil, zeroTenant(in)
		}
		switch k {
		case reflect.Int:
			out = int(s)
		case reflect.Int8:
			out = int8(s)
		case reflect.Int16:
			out = int16(s)
		case reflect.Int32:
			out = int32(s)
		default:
			out = s
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if !unsigned {
			if s < 0 {
				return nil, tenantMisfit(in, id)
			}
			u = uint64(s)
		}
		if t.OverflowUint(u) {
			return nil, tenantMisfit(in, id)
		}
		if u == 0 {
			return nil, zeroTenant(in)
		}
		switch k {
		case reflect.Uint:
			out = uint(u)
		case reflect.Uint8:
			out = uint8(u)
		case reflect.Uint16:
			out = uint16(u)
		case reflect.Uint32:
			out = uint32(u)
		default:
			out = u
		}
	default:
		return nil, tenantMismatch(in, id)
	}
	if reflect.TypeOf(out) != t {
		out = reflect.ValueOf(out).Convert(t).Interface() // a named column type
	}
	return out, nil
}

// tenantMismatch is the error of a tenant id that never serves in's tenant
// column.
func tenantMismatch(in *EntityInfo, id any) error {
	c := in.Columns[in.Tenant]
	return fmt.Errorf("velox: %s: %w: the context's tenant is a %T, the column %s is a %s", in.Name, ErrTenantMismatch, id, c.Name, GoType(c))
}

// tenantMisfit is the error of a tenant id that would convert into in's
// tenant column, but whose value the column's type cannot hold.
func tenantMisfit(in *EntityInfo, id any) error {
	c := in.Columns[in.Tenant]
	return fmt.Errorf("velox: %s: %w: the context's tenant %v (a %T) does not fit the column %s, a %s", in.Name, ErrTenantMismatch, id, id, c.Name, GoType(c))
}

// zeroTenant is the error of a tenant id that is the zero value.
func zeroTenant(in *EntityInfo) error {
	return fmt.Errorf("velox: %s: %w: the tenant is the zero value", in.Name, ErrNoTenant)
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

// walk returns ps with the tenant and Read conditions added inside every
// EXISTS, ready for extra more conditions to be appended. The conditions are
// the caller's and may be shared with other runs of the same query, in other
// goroutines: an EXISTS that gains a condition is replaced by a copy, and so
// is every node above it, in a copy of ps. The array ps is on may be anyone's
// too (a Rewrite hook may point st.Where at a slice of defaults it shares
// between statements), so appending is never left to write past ps's length:
// with extra > 0 the result is a copy of ps even when nothing in it changed.
func (sc *scoper) walk(ps []Predicate, extra int) ([]Predicate, error) {
	copied := false
	for i, p := range ps {
		n, changed, err := sc.node(p.n)
		if err != nil {
			return nil, err
		}
		if !changed {
			continue
		}
		if !copied {
			ps, copied = withRoom(ps, extra), true
		}
		ps[i] = Predicate{n}
	}
	if !copied && extra > 0 {
		ps = withRoom(ps, extra)
	}
	return ps, nil
}

// withRoom returns a copy of ps on a new array with room for extra more.
func withRoom(ps []Predicate, extra int) []Predicate {
	w := make([]Predicate, len(ps), len(ps)+extra)
	copy(w, ps)
	return w
}

// node returns n with the tenant and Read conditions added inside every
// EXISTS on a target that has them, and
// whether that changed anything. It never edits n: a changed node is a copy
// with a new kids slice, and unchanged subtrees are shared with n.
func (sc *scoper) node(n node) (node, bool, error) {
	switch n.op {
	case opAnd, opOr, opNot, opExists:
	default:
		// An opThrough reads only join rows, which are never scoped: a join
		// entity has neither a tenant column nor a Read policy.
		return n, false, nil
	}
	// An EXISTS's relation first: an error from inside it names its target.
	// resolveNode, for the statement's own conditions, walks the kids first;
	// the order differs on purpose, and only a stale generated relation can
	// fail here.
	var l link
	var t *EntityInfo // the EXISTS's target
	if n.op == opExists {
		var err error
		if l, err = existsLink(n); err != nil {
			return n, false, err
		}
		t = l.target
	}
	var kids []node // nil until a kid changes
	for i, k := range n.kids {
		nk, changed, err := sc.node(k)
		if err != nil {
			if t != nil {
				err = sc.inExists(t, err)
			}
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
	resolved := false
	if n.op == opExists {
		// A relation is resolved here when the condition came from a policy
		// or a Rewrite hook: the statement's own were resolved by prepare.
		if _, lazy := n.v.(relRef); lazy {
			n.v, resolved = l, true // n is a copy
		}
		id, tenant, read, err := sc.target(t)
		if err != nil {
			return n, false, sc.inExists(t, err)
		}
		if (tenant || read.n.op != 0) && kids == nil {
			kids = make([]node, len(n.kids), len(n.kids)+2)
			copy(kids, n.kids)
		}
		if tenant {
			kids = append(kids, tenantPredicate(t, id).n)
		}
		if read.n.op != 0 {
			kids = append(kids, read.n)
		}
	}
	if kids == nil {
		return n, resolved, nil
	}
	n.kids = kids // n is a copy
	return n, true, nil
}

// target returns what an EXISTS on entity t gets: whether the tenant
// condition, with the tenant as t's column type, and t's Read condition (zero
// for none).
func (sc *scoper) target(t *EntityInfo) (any, bool, Predicate, error) {
	id, tenant, err := sc.tenantOf(t)
	if err != nil {
		return nil, false, Predicate{}, err
	}
	if t.policy == nil || t.policy.read == nil || !sc.policyOn() {
		return id, tenant, Predicate{}, nil
	}
	// An EXISTS reads its target, whatever the statement's kind.
	read, err := sc.readOf(t, OpSelect)
	return id, tenant, read, err
}

// inExists returns err, an error from inside an EXISTS on t (from the EXISTS
// in its condition, or from t's tenant and Read), with t at the front of its
// path. A cycle passes unchanged: it names the statement's entity already.
func (sc *scoper) inExists(t *EntityInfo, err error) error {
	switch e := err.(type) {
	case *cycleError:
		return err
	case *existsError:
		e.path = slices.Insert(e.path, 0, t.Name)
		return e
	}
	return &existsError{entity: sc.entity, path: []string{t.Name}, err: err}
}

// existsError is an error of the scope inside an EXISTS: its text names the
// statement's entity, then the EXISTS targets from the outermost in, and
// drops err's own "velox: <innermost target>: " so that the target is named
// once; errors.Is and As still see err.
type existsError struct {
	entity string
	path   []string // the EXISTS targets, outermost first
	err    error
}

func (e *existsError) Error() string {
	return "velox: " + e.entity + ": in the EXISTS on " + strings.Join(e.path, ", then on ") + ": " + strings.TrimPrefix(e.err.Error(), "velox: "+e.path[len(e.path)-1]+": ")
}

func (e *existsError) Unwrap() error { return e.err }
