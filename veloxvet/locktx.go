package veloxvet

import (
	"fmt"
	"go/constant"
	"go/token"
	"go/types"
	"slices"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ssa"
)

// LockTx reports a locking read (ForUpdate or ForShare) whose database
// handle is not provably a transaction's. It is off unless its flag
// -locktx.enable is set, and it ignores the transaction a context carries:
// see its Doc.
var LockTx = &analysis.Analyzer{
	Name:      "locktx",
	Doc:       lockTxDoc,
	URL:       "https://pkg.go.dev/github.com/syssam/velox/veloxvet#LockTx",
	Run:       runLockTx,
	FactTypes: []analysis.Fact{(*LocksOnParam)(nil), (*TxCallback)(nil), (*QueryFrom)(nil)},
}

// lockTxEnabled is LockTx's -enable flag.
var lockTxEnabled bool

func init() {
	LockTx.Flags.BoolVar(&lockTxEnabled, "enable", false,
		"run the locktx check, which is off without this flag")
}

const lockTxDoc = `report a locking read whose handle is not provably a transaction's

This check is off unless its flag is set: -locktx.enable, as in
go vet -vettool=$(which veloxvet) -locktx.enable ./..., which runs it
beside the other analyzers. (-locktx alone selects it and turns the others
off, and it still does nothing without -locktx.enable.)

velox refuses a locking read (ForUpdate or ForShare) outside a
transaction: "a locking read runs only inside Tx". A read with no
transaction can still run in one, as a plain DB joins the transaction its
context carries, so no sound check can tell. This one is strict instead:
it ignores the context and asks that the handle the read's query started
from be provably a transaction's. It is for code that passes transaction
handles explicitly.

A locking read is All, One, First, Iter, Exists, IDs, Values, Rows, SQL or
Paginate of a query, velox.Scan or velox.ScanOne of one, or All, One,
First, Exists, Rows or SQL of an untyped TableQuery, when ForUpdate or
ForShare was applied to it on some path. The check follows the query back
through the function's local variables, reassignments, loops and branches
to the handle it started from: the receiver of db.Query, the argument of
untyped.Query, or the parameter a function returning a query started it
from (a QueryFrom fact, with the conditions of its lock). The handle is a
transaction's when it is a *velox.DB parameter of a function literal given
to DB.Tx, or to a function, or a function literal, that hands its
parameter only a transaction (a TxCallback fact; comparing the parameter
with nil is allowed), when it is velox.TxFrom's or velox.UseTx's result,
or WithoutPolicy or Record of such a handle.

A handle that is a parameter of the enclosing function, a query that is
one, or a field of a parameter other than the receiver (an input struct's
Tx, or a field of a field, in.Tx), is not reported there: the function
gets a LocksOnParam fact, and each call of it, in this package or another,
recursive calls included, is checked in its place, with the argument it
passes, or what the struct literal or variable it passes stores in the
field. A struct whose address goes anywhere else, or that is a call's
result, holds a handle the check cannot follow. Any other handle, such as
a field of the receiver (s.client), a package variable, a call's result,
an entity's edge query or a function literal's parameter that is not a
transaction's, is reported at the read or at the call. So is a handle of
a struct field inside a Tx closure: velox would accept that read, as the
context joins, but the check does not.

A lock taken only under a condition on a parameter, as in
if lock { q = q.ForUpdate() } or if mode == lockRows {...}, with a bool
parameter or one compared with a constant, is recorded with its
condition. A call that passes a constant under which the lock is not
taken is not reported. A condition on anything else, or an argument that
is not a constant, counts as taking the lock.

Calls through an interface or a function value are not followed, nor a
locked query passed to another function to read.

A comment //velox:lock-ok, best with a reason, on any line of the
statement that holds the read or the call, or alone on the line before
the statement, waives it: nothing is reported there and nothing is passed
on to the callers. For a read or a call in the header of an if, for or
switch statement, or of a case, only the header counts. The lines inside
a function literal's body belong to the literal's statements, not to the
statement around it.`

// LocksOnParam is the fact of a function that takes a row lock on a
// handle it is given, or on one of its own under a condition on its
// parameters: each call of it is checked as the read would be.
type LocksOnParam struct {
	Locks []ParamLock
}

// ParamLock is a lock of a LocksOnParam fact.
type ParamLock struct {
	// Param is the index of the parameter that holds the handle or the
	// query, the receiver first; -1 for a handle that is not a parameter.
	Param int
	// Field is the name of the parameter's field that holds the handle,
	// when the parameter is a struct or a pointer to one; "" when the
	// parameter is the handle or the query.
	Field string
	// Handle says what the handle is, for Param -1.
	Handle string
	// When are the conditions under which the lock is taken: when every
	// LockCond of one of them holds. An empty one always holds.
	When [][]LockCond
}

// LockCond is a condition on a parameter: it holds when the parameter's
// value equals the constant Value, if Equal, or differs from it.
type LockCond struct {
	// Param is the parameter's index, the receiver first.
	Param int
	// Equal tells whether the parameter equals Value or differs from it.
	Equal bool
	// Value is the constant, as constant.Value's ExactString.
	Value string
	// Kind is the constant's kind: Bool, Int or String.
	Kind constant.Kind
}

// AFact marks LocksOnParam as a go/analysis fact.
func (*LocksOnParam) AFact() {}

// String names the fact in analysistest's expectations, with p and the
// index for a parameter and "own" for Param -1.
func (f *LocksOnParam) String() string {
	var locks []string
	for _, l := range f.Locks {
		s := "own"
		if l.Param >= 0 {
			s = fmt.Sprintf("p%d", l.Param)
		}
		if l.Field != "" {
			s += "." + l.Field
		}
		locks = append(locks, s+whenString(l.When))
	}
	return "LocksOnParam(" + strings.Join(locks, ", ") + ")"
}

// TxCallback is the fact of a function some of whose parameters are
// functions that it calls only with a transaction's handle, or passes on
// only to DB.Tx or to such a parameter: a function literal given there is
// a transaction's closure.
type TxCallback struct {
	// Params are the parameters' indexes, the receiver first.
	Params []int
}

// AFact marks TxCallback as a go/analysis fact.
func (*TxCallback) AFact() {}

// String names the fact in analysistest's expectations.
func (f *TxCallback) String() string { return "TxCallback(" + indexes(f.Params) + ")" }

// QueryFrom is the fact of a function that returns a query started from
// the handle, or the query, of some of its parameters on every path.
type QueryFrom struct {
	// Params are the parameters' indexes, the receiver first.
	Params []int
	// Locked tells whether some path applies ForUpdate or ForShare.
	Locked bool
	// When are the conditions under which the returned query is locked,
	// as ParamLock's When: an empty one always holds.
	When [][]LockCond
}

// AFact marks QueryFrom as a go/analysis fact.
func (*QueryFrom) AFact() {}

