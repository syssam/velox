package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// ConditionUpsert scopes the condition of the update branch of st's upsert:
// every EXISTS in it, a Rewrite hook's included, gets its target's row
// condition, as in a Where. It gains entity in's interceptors' conditions,
// and with policy its Read and Write conditions. It works on a copy of the clause: the caller's
// InsertQuery may share it with other runs. A denial is the upsert's, as an
// update's.
func (sc *Scoper) ConditionUpsert(in *EntityInfo, st *Statement, policy bool) error {
	var read, write Predicate
	var err error
	// The entity's own conditions first, as in scope.
	if sc.filtered(in) || (policy && in.policy.Read != nil) {
		if read, err = sc.RowCond(in, OpUpdate); err != nil {
			return err
		}
	}
	if policy && in.policy.Write != nil {
		p, err := in.policy.Write(sc.Ctx, OpUpdate)
		if write, err = sc.PolicyCond(in, OpUpdate, "Write", p, err); err != nil {
			return err
		}
	}
	extra := 0
	if read.n.Op != opZero {
		extra++
	}
	if write.n.Op != opZero {
		extra++
	}
	if extra == 0 && len(st.Conflict.Where) == 0 {
		return nil
	}
	cf := *st.Conflict
	where, err := sc.Walk(cf.Where, extra)
	if err != nil {
		return err
	}
	cf.Where = where // the scope's own array, or nothing is appended to it
	cf.Where = append(append(cf.Where, conjuncts(read)...), conjuncts(write)...)
	st.upsertPolicy = extra > 0
	st.upsertRows = len(st.Rows)
	st.Conflict = &cf
	return nil
}

// Record shows hooks that a policy was skipped for st: Statement.PolicyBypass.
func (sc *Scoper) Record(st *Statement) {
	if sc.skipped {
		st.PolicyBypass = sc.Skip
	}
}

// Scoper is one statement's scope: the DB's WithoutPolicy reason and the row
// conditions made so far.
type Scoper struct {
	Ctx    context.Context
	Entity string // the statement's entity, which every error names
	Skip   string // the DB's WithoutPolicy reason; "" when none
	// Filters are the DB's WithFilter filters, which every entity's row
	// condition gets before its interceptors'.
	Filters []EntityFilterFn
	// Reads is set for a statement that reads only (a SELECT that is not
	// Writable): the policies' Query conditions apply to it, and to every
	// EXISTS and join in it.
	Reads bool

	skipped bool // a policy that would have applied was skipped

	reads     []readCond
	computing []*EntityInfo // the entities whose row condition is being made, outermost first
	cut       int           // the outermost place (from 1) in computing that the condition being made was cut at; 0 for none
}

// readCond is an entity's row condition, made once per statement.
type readCond struct {
	entity *EntityInfo
	p      Predicate
}

// conditions reports whether policy p gives this statement a row condition:
// a Read, or a Query when the statement reads only.
func (sc *Scoper) conditions(p *EntityPolicy) bool {
	return p != nil && (p.Read != nil || sc.Reads && p.Query != nil)
}

// filtered reports whether entity t's rows get a condition whatever the
// policy: from a WithFilter filter or an interceptor.
func (sc *Scoper) filtered(t *EntityInfo) bool { return len(sc.Filters) > 0 || len(t.filters) > 0 }

// PolicyOn reports whether a policy applies: false under WithoutPolicy, which
// is then recorded as applied.
func (sc *Scoper) PolicyOn() bool {
	if sc.Skip != "" {
		sc.skipped = true
		return false
	}
	return true
}

