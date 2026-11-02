package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// ConditionUpsert scopes the condition of the update branch of st's upsert:
// every EXISTS in it, a Rewrite hook's included, gets its target's Read
// condition, as in a Where, and with policy it gains entity in's Read
// and Write conditions. It works on a copy of the clause: the caller's
// InsertQuery may share it with other runs. A denial is the upsert's, as an
// update's.
func (sc *Scoper) ConditionUpsert(in *EntityInfo, st *Statement, policy bool) error {
	var read, write Predicate
	var err error
	// The entity's own policy first, as in scope.
	if policy && in.policy.Read != nil {
		if read, err = sc.ReadOf(in, OpUpdate); err != nil {
			return err
		}
	}
	if policy && in.policy.Write != nil {
		p, err := in.policy.Write(sc.Ctx)
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
		st.PolicyBypass = sc.skip
	}
}

// Scoper is one statement's scope: the context's WithoutPolicy, looked up when
// an entity first needs it, and the Read conditions made so far.
type Scoper struct {
	Ctx    context.Context
	Entity string // the statement's entity, which every error names

	skipRead bool   // skip is looked up
	skip     string // the WithoutPolicy reason
	skipped  bool   // a policy that would have applied was skipped

	reads     []readCond
	computing []*EntityInfo // the entities whose Read is being made, outermost first
}

// readCond is an entity's Read condition, made once per statement.
type readCond struct {
	entity *EntityInfo
	p      Predicate
}

// PolicyOn reports whether a policy applies: false under WithoutPolicy, which
// is then recorded as applied.
func (sc *Scoper) PolicyOn() bool {
	if !sc.skipRead {
		sc.skip = PolicyBypassReason(sc.Ctx)
		sc.skipRead = true
	}
	if sc.skip != "" {
		sc.skipped = true
		return false
	}
	return true
}

// ReadOf returns entity t's Read condition, calling its policy the first time
// only; a denial is a *PolicyError for op. The EXISTS in the condition get
// their targets' Read conditions in turn; a condition that reaches t's own
// Read again, directly or through other entities, cannot be made, and the
// statement is refused.
func (sc *Scoper) ReadOf(t *EntityInfo, op Op) (Predicate, error) {
	for _, r := range sc.reads {
		if r.entity == t {
			return r.p, nil
		}
	}
	if slices.Contains(sc.computing, t) {
		return Predicate{}, &cycleError{entity: sc.Entity, policy: t.Name}
	}
	sc.computing = append(sc.computing, t)
	p, err := t.policy.Read(sc.Ctx)
	p, err = sc.PolicyCond(t, op, "Read", p, err)
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
		return Predicate{}, fmt.Errorf("velox: %s: the %s policy returned the zero Pred; return velox.Allow[E]() for no restriction", t.Name, which)
	case p.n.Op == OpAllow:
		return Predicate{}, nil // no restriction
	case hasZero(p.n):
		return Predicate{}, fmt.Errorf("velox: %s: the %s policy's condition holds the zero Pred", t.Name, which)
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
		if _, lazy := n.V.(RelRef); lazy {
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

// target returns what an EXISTS on entity t gets: t's Read condition (zero for
// none).
func (sc *Scoper) target(t *EntityInfo) (Predicate, error) {
	if t.policy == nil || t.policy.Read == nil || !sc.PolicyOn() {
		return Predicate{}, nil
	}
	// An EXISTS reads its target, whatever the statement's kind.
	return sc.ReadOf(t, OpSelect)
}

// inExists returns err, an error from inside an EXISTS on t (from the EXISTS
// in its condition, or from t's Read), with t at the front of its
// path. A cycle passes unchanged: it names the statement's entity already.
func (sc *Scoper) inExists(t *EntityInfo, err error) error {
	switch e := err.(type) {
	case *cycleError:
		return err
	case *existsError:
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

// scope applies the entities' policies to a statement. Unless the context says
// WithoutPolicy, a SELECT gets the entity's Read policy and an UPDATE or
// DELETE, or a Writable SELECT, its Read and Write policies. Every EXISTS gets
// its target's Read policy the same way. scope runs after the guard and the
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
	in := st.Entity
	sc := Scoper{Ctx: ctx, Entity: in.Name}
	// The entity's own policy first, so that its denial is reported as the
	// statement's, and an EXISTS on the same entity reuses its Read.
	var read, write Predicate
	var err error
	if pol := InfoPolicy(in); pol != nil {
		if pol.Read != nil && sc.PolicyOn() {
			if read, err = sc.ReadOf(in, st.Op); err != nil {
				return err
			}
		}
		if (st.Op != OpSelect || st.Writable) && pol.Write != nil && sc.PolicyOn() {
			p, err := pol.Write(ctx)
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

// scopeJoins gives each joined target its Read condition, in the join's ON
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
// policy hides from an update: its update branch gets the Read and Write
// conditions; an EXISTS a Rewrite hook put in that branch's condition gets its
// target's Read, as one in a Where does. MySQL's ON DUPLICATE KEY UPDATE takes
// no condition, so an entity with a Read or Write policy is refused. Keeping
// the existing row (DoNothing) changes nothing, so it is exempt.
func (db *DB) scopeInsert(ctx context.Context, st *Statement) error {
	in := st.Entity
	sc := Scoper{Ctx: ctx, Entity: in.Name}
	pol := InfoPolicy(in)
	if pol != nil && pol.Insert != nil {
		sc.PolicyOn() // records a WithoutPolicy that InsertAllowed obeyed
	}
	if cf := st.Conflict; cf != nil && !cf.DoNothing && len(cf.Update) > 0 {
		conditioned := pol != nil && (pol.Read != nil || pol.Write != nil) && sc.PolicyOn()
		if conditioned && db.D == MySQL {
			return fmt.Errorf("velox: %s: %w: MySQL's ON DUPLICATE KEY UPDATE takes no condition, and the entity has a Read or Write policy; keep the existing row with DoNothing, or update it with Update or Save", in.Name, ErrPolicyUpsert)
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