// String names the fact in analysistest's expectations.
func (f *QueryFrom) String() string {
	s := indexes(f.Params)
	if f.Locked {
		s += " locked" + whenString(f.When)
	}
	return "QueryFrom(" + s + ")"
}

// whenString spells conditions as LocksOnParam's String does: "" when
// they always hold.
func whenString(when [][]LockCond) string {
	if always(when) {
		return ""
	}
	var gates []string
	for _, g := range when {
		var cs []string
		for _, c := range g {
			op := "!="
			if c.Equal {
				op = "=="
			}
			cs = append(cs, fmt.Sprintf("p%d%s%s", c.Param, op, c.Value))
		}
		gates = append(gates, strings.Join(cs, " and "))
	}
	return " when " + strings.Join(gates, " or ")
}

func indexes(ps []int) string {
	var s []string
	for _, p := range ps {
		s = append(s, fmt.Sprintf("p%d", p))
	}
	return strings.Join(s, " ")
}

// lockApply are the methods that make a query a locking read.
var lockApply = map[string]bool{
	veloxPath + ".Query.ForUpdate":       true,
	veloxPath + ".Query.ForShare":        true,
	enginePath + ".TableQuery.ForUpdate": true,
	enginePath + ".TableQuery.ForShare":  true,
}

// lockReads are the reads that take a query's lock, by the index of the
// query among the call's arguments (the receiver is the first). Count and
// the aggregates refuse a lock whatever the handle, and LoadEach and
// CountEach refuse one too. TestTablesMatchAPI checks this table.
var lockReads = func() map[string]int {
	m := map[string]int{veloxPath + ".Scan": 1, veloxPath + ".ScanOne": 1}
	for _, n := range []string{"All", "One", "First", "Iter", "Exists", "IDs", "Values", "Rows", "SQL", "Paginate"} {
		m[veloxPath+".Query."+n] = 0
	}
	for _, n := range []string{"All", "One", "First", "Exists", "Rows", "SQL"} {
		m[enginePath+".TableQuery."+n] = 0
	}
	return m
}()

// txOpeners are the functions that begin a transaction and give its handle
// to a function, by that function's index among the call's arguments.
var txOpeners = map[string]int{veloxPath + ".DB.Tx": 2}

// txResults are the functions whose first result is a transaction's handle.
var txResults = map[string]bool{veloxPath + ".TxFrom": true, veloxPath + ".UseTx": true}

// sameHandle are the methods that return a DB in the transaction of their
// receiver, if it is in one.
var sameHandle = map[string]bool{veloxPath + ".DB.WithoutPolicy": true, veloxPath + ".DB.Record": true}

// queryHandle are the functions that start a query, by the index of its
// handle among the call's arguments.
var queryHandle = map[string]int{
	veloxPath + ".DB.Query":    0,
	untypedPath + ".Query":     0,
	enginePath + ".QueryTable": 0,
}

// lockDirective waives a LockTx report.
const lockDirective = "//velox:lock-ok"

// handle is where a database handle can come from, on the paths to it.
type handle struct {
	tx     bool     // a transaction's
	params []pref   // a parameter of the enclosing named function, or its field
	others []string // anything else, said in words
}

func (h *handle) merge(o *handle) {
	if o == nil {
		return
	}
	h.tx = h.tx || o.tx
	for _, p := range o.params {
		if !slices.Contains(h.params, p) {
			h.params = append(h.params, p)
		}
	}
	for _, s := range o.others {
		if !slices.Contains(h.others, s) {
			h.others = append(h.others, s)
		}
	}
}

// txOnly reports whether h is a transaction's on every path.
func (h *handle) txOnly() bool { return h.tx && len(h.params) == 0 && len(h.others) == 0 }

func other(s string) *handle { return &handle{others: []string{s}} }

// pref is a parameter of the enclosing named function, by its index (the
// receiver first), or a field of it, a struct or a pointer to one.
type pref struct {
	param int
	field string
}

func param(i int, field string) *handle { return &handle{params: []pref{{i, field}}} }

// qpath is a path to a query: the handle it started from and a call of
// ForUpdate or ForShare on the path, nil when there is none, with the
// conditions, besides those around the call, under which it locks: a
// helper's, from its QueryFrom.
type qpath struct {
	h    *handle
	lock ssa.Instruction
	gate []LockCond
}

// key identifies p among a value's paths.
func (p qpath) key() string {
	return fmt.Sprintf("%s|%p|%v", p.h.key(), p.lock, p.gate)
}

// key identifies h's contents.
func (h *handle) key() string {
	ps := make([]string, 0, len(h.params))
	for _, p := range h.params {
		ps = append(ps, fmt.Sprintf("%d.%s", p.param, p.field))
	}
	slices.Sort(ps)
	others := slices.Clone(h.others)
	slices.Sort(others)
	return fmt.Sprintf("%v|%v|%v", h.tx, ps, others)
}

// maxPaths and maxGates bound the work on one value and one lock;
// maxRounds bounds the passes over a cycle and over the package.
const (
	maxPaths  = 64
	maxGates  = 8
	maxRounds = 16
)

// noLow is lockChecker.low when no open computation was reached.
const noLow = int(^uint(0) >> 1)

// lockSum is what LockTx knows of a named function of the package, its
// function literals included.
type lockSum struct {
	done  bool
	own   bool // the function can carry a fact
	locks []ParamLock
	diags []analysis.Diagnostic
}

// lockChecker checks one package.
type lockChecker struct {
	pass  *analysis.Pass
	pkg   *ssa.Package
	ok    directives
	sums  map[*ssa.Function]*lockSum
	cbs   map[*ssa.Function][]int
	qfs   map[*ssa.Function]*QueryFrom
	txFn  map[*ssa.Function]int8 // 1 a transaction's closure, -1 not, 0 being computed
	hand  map[ssa.Value]*handle
	paths map[ssa.Value][]qpath
	seen  map[token.Pos]bool
	litCB map[*ssa.Function]bool // literals whose parameters are being checked

	// The walks of provenance and queryPaths cache only what they
	// computed with no open walk outside them: depth is the open walks'
	// count, openH and openP their depths, provH and provP the answers
	// they have so far, and low the lowest depth of an open walk that a
	// walk reached.
	depth        int
	low          int
	openH, openP map[ssa.Value]int
	provH        map[ssa.Value]*handle
	provP        map[ssa.Value][]qpath

	// prev are the locks of the previous pass over the package, which a
	// call of a function whose summary is being computed sees; recur
	// tells that a pass saw one.
	prev  map[*ssa.Function][]ParamLock
	recur bool
}