// RowCond returns entity t's row condition: the AND of the WithFilter
// filters' conditions, then its interceptors', in order, then its Read
// policy's unless WithoutPolicy. It calls them the first time only, once per
// statement. A denial of the policy is a *PolicyError for op; a filter's or
// an interceptor's error fails the statement and is not a denial. The EXISTS
// in the condition get their targets' row conditions in turn.
//
// A condition that reaches t's own again, directly or through other
// entities, while t's is being made, is cut there (ADR 0091): that inner
// EXISTS on t gets no row condition from t, neither its interceptors nor its
// policy, as the old velox's raw subquery got none. The outer occurrence
// applies t's in full, and every other entity's applies inside. A condition
// made below such a cut depends on where it was reached, so it is not kept
// for reuse: only t's own, whose cut is its own looping occurrence, is.
func (sc *Scoper) RowCond(t *EntityInfo, op Op) (Predicate, error) {
	for _, r := range sc.reads {
		if r.entity == t {
			return r.p, nil
		}
	}
	if i := slices.Index(sc.computing, t); i >= 0 {
		sc.markCut(i + 1)
		return Predicate{}, nil
	}
	depth := len(sc.computing) + 1 // t's place on the stack, counted from 1
	saved := sc.cut
	sc.cut = 0
	sc.computing = append(sc.computing, t)
	p, err := sc.rowCond(t, op)
	sc.computing = sc.computing[:len(sc.computing)-1]
	cut := sc.cut
	sc.cut = saved
	if cut != 0 && cut < depth {
		sc.markCut(cut) // the caller's condition is below this cut too
	}
	if err != nil {
		return Predicate{}, err
	}
	if cut == 0 || cut >= depth {
		sc.reads = append(sc.reads, readCond{t, p})
	}
	return p, nil
}

// markCut records a cut at the entity at place at (from 1) on the stack of
// conditions being made: the outermost cut counts.
func (sc *Scoper) markCut(at int) {
	if sc.cut == 0 || at < sc.cut {
		sc.cut = at
	}
}

// rowCond makes RowCond's condition.
func (sc *Scoper) rowCond(t *EntityInfo, op Op) (Predicate, error) {
	var kids []Node
	if len(sc.Filters) > 0 {
		name := EntityName(t)
		for i, f := range sc.Filters {
			p, err := f(sc.Ctx, name)
			c, err := sc.filterCond(t, "filter", i, p, err)
			if err != nil {
				return Predicate{}, err
			}
			for _, k := range conjuncts(c) {
				kids = append(kids, k.n)
			}
		}
	}
	for i, f := range t.filters {
		p, err := f(sc.Ctx)
		c, err := sc.filterCond(t, "interceptor", i, p, err)
		if err != nil {
			return Predicate{}, err
		}
		for _, k := range conjuncts(c) {
			kids = append(kids, k.n)
		}
	}
	if t.policy != nil && t.policy.Read != nil && sc.PolicyOn() {
		p, err := t.policy.Read(sc.Ctx)
		c, err := sc.PolicyCond(t, op, "Read", p, err)
		if err != nil {
			return Predicate{}, err
		}
		for _, k := range conjuncts(c) {
			kids = append(kids, k.n)
		}
	}
	if sc.Reads && t.policy != nil && t.policy.Query != nil && sc.PolicyOn() {
		p, err := t.policy.Query(sc.Ctx)
		c, err := sc.PolicyCond(t, op, "Query", p, err)
		if err != nil {
			return Predicate{}, err
		}
		for _, k := range conjuncts(c) {
			kids = append(kids, k.n)
		}
	}
	switch len(kids) {
	case 0:
		return Predicate{}, nil
	case 1:
		return Predicate{kids[0]}, nil
	}
	return Predicate{Group(OpAnd, kids)}, nil
}

// filterCond checks what entity t's interceptor or WithFilter filter
// (kind, "interceptor" or "filter") i returned, as PolicyCond checks a
// policy's: an error fails the statement, wrapped and not as a denial, a
// zero Pred fails it, and Allow is no condition.
func (sc *Scoper) filterCond(t *EntityInfo, kind string, i int, p Predicate, err error) (Predicate, error) {
	switch {
	case err != nil:
		return Predicate{}, fmt.Errorf("velox: %s: %s [%d]: %w", t.Name, kind, i, err)
	case p.n.Op == opZero:
		none := "velox.Allow()"
		if kind == "filter" {
			none = "velox.NoFilter()"
		}
		return Predicate{}, fmt.Errorf("velox: %s: %s [%d] returned the zero Predicate; return %s for no restriction", t.Name, kind, i, none)
	case p.n.Op == OpAllow:
		return Predicate{}, nil
	case hasZero(p.n):
		return Predicate{}, fmt.Errorf("velox: %s: %s [%d]'s condition holds the zero Pred", t.Name, kind, i)
	}
	if named(p.n) {
		n, err := resolveNamed(t, p.n)
		if err != nil {
			return Predicate{}, fmt.Errorf("velox: %s: %s [%d]'s condition: %w", t.Name, kind, i, err)
		}
		p = Predicate{n}
	}
	if err := PredErr(p.n); err != nil {
		return Predicate{}, fmt.Errorf("velox: %s: %s [%d]'s condition: %w", t.Name, kind, i, err)
	}
	n, _, err := sc.Node(p.n)
	return Predicate{n}, err
}