func runLockTx(pass *analysis.Pass) (any, error) {
	if !lockTxEnabled || !reaches(pass.Pkg, veloxPath, map[*types.Package]bool{}) {
		return nil, nil
	}
	pkg, funcs := buildSSA(pass)
	c := &lockChecker{
		pass: pass, pkg: pkg, ok: findDirectives(pass, lockDirective),
		sums: map[*ssa.Function]*lockSum{}, cbs: map[*ssa.Function][]int{},
		qfs: map[*ssa.Function]*QueryFrom{}, txFn: map[*ssa.Function]int8{},
		hand: map[ssa.Value]*handle{}, paths: map[ssa.Value][]qpath{}, seen: map[token.Pos]bool{},
		litCB: map[*ssa.Function]bool{},
		low:   noLow, openH: map[ssa.Value]int{}, openP: map[ssa.Value]int{},
		provH: map[ssa.Value]*handle{}, provP: map[ssa.Value][]qpath{},
		prev: map[*ssa.Function][]ParamLock{},
	}
	var roots []*ssa.Function
	for _, fn := range funcs {
		if fn.Parent() == nil {
			roots = append(roots, fn)
		}
	}
	// A function that calls one whose summary is being computed, through
	// recursion, sees that one's locks from the previous pass; the passes
	// go on until no lock changes.
	for round := 0; round < maxRounds; round++ {
		c.sums, c.seen, c.recur = map[*ssa.Function]*lockSum{}, map[token.Pos]bool{}, false
		for _, fn := range roots {
			c.summary(fn)
		}
		if !c.recur {
			break
		}
		changed := false
		for fn, s := range c.sums {
			if (&LocksOnParam{Locks: s.locks}).String() != (&LocksOnParam{Locks: c.prev[fn]}).String() {
				changed = true
			}
			c.prev[fn] = s.locks
		}
		if !changed {
			break
		}
	}
	for _, fn := range roots {
		s := c.summary(fn)
		obj, ok := fn.Object().(*types.Func)
		if !ok || obj.Pkg() != pass.Pkg {
			continue
		}
		if len(s.locks) > 0 {
			pass.ExportObjectFact(obj, &LocksOnParam{Locks: s.locks})
		}
		if cb := c.callbacks(fn); len(cb) > 0 {
			pass.ExportObjectFact(obj, &TxCallback{Params: cb})
		}
		if qf := c.queryFrom(fn); qf != nil {
			pass.ExportObjectFact(obj, qf)
		}
	}
	for _, fn := range roots {
		for _, d := range c.summary(fn).diags {
			pass.Report(d)
		}
	}
	return nil, nil
}

// root is the named function, or the package's init, that holds fn.
func root(fn *ssa.Function) *ssa.Function {
	for fn.Parent() != nil {
		fn = fn.Parent()
	}
	return fn
}

// summary computes, once a pass, a named function's locks and reports. A
// recursive call sees the function's locks of the previous pass while it
// is being computed, and runLockTx passes again until they hold.
func (c *lockChecker) summary(fn *ssa.Function) *lockSum {
	if s, ok := c.sums[fn]; ok {
		if !s.done {
			c.recur = true
			return &lockSum{done: true, locks: c.prev[fn]}
		}
		return s
	}
	s := &lockSum{}
	if obj, ok := fn.Object().(*types.Func); ok && obj.Pkg() == c.pass.Pkg {
		s.own = true
	}
	c.sums[fn] = s
	var visit func(f *ssa.Function)
	visit = func(f *ssa.Function) {
		for _, b := range f.Blocks {
			for _, ins := range b.Instrs {
				if call, ok := ins.(ssa.CallInstruction); ok {
					c.checkCall(s, call)
				}
			}
		}
		for _, a := range f.AnonFuncs {
			visit(a)
		}
	}
	visit(fn)
	s.done = true
	return s
}

// checkCall checks a locking read, or a call of a function that locks on
// its parameters.
func (c *lockChecker) checkCall(s *lockSum, call ssa.CallInstruction) {
	cm := call.Common()
	fn, obj := callee(cm)
	if obj == nil {
		return
	}
	if i, ok := lockReads[funcKey(obj)]; ok {
		q := arg(cm, i)
		if q == nil || c.ok.waives(c.pass, call.Pos()) {
			return
		}
		name := obj.Name()
		if obj.Signature().Recv() == nil {
			name = "velox." + name
		}
		// The paths that lock under one gate are judged together, so that
		// a report names every handle that is not a transaction's.
		var gates [][]LockCond
		var hs []*handle
		site := c.conds(call)
		for _, p := range c.queryPaths(q) {
			if p.lock == nil {
				continue
			}
			gate, live := normalize(slices.Concat(p.gate, c.conds(p.lock), site))
			if !live {
				continue
			}
			i := slices.IndexFunc(gates, func(g []LockCond) bool { return slices.Equal(g, gate) })
			if i < 0 {
				gates, hs = append(gates, gate), append(hs, &handle{})
				i = len(gates) - 1
			}
			hs[i].merge(p.h)
		}
		for i, gate := range gates {
			c.oblige(s, hs[i], gate, call, func(h string) string {
				return fmt.Sprintf("%s takes a row lock (ForUpdate or ForShare) on %s, which is not provably a transaction's handle: "+
					"run it in db.Tx on the closure's tx, or take the handle as a parameter", name, h)
			})
		}
		return
	}
	f := c.locksOf(fn, obj)
	if f == nil || c.ok.waives(c.pass, call.Pos()) {
		return
	}
	site := c.conds(call)
	for _, l := range f.Locks {
		var h *handle
		var what string
		if l.Param >= 0 {
			a := arg(cm, l.Param)
			if a == nil {
				continue
			}
			what = "its parameter " + paramName(obj, l.Param) + ", given "
			if l.Field != "" {
				what = "its parameter " + paramName(obj, l.Param) + "'s field " + l.Field + ", given "
			}
			h = c.fieldProvenance(a, l.Field, call)
		} else {
			h, what = other(l.Handle), "its own handle, "
		}
		for _, g := range l.When {
			gate, live := c.translate(g, cm)
			if !live {
				continue
			}
			c.oblige(s, h, append(gate, site...), call, func(h string) string {
				return fmt.Sprintf("%s takes a row lock (ForUpdate or ForShare) on %s%s, which is not provably a transaction's handle: "+
					"call it in db.Tx with the closure's tx, or take the handle as a parameter", obj.Name(), what, h)
			})
		}
	}
}

// paramName names obj's i-th parameter, the receiver first.
func paramName(obj *types.Func, i int) string {
	sig := obj.Signature()
	if sig.Recv() != nil {
		if i == 0 {
			return nameOr(sig.Recv().Name(), "the receiver")
		}
		i--
	}
	if i < sig.Params().Len() {
		return nameOr(sig.Params().At(i).Name(), fmt.Sprintf("#%d", i+1))
	}
	return fmt.Sprintf("#%d", i+1)
}

func nameOr(s, or string) string {
	if s == "" || s == "_" {
		return or
	}
	return s
}

// oblige records a lock taken under gate on a handle from h: a
// parameter's lock goes into the function's fact, any other is reported,
// unless gate depends on the function's parameters, which the callers
// then decide.
func (c *lockChecker) oblige(s *lockSum, h *handle, gate []LockCond, call ssa.CallInstruction, msg func(string) string) {
	gate, live := normalize(gate)
	if !live {
		return
	}
	for _, p := range h.params {
		s.addLock(p.param, p.field, "", gate)
	}
	if len(h.others) == 0 {
		return
	}
	if len(gate) > 0 && s.own {
		s.addLock(-1, "", h.others[0], gate)
		return
	}
	if c.seen[call.Pos()] {
		return
	}
	c.seen[call.Pos()] = true
	s.diags = append(s.diags, analysis.Diagnostic{Pos: call.Pos(), Message: msg(strings.Join(h.others, " or "))})
}

// addLock adds a lock on param, or its field, under gate to s.
func (s *lockSum) addLock(param int, field, desc string, gate []LockCond) {
	i := slices.IndexFunc(s.locks, func(l ParamLock) bool { return l.Param == param && l.Field == field })
	if i < 0 {
		s.locks = append(s.locks, ParamLock{Param: param, Field: field, Handle: desc})
		i = len(s.locks) - 1
	}
	l := &s.locks[i]
	switch {
	case always(l.When):
	case len(gate) == 0 || len(l.When) >= maxGates:
		l.When = [][]LockCond{{}}
	default:
		if !slices.ContainsFunc(l.When, func(g []LockCond) bool { return slices.Equal(g, gate) }) {
			l.When = append(l.When, gate)
			slices.SortFunc(l.When, func(a, b []LockCond) int { return slices.CompareFunc(a, b, compareCond) })
		}
	}
}

// always reports whether the conditions when always hold.
func always(when [][]LockCond) bool {
	return slices.ContainsFunc(when, func(g []LockCond) bool { return len(g) == 0 })
}

// normalize drops the repeated conditions of gate and reports whether
// they can all hold.
func normalize(gate []LockCond) ([]LockCond, bool) {
	var out []LockCond
	for _, c := range gate {
		if slices.Contains(out, c) {
			continue
		}
		for _, d := range out {
			if c.Param != d.Param || c.Kind != d.Kind {
				continue
			}
			switch {
			case c.Equal && d.Equal && c.Value != d.Value,
				c.Equal != d.Equal && c.Value == d.Value:
				return nil, false
			}
		}
		out = append(out, c)
	}
	slices.SortFunc(out, compareCond)
	return out, true
}

// compareCond orders conditions by parameter, value and Equal, so that a
// gate and a fact have one spelling.
func compareCond(a, b LockCond) int {
	if a.Param != b.Param {
		return a.Param - b.Param
	}
	if a.Value != b.Value {
		return strings.Compare(a.Value, b.Value)
	}
	switch {
	case a.Equal == b.Equal:
		return 0
	case !a.Equal:
		return -1
	}
	return 1
}

// translate is a callee's gate at a call: a condition on a constant
// argument is decided, one on the caller's parameter becomes the caller's,
// and any other is dropped, as if it held. live is false when a condition
// does not hold.
func (c *lockChecker) translate(g []LockCond, cm *ssa.CallCommon) ([]LockCond, bool) {
	var out []LockCond
	for _, cond := range g {
		a := arg(cm, cond.Param)
		if k, ok := a.(*ssa.Const); ok {
			if holds, known := evalCond(cond, k); known {
				if !holds {
					return nil, false
				}
				continue
			}
		}
		if i, ok := c.rootParam(a); ok {
			cond.Param = i
			out = append(out, cond)
		}
	}
	return out, true
}

// evalCond decides cond on the constant k.
func evalCond(cond LockCond, k *ssa.Const) (holds, known bool) {
	v, kind, ok := constKey(k)
	if !ok || kind != cond.Kind {
		return false, false
	}
	return (v == cond.Value) == cond.Equal, true
}

// constKey is k's value and kind, for a bool, integer or string constant.
func constKey(k *ssa.Const) (string, constant.Kind, bool) {
	if k == nil || k.Value == nil {
		return "", 0, false
	}
	switch kind := k.Value.Kind(); kind {
	case constant.Bool, constant.Int, constant.String:
		return k.Value.ExactString(), kind, true
	}
	return "", 0, false
}

// rootParam returns the index of the parameter of the enclosing named
// function that v is: the parameter, or a function literal's free variable
// bound to it.
func (c *lockChecker) rootParam(v ssa.Value) (int, bool) {
	switch v := v.(type) {
	case *ssa.Parameter:
		fn := v.Parent()
		if fn.Parent() == nil {
			return slices.Index(fn.Params, v), true
		}
	case *ssa.FreeVar:
		bs := c.bindings(v)
		if len(bs) == 1 {
			return c.rootParam(bs[0])
		}
	case *ssa.UnOp:
		// A load of a variable that holds only the parameter: one
		// that a function literal captures, which go/ssa spills.
		if v.Op != token.MUL {
			break
		}
		if vals, ok := c.stored(v.X); ok && len(vals) == 1 {
			if _, ok := vals[0].(*ssa.Parameter); ok {
				return c.rootParam(vals[0])
			}
		}
	case *ssa.Alloc:
		// A struct parameter whose field the function addresses: its
		// spilled copy, when nothing else is stored in it.
		if vals, ok := c.stored(v); ok && len(vals) == 1 {
			if _, ok := vals[0].(*ssa.Parameter); ok {
				return c.rootParam(vals[0])
			}
		}
	}
	return 0, false
}

// conds are the conditions on the enclosing named function's parameters
// under which ins runs: those of the if statements whose one branch
// dominates it, and, in a function literal made at one place, those of the
// place.
func (c *lockChecker) conds(ins ssa.Instruction) []LockCond {
	var out []LockCond
	for ins != nil {
		b := ins.Block()
		for a := b.Idom(); a != nil; a = a.Idom() {
			iff, ok := a.Instrs[len(a.Instrs)-1].(*ssa.If)
			if !ok {
				continue
			}
			for i, s := range a.Succs {
				if len(s.Preds) == 1 && s.Dominates(b) {
					if cd, ok := c.condOf(iff.Cond, i == 0); ok {
						out = append(out, cd)
					}
				}
			}
		}
		fn := ins.Parent()
		ins = nil
		if fn.Parent() != nil {
			if sites := c.sites(fn); len(sites) == 1 {
				ins = sites[0]
			}
		}
	}
	return out
}

// condOf is the condition that v is taken, or not, on a parameter.
func (c *lockChecker) condOf(v ssa.Value, taken bool) (LockCond, bool) {
	switch v := v.(type) {
	case *ssa.UnOp:
		if v.Op == token.NOT {
			return c.condOf(v.X, !taken)
		}
	case *ssa.BinOp:
		if v.Op != token.EQL && v.Op != token.NEQ {
			return LockCond{}, false
		}
		x, y := v.X, v.Y
		if _, ok := x.(*ssa.Const); ok {
			x, y = y, x
		}
		k, ok := y.(*ssa.Const)
		if !ok {
			return LockCond{}, false
		}
		i, ok := c.rootParam(x)
		if !ok {
			return LockCond{}, false
		}
		val, kind, ok := constKey(k)
		if !ok {
			return LockCond{}, false
		}
		return LockCond{Param: i, Equal: (v.Op == token.EQL) == taken, Value: val, Kind: kind}, true
	}
	if b, ok := v.Type().Underlying().(*types.Basic); !ok || b.Info()&types.IsBoolean == 0 {
		return LockCond{}, false
	}
	i, ok := c.rootParam(v)
	if !ok {
		return LockCond{}, false
	}
	return LockCond{Param: i, Equal: taken, Value: "true", Kind: constant.Bool}, true
}