// PolicyCond checks what entity t's policy (which, "Read" or "Write")
// returned for a statement of kind op: an error denies the statement, a zero
// Pred fails it (a policy says Allow for no restriction), Allow is no
// condition, and a condition is refused when it holds the zero Pred or is
// invalid. An EXISTS in the condition gets its target's Read
// condition, as one in the statement's own conditions does: a policy that
// inherits another entity's with Has sees only the rows that one lets through.
func (sc *Scoper) PolicyCond(t *EntityInfo, op Op, which string, p Predicate, err error) (Predicate, error) {
	switch {
	case err != nil:
		return Predicate{}, &PolicyError{Entity: t.Name, Op: op, Err: err}
	case p.n.Op == opZero:
		return Predicate{}, fmt.Errorf("velox: %s: the %s policy returned the zero Predicate; return velox.Allow() for no restriction", t.Name, which)
	case p.n.Op == OpAllow:
		return Predicate{}, nil // no restriction
	case hasZero(p.n):
		return Predicate{}, fmt.Errorf("velox: %s: the %s policy's condition holds the zero Pred", t.Name, which)
	}
	if named(p.n) {
		n, err := resolveNamed(t, p.n)
		if err != nil {
			return Predicate{}, fmt.Errorf("velox: %s: the %s policy's condition: %w", t.Name, which, err)
		}
		p = Predicate{n}
	}
	if err := PredErr(p.n); err != nil {
		return Predicate{}, fmt.Errorf("velox: %s: the %s policy's condition: %w", t.Name, which, err)
	}
	n, _, err := sc.Node(p.n)
	return Predicate{n}, err
}