// locksOf is the LocksOnParam of a call's function: by its summary in this
// package, else by its fact. nil when it has none.
func (c *lockChecker) locksOf(fn *ssa.Function, obj *types.Func) *LocksOnParam {
	if fn != nil && fn.Pkg == c.pkg && len(fn.Blocks) > 0 {
		if fn.Parent() != nil {
			return nil
		}
		if s := c.summary(fn); len(s.locks) > 0 {
			return &LocksOnParam{Locks: s.locks}
		}
		return nil
	}
	var f LocksOnParam
	if obj.Pkg() != c.pass.Pkg && c.pass.ImportObjectFact(obj, &f) {
		return &f
	}
	return nil
}

// isDB reports whether t is *velox.DB or *engine.DB.
func isDB(t types.Type) bool {
	p, ok := types.Unalias(t).(*types.Pointer)
	if !ok {
		return false
	}
	n, ok := types.Unalias(p.Elem()).(*types.Named)
	if !ok || n.Obj().Pkg() == nil {
		return false
	}
	switch n.Obj().Pkg().Path() + "." + n.Obj().Name() {
	case veloxPath + ".DB", enginePath + ".DB":
		return true
	}
	return false
}

// provenance is where the handle v, a DB or a query, can come from.
//
// A value on a cycle (a phi of a loop, a variable stored back into
// itself) reaches its own walk while it is open. The walk then sees the
// answer so far, starting empty, and marks low; the value at the head of
// the cycle walks again with its larger answer until it stops growing.
// A value whose walk reached an open walk outside it is not cached: its
// answer may lack what that walk has yet to find.
func (c *lockChecker) provenance(v ssa.Value) *handle {
	if isQuery(v.Type()) {
		h := &handle{}
		for _, p := range c.queryPaths(v) {
			h.merge(p.h)
		}
		return h
	}
	if h, ok := c.hand[v]; ok {
		return h
	}
	if d, ok := c.openH[v]; ok {
		c.low = min(c.low, d)
		return c.provH[v]
	}
	c.depth++
	d, outer := c.depth, c.low
	c.openH[v], c.provH[v] = d, &handle{}
	var h *handle
	done := false
	for round := 0; ; round++ {
		c.low = noLow
		h = c.provenance1(v)
		if c.low != d {
			done = c.low > d
			break
		}
		if h.key() == c.provH[v].key() || round == maxRounds {
			done = true
			break
		}
		c.provH[v] = h
	}
	low := c.low
	delete(c.openH, v)
	delete(c.provH, v)
	c.depth--
	if done {
		c.hand[v] = h
		c.low = outer
	} else {
		c.low = min(outer, low)
	}
	return h
}

func (c *lockChecker) provenance1(v ssa.Value) *handle {
	switch v := v.(type) {
	case *ssa.Parameter:
		fn := v.Parent()
		if fn.Parent() == nil {
			return param(slices.Index(fn.Params, v), "")
		}
		if isDB(v.Type()) && c.txClosure(fn) {
			return &handle{tx: true}
		}
		return other("a function literal's parameter " + v.Name())
	case *ssa.FreeVar:
		h := &handle{}
		bs := c.bindings(v)
		if len(bs) == 0 {
			return other("a free variable the check cannot follow")
		}
		for _, b := range bs {
			h.merge(c.provenance(b))
		}
		return h
	case *ssa.Phi:
		h := &handle{}
		for _, e := range v.Edges {
			h.merge(c.provenance(e))
		}
		return h
	case *ssa.ChangeType:
		return c.provenance(v.X)
	case *ssa.Const:
		return &handle{} // nil
	case *ssa.UnOp:
		if v.Op == token.MUL {
			return c.loaded(v.X, c.provenance)
		}
	case *ssa.Extract:
		if call, ok := v.Tuple.(*ssa.Call); ok {
			if _, obj := callee(call.Common()); obj != nil {
				if v.Index == 0 && txResults[funcKey(obj)] {
					return &handle{tx: true}
				}
				return other("the result of " + obj.Name())
			}
		}
		return other("a call's result")
	case *ssa.Call:
		if _, obj := callee(v.Common()); obj != nil {
			if sameHandle[funcKey(obj)] {
				return c.provenance(v.Common().Args[0])
			}
			return other("the result of " + obj.Name())
		}
		return other("a call's result")
	case *ssa.Field:
		if i, path, ok := c.fieldPath(v, 0); ok {
			return param(i, path)
		}
		return other("the field " + fieldName(v.X.Type(), v.Field))
	}
	return other("a value the check cannot follow")
}

// fieldPath returns the parameter, not the receiver, whose field, or
// field of a field, v is (a Field, a FieldAddr or a load of one), and the
// path to it, its names joined by dots: "Tx", "in.Tx". A field the
// function stores to is not followed.
func (c *lockChecker) fieldPath(v ssa.Value, depth int) (int, string, bool) {
	if depth > 4 {
		return 0, "", false
	}
	var x ssa.Value
	var name string
	switch v := v.(type) {
	case *ssa.Field:
		x, name = v.X, fieldName(v.X.Type(), v.Field)
	case *ssa.FieldAddr:
		if c.fieldStored(v) {
			return 0, "", false
		}
		x, name = v.X, fieldName(v.X.Type(), v.Field)
	case *ssa.UnOp:
		if fa, ok := v.X.(*ssa.FieldAddr); ok && v.Op == token.MUL {
			return c.fieldPath(fa, depth)
		}
		return 0, "", false
	default:
		return 0, "", false
	}
	if a, ok := x.(*ssa.Alloc); ok {
		// A local copy of a field, in := legs.in, holding one value.
		if vals, ok := c.stored(a); ok && len(vals) == 1 {
			if _, isParam := vals[0].(*ssa.Parameter); !isParam {
				x = vals[0]
			}
		}
	}
	if i, ok := c.structParam(x); ok {
		return i, name, true
	}
	i, path, ok := c.fieldPath(x, depth+1)
	if !ok {
		return 0, "", false
	}
	return i, path + "." + name, true
}

// fieldName is the name of struct type t's i-th field, through a pointer.
func fieldName(t types.Type, i int) string {
	if p, ok := t.Underlying().(*types.Pointer); ok {
		t = p.Elem()
	}
	if s, ok := t.Underlying().(*types.Struct); ok && i < s.NumFields() {
		return s.Field(i).Name()
	}
	return "?"
}

// structParam returns the index of the parameter of the enclosing named
// function that v is, as rootParam does, unless it is the receiver. A
// field of a parameter holds a handle the caller chose for this call, such
// as an input struct's Tx; a field of the receiver, such as a service's
// client, is set long before and is not followed.
func (c *lockChecker) structParam(v ssa.Value) (int, bool) {
	i, ok := c.rootParam(v)
	if !ok {
		return 0, false
	}
	in, ok := v.(interface{ Parent() *ssa.Function })
	if !ok {
		return 0, false
	}
	if i == 0 && root(in.Parent()).Signature.Recv() != nil {
		return 0, false
	}
	return i, true
}

// fieldStored reports whether the function stores to the field fa
// addresses, in a parameter's struct: then the field may not hold what the
// caller gave.
func (c *lockChecker) fieldStored(fa *ssa.FieldAddr) bool {
	idx, isParam := c.rootParam(fa.X)
	stored := false
	var visit func(f *ssa.Function)
	visit = func(f *ssa.Function) {
		for _, b := range f.Blocks {
			for _, ins := range b.Instrs {
				st, ok := ins.(*ssa.Store)
				if !ok {
					continue
				}
				o, ok := st.Addr.(*ssa.FieldAddr)
				if !ok || o.Field != fa.Field {
					continue
				}
				if o.X == fa.X {
					stored = true
				} else if i, ok := c.rootParam(o.X); ok && isParam && i == idx {
					stored = true // in a literal, through a free variable
				}
			}
		}
		for _, a := range f.AnonFuncs {
			visit(a)
		}
	}
	visit(root(fa.Parent()))
	return stored
}

// fieldProvenance is where the handle in field of the argument a can come
// from: a itself when field is "", else the values a struct literal or a
// local struct variable stores in the field, or the caller's parameter's
// field. at is the call being judged, whose use of the struct is not a
// write the check cannot see.
func (c *lockChecker) fieldProvenance(a ssa.Value, field string, at ssa.Instruction) *handle {
	if field == "" {
		return c.provenance(a)
	}
	if i, ok := c.structParam(a); ok {
		return param(i, field)
	}
	if i, path, ok := c.fieldPath(a, 0); ok {
		return param(i, path+"."+field)
	}
	if _, ok := a.(*ssa.Const); ok {
		return &handle{} // the zero struct: a nil handle
	}
	first, rest, _ := strings.Cut(field, ".")
	addr := a
	if u, ok := a.(*ssa.UnOp); ok && u.Op == token.MUL {
		addr = u.X
	}
	unseen := other("the field " + field + " of a value the check cannot follow")
	alloc, ok := addr.(*ssa.Alloc)
	if !ok {
		return unseen
	}
	// The values stored in the variable or in its field. Any other use of
	// the variable's address, or of the field's, could write the field.
	h := &handle{}
	refs := alloc.Referrers()
	if refs == nil {
		return h
	}
	for _, r := range *refs {
		if r == at {
			continue // the call being judged, given the struct's address
		}
		switch r := r.(type) {
		case *ssa.DebugRef:
		case *ssa.UnOp:
			if r.Op != token.MUL {
				h.merge(unseen)
			}
		case *ssa.Store:
			if r.Addr != alloc {
				h.merge(unseen) // the address is stored
				continue
			}
			h.merge(c.fieldProvenance(r.Val, field, nil))
		case *ssa.FieldAddr:
			if fieldName(r.X.Type(), r.Field) != first {
				continue
			}
			if r.Referrers() == nil {
				continue
			}
			for _, w := range *r.Referrers() {
				switch w := w.(type) {
				case *ssa.DebugRef:
				case *ssa.Store:
					if w.Addr == r {
						h.merge(c.fieldProvenance(w.Val, rest, nil))
					} else {
						h.merge(unseen)
					}
				case *ssa.UnOp:
					if w.Op != token.MUL {
						h.merge(unseen)
					}
				default:
					h.merge(unseen) // such as a FieldAddr of a field of the field
				}
			}
		default:
			h.merge(unseen)
		}
	}
	return h
}

// loaded is f of the values stored at addr: a local variable's, which the
// function or its literals store, or a field's or a package variable's,
// which it does not follow.
func (c *lockChecker) loaded(addr ssa.Value, f func(ssa.Value) *handle) *handle {
	switch a := addr.(type) {
	case *ssa.FieldAddr:
		if i, path, ok := c.fieldPath(a, 0); ok {
			return param(i, path)
		}
		return other("the field " + fieldName(a.X.Type(), a.Field))
	case *ssa.Global:
		return other("the package variable " + a.Name())
	case *ssa.Alloc, *ssa.FreeVar:
		vals, ok := c.stored(addr)
		if !ok {
			return other("a variable the check cannot follow")
		}
		h := &handle{}
		for _, v := range vals {
			h.merge(f(v))
		}
		return h
	}
	return other("a value the check cannot follow")
}

// stored are the values stored in a local variable, from the Alloc that
// holds it, through every function literal it is captured by.
func (c *lockChecker) stored(addr ssa.Value) ([]ssa.Value, bool) {
	// Up from a free variable to the Allocs it is bound to.
	var tops []*ssa.Alloc
	var up func(v ssa.Value, depth int) bool
	up = func(v ssa.Value, depth int) bool {
		switch v := v.(type) {
		case *ssa.Alloc:
			tops = append(tops, v)
			return true
		case *ssa.FreeVar:
			bs := c.bindings(v)
			if len(bs) == 0 || depth > 16 {
				return false
			}
			for _, b := range bs {
				if !up(b, depth+1) {
					return false
				}
			}
			return true
		}
		return false
	}
	if !up(addr, 0) {
		return nil, false
	}
	var out []ssa.Value
	seen := map[ssa.Value]bool{}
	var down func(a ssa.Value) bool
	down = func(a ssa.Value) bool {
		if seen[a] {
			return true
		}
		seen[a] = true
		refs := a.Referrers()
		if refs == nil {
			return true
		}
		for _, r := range *refs {
			switch r := r.(type) {
			case *ssa.Store:
				if r.Addr == a {
					out = append(out, r.Val)
				}
			case *ssa.MakeClosure:
				lit := r.Fn.(*ssa.Function)
				for k, b := range r.Bindings {
					if b == a && !down(lit.FreeVars[k]) {
						return false
					}
				}
			}
		}
		return true
	}
	for _, t := range tops {
		if !down(t) {
			return nil, false
		}
	}
	return out, true
}

// queryPaths are the paths to the query v: where each started, and a lock
// applied on it. A cycle is walked as provenance walks one.
func (c *lockChecker) queryPaths(v ssa.Value) []qpath {
	if ps, ok := c.paths[v]; ok {
		return ps
	}
	if d, ok := c.openP[v]; ok {
		c.low = min(c.low, d)
		return c.provP[v]
	}
	c.depth++
	d, outer := c.depth, c.low
	c.openP[v], c.provP[v] = d, nil
	var ps []qpath
	done := false
	for round := 0; ; round++ {
		c.low = noLow
		ps = dedupPaths(c.queryPaths1(v))
		if c.low != d {
			done = c.low > d
			break
		}
		if pathsKey(ps) == pathsKey(c.provP[v]) || round == maxRounds {
			done = true
			break
		}
		c.provP[v] = ps
	}
	low := c.low
	delete(c.openP, v)
	delete(c.provP, v)
	c.depth--
	if done {
		c.paths[v] = ps
		c.low = outer
	} else {
		c.low = min(outer, low)
	}
	return ps
}