// Walk returns ps with the Read conditions added inside every
// EXISTS, ready for extra more conditions to be appended. The conditions are
// the caller's and may be shared with other runs of the same query, in other
// goroutines: an EXISTS that gains a condition is replaced by a copy, and so
// is every node above it, in a copy of ps. The array ps is on may be anyone's
// too (a Rewrite hook may point st.Where at a slice of defaults it shares
// between statements), so appending is never left to write past ps's length:
// with extra > 0 the result is a copy of ps even when nothing in it changed.
func (sc *Scoper) Walk(ps []Predicate, extra int) ([]Predicate, error) {
	copied := false
	for i, p := range ps {
		n, changed, err := sc.Node(p.n)
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

// Node returns n with the Read conditions added inside every
// EXISTS on a target that has them, and whether that changed anything. It
// never edits n: a changed node is a copy with a new kids slice, and
// unchanged subtrees are shared with n.
func (sc *Scoper) Node(n Node) (Node, bool, error) {
	switch n.Op {
	case OpAnd, OpOr, OpNot, OpJoined:
		if !n.Deep {
			return n, false, nil // no EXISTS in it: nothing to scope
		}
	case OpExists:
	default:
		return n, false, nil
	}
	// An EXISTS's relation first: an error from inside it names its target.
	// ResolveNode, for the statement's own conditions, walks the kids first;
	// the order differs on purpose, and only a stale generated relation can
	// fail here.
	var l Link
	var t *EntityInfo // the EXISTS's target
	if n.Op == OpExists {
		var err error
		if l, err = existsLink(n); err != nil {
			return n, false, err
		}
		t = l.Target
	}
	var kids []Node // nil until a kid changes
	for i, k := range n.Kids {
		nk, changed, err := sc.Node(k)
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
			kids = make([]Node, len(n.Kids), len(n.Kids)+2)
			copy(kids, n.Kids)
		}
		kids[i] = nk
	}
	resolved := false
	if n.Op == OpExists {
		// A relation is resolved here when the condition came from a policy
		// or a Rewrite hook: the statement's own were resolved by prepare.
		if _, lazy := n.V.(linkRef); lazy {
			n.V, resolved = l, true // n is a copy
		}
		read, err := sc.target(t)
		if err != nil {
			return n, false, sc.inExists(t, err)
		}
		if read.n.Op != opZero {
			if kids == nil {
				kids = make([]Node, len(n.Kids), len(n.Kids)+1)
				copy(kids, n.Kids)
			}
			for _, c := range conjuncts(NewPredicate(read.n)) {
				kids = append(kids, c.n)
			}
		}
	}
	if kids == nil {
		return n, resolved, nil
	}
	n.Kids = kids // n is a copy
	return n, true, nil
}

// target returns what an EXISTS on entity t gets: t's row condition (zero for
// none).
func (sc *Scoper) target(t *EntityInfo) (Predicate, error) {
	if !sc.filtered(t) && (!sc.conditions(t.policy) || !sc.PolicyOn()) {
		return Predicate{}, nil
	}
	// An EXISTS reads its target, whatever the statement's kind.
	return sc.RowCond(t, OpSelect)
}

// inExists returns err, an error from inside an EXISTS on t (from the EXISTS
// in its condition, or from t's Read), with t at the front of its
// path.
func (sc *Scoper) inExists(t *EntityInfo, err error) error {
	if e, ok := err.(*existsError); ok {
		e.path = slices.Insert(e.path, 0, t.Name)
		return e
	}
	return &existsError{entity: sc.Entity, path: []string{t.Name}, err: err}
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

// scope applies the entities' interceptors and policies to a statement. A
// SELECT, UPDATE or DELETE gets the entity's interceptors' conditions. Unless
// the context says WithoutPolicy, a SELECT then gets its Read policy, and an
// UPDATE or DELETE, or a Writable SELECT, its Read and Write policies. Every
// EXISTS gets its target's interceptors and Read policy the same way. scope runs after the guard and the
// Rewrite hooks, so neither sees what it adds and no hook can remove it.
//
// Raw SQL (OpRawQuery and OpRawExec) is never scoped: prepare does not call
// scope for it, as its SQL is the caller's, and its Statement.PolicyBypass
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
	if stmtUnscoped(st) {
		return nil
	}
	in := st.Entity
	sc := Scoper{Ctx: ctx, Entity: in.Name, Skip: db.bypass, Filters: db.Cfg.Filters, Reads: st.Op == OpSelect && !st.Writable}
	// The entity's own conditions first, so that a denial is reported as the
	// statement's, and an EXISTS on the same entity reuses its row condition.
	var read, write Predicate
	var err error
	pol := InfoPolicy(in)
	if sc.filtered(in) || (sc.conditions(pol) && sc.PolicyOn()) {
		if read, err = sc.RowCond(in, st.Op); err != nil {
			return err
		}
	}
	if pol != nil {
		if (st.Op != OpSelect || st.Writable) && pol.Write != nil && sc.PolicyOn() {
			op := st.Op
			if op == OpSelect {
				op = OpUpdate // a Writable SELECT reads the rows an update changes
			}
			p, err := pol.Write(ctx, op)
			if write, err = sc.PolicyCond(in, st.Op, "Write", p, err); err != nil {
				return err
			}
		}
	}
	extra := 0
	if PredNode(read).Op != opZero {
		extra++
	}
	if PredNode(write).Op != opZero {
		extra++
	}
	if st.Where, err = sc.Walk(st.Where, extra); err != nil {
		return err
	}
	// st.Where is now the scope's own array, or nothing is appended to it.
	st.Where = append(append(st.Where, conjuncts(read)...), conjuncts(write)...)
	if err := sc.scopeJoins(st); err != nil {
		return err
	}
	sc.Record(st)
	return nil
}

// scopeJoins gives each joined target its row condition, in the join's ON
// clause: a joined row is read through the target's policy as a With load
// reads it, so a join exposes nothing that a query on the target would not.
// The joins are the statement's own copy: resolve made it.
func (sc *Scoper) scopeJoins(st *Statement) error {
	if st.Op != OpSelect || len(st.Joins) == 0 {
		return nil
	}
	joins := slices.Clone(st.Joins)
	for i := range joins {
		t := joins[i].Link.Target
		read, err := sc.target(t)
		if err != nil {
			return sc.inExists(t, err)
		}
		joins[i].On = conjuncts(read)
	}
	st.Joins = joins
	return nil
}

// scopeInsert applies the entity's policy to an INSERT. InsertAllowed has
// asked the Insert policy. An upsert that updates must not reach a row the
// interceptors or the policy hide from an update: its update branch gets
// their conditions; an EXISTS a Rewrite hook put in that branch's condition
// gets its target's row condition, as one in a Where does. MySQL's ON
// DUPLICATE KEY UPDATE takes no condition, so an entity with a Read or Write
// policy or an interceptor is refused. Keeping
// the existing row (DoNothing) changes nothing, so it is exempt.
func (db *DB) scopeInsert(ctx context.Context, st *Statement) error {
	in := st.Entity
	sc := Scoper{Ctx: ctx, Entity: in.Name, Skip: db.bypass, Filters: db.Cfg.Filters}
	pol := InfoPolicy(in)
	if pol != nil && pol.Insert != nil {
		sc.PolicyOn() // records a WithoutPolicy that InsertAllowed obeyed
	}
	if cf := st.Conflict; cf != nil && !cf.DoNothing && len(cf.Update) > 0 {
		conditioned := pol != nil && (pol.Read != nil || pol.Write != nil) && sc.PolicyOn()
		if db.D == MySQL {
			if err := sc.mysqlUpsert(in, conditioned); err != nil {
				return err
			}
		}
		if err := sc.ConditionUpsert(in, st, conditioned); err != nil {
			return err
		}
		if stmtUpsertPolicy(st) && len(st.Rows) > 1 && !st.InTx {
			// conditionedUpsert runs every such InsertAll in a transaction:
			// only a Rewrite hook gets here.
			return fmt.Errorf("velox: %s: a Rewrite hook gave a multi-row upsert outside a transaction an update branch the policy conditions; a row the policy hides would fail it after the others were written", in.Name)
		}
	}
	sc.Record(st)
	return nil
}

// mysqlUpsert refuses an upsert that updates on MySQL, whose ON DUPLICATE
// KEY UPDATE takes no condition, when the update branch would get one: the
// entity has a Read or Write policy that applies (conditioned) or an
// interceptor, or a WithFilter filter returns a condition for it. A filter
// that returns NoFilter for the entity lets the upsert run.
func (sc *Scoper) mysqlUpsert(in *EntityInfo, conditioned bool) error {
	refuse := conditioned || len(in.filters) > 0
	if !refuse && len(sc.Filters) > 0 {
		p, err := sc.RowCond(in, OpUpdate)
		if err != nil {
			return err
		}
		refuse = p.n.Op != opZero
	}
	if refuse {
		return fmt.Errorf("velox: %s: %w: MySQL's ON DUPLICATE KEY UPDATE takes no condition, and the entity has a Read or Write policy or an interceptor, or a WithFilter condition; keep the existing row with DoNothing, or update it with Update or Save", in.Name, ErrPolicyUpsert)
	}
	return nil
}

// conjuncts returns the conditions p stands for when ANDed into a Where: its
// parts when p is an AND (a policy that combines two conditions, as a scope with
// rules of its own does), so that the SQL reads "a AND b AND c" and not
// "a AND (b AND c)"; p itself otherwise; none for the zero Predicate.
func conjuncts(p Predicate) []Predicate {
	switch {
	case p.n.Op == opZero:
		return nil
	case p.n.Op == OpAnd && len(p.n.Kids) > 0:
		out := make([]Predicate, len(p.n.Kids))
		for i, k := range p.n.Kids {
			out[i] = NewPredicate(k)
		}
		return out
	}
	return []Predicate{p}
}