// dedupPaths drops repeated paths. Past maxPaths it gives one path that
// keeps a lock, if any path has one, on a handle it does not follow.
func dedupPaths(ps []qpath) []qpath {
	var out []qpath
	seen := map[string]bool{}
	for _, p := range ps {
		if k := p.key(); !seen[k] {
			seen[k] = true
			out = append(out, p)
		}
	}
	if len(out) <= maxPaths {
		return out
	}
	p := qpath{h: other("a query with more paths than the check follows")}
	for _, q := range out {
		if q.lock != nil {
			p.lock = q.lock
			break
		}
	}
	return []qpath{p}
}

// pathsKey identifies a set of paths, in any order.
func pathsKey(ps []qpath) string {
	ks := make([]string, 0, len(ps))
	for _, p := range ps {
		ks = append(ks, p.key())
	}
	slices.Sort(ks)
	return strings.Join(ks, ";")
}

func (c *lockChecker) queryPaths1(v ssa.Value) []qpath {
	one := func(h *handle) []qpath { return []qpath{{h: h}} }
	switch v := v.(type) {
	case *ssa.Phi:
		var ps []qpath
		for _, e := range v.Edges {
			ps = append(ps, c.queryPaths(e)...)
		}
		return ps
	case *ssa.Call:
		fn, obj := callee(v.Common())
		if obj == nil {
			return one(other("a query a call returns"))
		}
		key := funcKey(obj)
		if lockApply[key] {
			var ps []qpath
			for _, p := range c.queryPaths(arg(v.Common(), 0)) {
				ps = append(ps, qpath{h: p.h, lock: v})
			}
			return ps
		}
		if i, ok := queryHandle[key]; ok {
			return one(c.provenance(arg(v.Common(), i)))
		}
		switch r := roles[key]; r.role {
		case roleKeep, roleLimit, roleAllow, roleFrom:
			return c.queryPaths(arg(v.Common(), r.arg))
		case roleNew:
			return one(other("an entity's edge query"))
		}
		if qf := c.queryFromOf(fn, obj); qf != nil {
			h := &handle{}
			for _, p := range qf.Params {
				if a := arg(v.Common(), p); a != nil {
					h.merge(c.provenance(a))
				}
			}
			ps := []qpath{{h: h}}
			if qf.Locked {
				when := qf.When
				if len(when) == 0 {
					when = [][]LockCond{{}} // a fact without conditions
				}
				for _, g := range when {
					if gate, live := c.translate(g, v.Common()); live {
						ps = append(ps, qpath{h: h, lock: v, gate: gate})
					}
				}
			}
			return ps
		}
		return one(other("the query " + obj.Name() + " returns"))
	case *ssa.Parameter, *ssa.FreeVar:
		if _, ok := v.(*ssa.FreeVar); ok {
			var ps []qpath
			for _, b := range c.bindings(v.(*ssa.FreeVar)) {
				ps = append(ps, c.queryPaths(b)...)
			}
			return ps
		}
		p := v.(*ssa.Parameter)
		if p.Parent().Parent() == nil {
			return one(param(slices.Index(p.Parent().Params, p), ""))
		}
		return one(other("a function literal's parameter " + p.Name()))
	case *ssa.UnOp:
		if v.Op == token.MUL {
			vals, ok := c.stored(v.X)
			if !ok {
				return one(c.loaded(v.X, c.provenance))
			}
			var ps []qpath
			for _, s := range vals {
				ps = append(ps, c.queryPaths(s)...)
			}
			return ps
		}
	case *ssa.ChangeType:
		return c.queryPaths(v.X)
	case *ssa.Const:
		return nil
	}
	return one(other("a query the check cannot follow"))
}

// queryFromOf is the QueryFrom of a call's function: by its summary in
// this package, else by its fact.
func (c *lockChecker) queryFromOf(fn *ssa.Function, obj *types.Func) *QueryFrom {
	if fn != nil && fn.Pkg == c.pkg && len(fn.Blocks) > 0 {
		if fn.Parent() != nil {
			return nil
		}
		return c.queryFrom(fn)
	}
	var f QueryFrom
	if obj.Pkg() != c.pass.Pkg && c.pass.ImportObjectFact(obj, &f) {
		return &f
	}
	return nil
}

// queryFrom computes, once, fn's QueryFrom: nil unless fn returns one
// query, started from its parameters on every path.
func (c *lockChecker) queryFrom(fn *ssa.Function) *QueryFrom {
	if f, ok := c.qfs[fn]; ok {
		return f
	}
	c.qfs[fn] = nil
	res := fn.Signature.Results()
	if res.Len() != 1 || !isQuery(res.At(0).Type()) || len(fn.Blocks) == 0 {
		return nil
	}
	h := &handle{}
	var s lockSum
	for _, b := range fn.Blocks {
		if r, ok := b.Instrs[len(b.Instrs)-1].(*ssa.Return); ok {
			for _, p := range c.queryPaths(r.Results[0]) {
				h.merge(p.h)
				if p.lock == nil {
					continue
				}
				if gate, live := normalize(slices.Concat(p.gate, c.conds(p.lock), c.conds(r))); live {
					s.addLock(0, "", "", gate)
				}
			}
		}
	}
	if len(h.params) == 0 || len(h.others) > 0 {
		return nil
	}
	var params []int
	for _, p := range h.params {
		if p.field != "" {
			return nil
		}
		params = append(params, p.param)
	}
	slices.Sort(params)
	f := &QueryFrom{Params: params}
	if len(s.locks) > 0 {
		f.Locked, f.When = true, s.locks[0].When
	}
	c.qfs[fn] = f
	return f
}

// sites are the instructions that make the function literal fn: its
// MakeClosures, or the instructions that use it when it has no free
// variables.
func (c *lockChecker) sites(fn *ssa.Function) []ssa.Instruction {
	var out []ssa.Instruction
	for _, v := range c.closureValues(fn) {
		if ins, ok := v.(ssa.Instruction); ok {
			out = append(out, ins)
		} else if refs := v.Referrers(); refs != nil {
			out = append(out, *refs...)
		}
	}
	return out
}

// closureValues are the values of the function literal fn: its
// MakeClosures, or fn itself when it has no free variables.
func (c *lockChecker) closureValues(fn *ssa.Function) []ssa.Value {
	if len(fn.FreeVars) == 0 {
		return []ssa.Value{fn}
	}
	var out []ssa.Value
	for _, b := range fn.Parent().Blocks {
		for _, ins := range b.Instrs {
			if mc, ok := ins.(*ssa.MakeClosure); ok && mc.Fn == fn {
				out = append(out, mc)
			}
		}
	}
	return out
}

// bindings are the values bound to a function literal's free variable.
func (c *lockChecker) bindings(fv *ssa.FreeVar) []ssa.Value {
	fn := fv.Parent()
	k := slices.Index(fn.FreeVars, fv)
	var out []ssa.Value
	for _, v := range c.closureValues(fn) {
		if mc, ok := v.(*ssa.MakeClosure); ok && k < len(mc.Bindings) {
			out = append(out, mc.Bindings[k])
		}
	}
	return out
}

// txClosure reports whether the function literal fn is a transaction's
// closure: every use of it hands it a transaction's handle.
func (c *lockChecker) txClosure(fn *ssa.Function) bool {
	switch c.txFn[fn] {
	case 1:
		return true
	case -1:
		return false
	}
	if _, ok := c.txFn[fn]; ok {
		return false // being computed
	}
	c.txFn[fn] = 0
	vals := c.closureValues(fn)
	yes := len(vals) > 0
	for _, v := range vals {
		yes = yes && c.handsTx(v)
	}
	if yes {
		c.txFn[fn] = 1
	} else {
		c.txFn[fn] = -1
	}
	return yes
}

// handsTx reports whether every use of the function value v hands it a
// transaction's handle: it is called with one, passed to DB.Tx or to a
// TxCallback parameter, or captured by a function literal that does one of
// these with it.
func (c *lockChecker) handsTx(v ssa.Value) bool {
	ok, used := c.uses(v, map[ssa.Value]bool{})
	return ok && used
}

// uses reports whether every use of v hands it a transaction's handle, and
// whether there is one, not counting a comparison with nil. seen holds the
// values and variables this walk has met: one met again adds nothing.
func (c *lockChecker) uses(v ssa.Value, seen map[ssa.Value]bool) (ok, used bool) {
	if seen[v] {
		return true, false
	}
	seen[v] = true
	refs := v.Referrers()
	if refs == nil {
		return false, false
	}
	for _, r := range *refs {
		switch r := r.(type) {
		case *ssa.DebugRef:
		case ssa.CallInstruction:
			cm := r.Common()
			if cm.Value == v && !cm.IsInvoke() {
				for _, a := range cm.Args {
					if isDB(a.Type()) && !c.provenance(a).txOnly() {
						return false, false
					}
				}
				used = true
			}
			for j, a := range cm.Args {
				if a != v {
					continue
				}
				if !c.txArg(cm, j) {
					return false, false
				}
				used = true
			}
		case *ssa.MakeClosure:
			lit := r.Fn.(*ssa.Function)
			for k, b := range r.Bindings {
				if b == v {
					ok, u := c.uses(lit.FreeVars[k], seen)
					if !ok {
						return false, false
					}
					used = used || u
				}
			}
		case *ssa.BinOp:
			if !nilCompare(r) {
				return false, false
			}
		case *ssa.Store:
			// A variable captured by a function literal: go/ssa
			// spills it to an Alloc that the literal loads.
			if r.Val != v {
				return false, false
			}
			ok, u := c.loads(r.Addr, seen)
			if !ok {
				return false, false
			}
			used = used || u
		default:
			return false, false
		}
	}
	return true, used
}

// nilCompare reports whether b compares a value with nil: a use of a
// function value that does not call it.
func nilCompare(b *ssa.BinOp) bool {
	if b.Op != token.EQL && b.Op != token.NEQ {
		return false
	}
	for _, x := range []ssa.Value{b.X, b.Y} {
		if k, ok := x.(*ssa.Const); ok && k.Value == nil {
			return true
		}
	}
	return false
}

// loads reports, as uses does, of every value loaded from the variable at
// addr, an Alloc or a literal's free variable bound to one: whatever else
// is stored there is then handed a transaction's handle too.
func (c *lockChecker) loads(addr ssa.Value, seen map[ssa.Value]bool) (ok, used bool) {
	switch addr.(type) {
	case *ssa.Alloc, *ssa.FreeVar:
	default:
		return false, false
	}
	if seen[addr] {
		return true, false
	}
	seen[addr] = true
	refs := addr.Referrers()
	if refs == nil {
		return false, false
	}
	for _, r := range *refs {
		var ok, u bool
		switch r := r.(type) {
		case *ssa.DebugRef:
			continue
		case *ssa.Store:
			if r.Addr != addr {
				return false, false // the variable's address is stored
			}
			continue
		case *ssa.UnOp:
			if r.Op != token.MUL {
				return false, false
			}
			ok, u = c.uses(r, seen)
		case *ssa.MakeClosure:
			ok = true
			lit := r.Fn.(*ssa.Function)
			for k, b := range r.Bindings {
				if b == addr {
					o, w := c.loads(lit.FreeVars[k], seen)
					ok, u = ok && o, u || w
				}
			}
		default:
			return false, false
		}
		if !ok {
			return false, false
		}
		used = used || u
	}
	return true, used
}

// txArg reports whether a call's j-th argument is a function that the
// callee gives a transaction's handle.
func (c *lockChecker) txArg(cm *ssa.CallCommon, j int) bool {
	fn, obj := callee(cm)
	if obj == nil {
		// A function literal of the function, such as a test's
		// inTx := func(fn func(*velox.DB) error) {...}.
		lit := cm.StaticCallee()
		if lit == nil || lit.Parent() == nil || j >= len(lit.Params) || c.litCB[lit] {
			return false
		}
		c.litCB[lit] = true // a recursive call is not one
		ok := c.handsTx(lit.Params[j])
		delete(c.litCB, lit)
		return ok
	}
	if i, ok := txOpeners[funcKey(obj)]; ok && i == j {
		return true
	}
	var params []int
	if fn != nil && fn.Pkg == c.pkg && len(fn.Blocks) > 0 {
		if fn.Parent() == nil {
			params = c.callbacks(fn)
		}
	} else {
		var f TxCallback
		if obj.Pkg() != c.pass.Pkg && c.pass.ImportObjectFact(obj, &f) {
			params = f.Params
		}
	}
	return slices.Contains(params, j)
}

// callbacks computes, once, the parameters of fn that are TxCallback's.
func (c *lockChecker) callbacks(fn *ssa.Function) []int {
	if ps, ok := c.cbs[fn]; ok {
		return ps
	}
	c.cbs[fn] = nil
	var out []int
	for i, p := range fn.Params {
		sig, ok := p.Type().Underlying().(*types.Signature)
		if !ok || !takesDB(sig) {
			continue
		}
		if c.handsTx(p) {
			out = append(out, i)
		}
	}
	c.cbs[fn] = out
	return out
}

// takesDB reports whether a parameter of sig is a DB.
func takesDB(sig *types.Signature) bool {
	for v := range sig.Params().Variables() {
		if isDB(v.Type()) {
			return true
		}
	}
	return false
}
