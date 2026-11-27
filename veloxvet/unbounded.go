package veloxvet

import (
	"go/ast"
	"go/constant"
	"go/types"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ssa"
)

// Unbounded reports a read of a velox query that fails with ErrUnbounded
// on every path to it: no Limit or AllowUnbounded is applied to the query
// it reads. A grouped aggregate (CountBy, SumBy and the rest) ignores
// Limit, so it is reported unless AllowUnbounded is applied.
var Unbounded = &analysis.Analyzer{
	Name:      "unbounded",
	Doc:       unboundedDoc,
	URL:       "https://pkg.go.dev/github.com/syssam/velox/veloxvet#Unbounded",
	Run:       runUnbounded,
	FactTypes: []analysis.Fact{(*FreshQuery)(nil)},
}

const unboundedDoc = `report velox reads that fail with ErrUnbounded

A velox read of rows (All, Iter, Values, IDs, Rows, SQL, velox.Scan, and
untyped's All, Rows and SQL) needs Limit or AllowUnbounded on its query,
and a grouped aggregate (velox.CountBy, SumBy, ...) needs AllowUnbounded;
otherwise it fails with velox.ErrUnbounded when it runs. One, First,
Count, Exists, Paginate, ScanOne and the plain aggregates need neither.
A constant Limit that is not positive, such as Limit(0), bounds nothing.

The check follows a query through the function's local variables, its
reassignments and its branches, and reports a read only when no path to
it applies a bound. A query from a parameter, a field, a closure's
variable or a call it cannot see is not reported. A function that returns
a new query with no bound on every path, such as velox-gen's QueryX
methods, carries a FreshQuery fact, so a read of its result is checked
in the packages that call it.

A comment //velox:unbounded-ok on any line of the statement that holds
the read, or alone on the line before the statement, waives the report:
for a test or a demo that expects the read to fail. For a read in the
header of an if, for or switch statement, or of a case, only the
header counts. The lines inside a function literal's body belong to the
literal's statements, not to the statement around it.`

// FreshQuery is the fact of a function each of whose returns is a query
// that velox started on that path, with no Limit and no AllowUnbounded.
type FreshQuery struct{}

// AFact marks FreshQuery as a go/analysis fact.
func (*FreshQuery) AFact() {}

// String names the fact in analysistest's expectations.
func (*FreshQuery) String() string { return "FreshQuery" }

const (
	veloxPath   = "github.com/syssam/velox"
	untypedPath = veloxPath + "/untyped"
	enginePath  = veloxPath + "/internal/engine"
)

// role is what a velox function does with a query.
type role int

const (
	roleNone  role = iota
	roleNew        // returns a new query, with no bound
	roleKeep       // returns its receiver with the bound it had
	roleLimit      // Limit
	roleAllow      // AllowUnbounded
	roleFrom       // untyped.FromQuery: its argument as a TableQuery
	roleRead       // a read that needs Limit or AllowUnbounded
	roleGroup      // a grouped aggregate, which needs AllowUnbounded
)

// funcRole is a velox function's role and, for a read, the index of the
// query among the call's arguments (the receiver is the first).
type funcRole struct {
	role role
	arg  int
}

// roles holds the velox functions by package path, receiver type and name.
var roles = func() map[string]funcRole {
	m := map[string]funcRole{
		veloxPath + ".DB.Query":                   {roleNew, 0},
		veloxPath + ".QueryEdge":                  {roleNew, 0},
		untypedPath + ".Query":                    {roleNew, 0},
		untypedPath + ".FromQuery":                {roleFrom, 0},
		enginePath + ".QueryTable":                {roleNew, 0},
		veloxPath + ".Scan":                       {roleRead, 1},
		veloxPath + ".Query.Limit":                {roleLimit, 0},
		enginePath + ".TableQuery.Limit":          {roleLimit, 0},
		veloxPath + ".Query.AllowUnbounded":       {roleAllow, 0},
		enginePath + ".TableQuery.AllowUnbounded": {roleAllow, 0},
	}
	// The chaining methods that leave Limit and AllowUnbounded as they
	// were. A method not named here returns a query the check does not
	// know. TestTablesMatchAPI checks these lists against velox.
	for _, name := range []string{
		"Where", "OrderBy", "Offset", "Writable", "ForUpdate", "ForShare",
		"MaxRows", "Columns", "After", "Select", "GroupBy", "Having",
		"Join", "LeftJoin",
	} {
		m[veloxPath+".Query."+name] = funcRole{roleKeep, 0}
		m[enginePath+".TableQuery."+name] = funcRole{roleKeep, 0}
	}
	for _, name := range []string{"With", "SkipLocked", "NoWait", "Omit", "SelectRaw", "Distinct"} {
		m[veloxPath+".Query."+name] = funcRole{roleKeep, 0}
	}
	for _, name := range []string{"Unordered", "OrderValues", "AfterValues", "Before"} {
		m[enginePath+".TableQuery."+name] = funcRole{roleKeep, 0}
	}
	for _, name := range []string{"All", "Iter", "Values", "IDs", "Rows", "SQL"} {
		m[veloxPath+".Query."+name] = funcRole{roleRead, 0}
	}
	for _, name := range []string{"All", "Rows", "SQL"} {
		m[enginePath+".TableQuery."+name] = funcRole{roleRead, 0}
	}
	for _, name := range []string{"CountBy", "SumBy", "AvgBy", "MinBy", "MaxBy", "SumByNull", "AvgByNull", "MinByNull", "MaxByNull"} {
		m[veloxPath+"."+name] = funcRole{roleGroup, 1}
	}
	return m
}()

// funcKey is f's key in roles.
func funcKey(f *types.Func) string {
	if f.Pkg() == nil {
		return ""
	}
	key := f.Pkg().Path() + "."
	if recv := f.Signature().Recv(); recv != nil {
		t := recv.Type()
		if p, ok := t.(*types.Pointer); ok {
			t = p.Elem()
		}
		n, ok := types.Unalias(t).(*types.Named)
		if !ok {
			return ""
		}
		key += n.Origin().Obj().Name() + "."
	}
	return key + f.Name()
}

// isQuery reports whether t is velox.Query or untyped.TableQuery.
func isQuery(t types.Type) bool {
	n, ok := types.Unalias(t).(*types.Named)
	if !ok {
		return false
	}
	obj := n.Origin().Obj()
	if obj.Pkg() == nil {
		return false
	}
	switch obj.Pkg().Path() + "." + obj.Name() {
	case veloxPath + ".Query", enginePath + ".TableQuery":
		return true
	}
	return false
}

// state is the set of what a query value can be on the paths to it: a bit
// for each.
type state uint8

const (
	sFresh   state = 1 << iota // no Limit, no AllowUnbounded
	sLimit                     // Limit, and no AllowUnbounded
	sAllow                     // AllowUnbounded
	sUnknown                   // a query the check cannot follow
)

// limited is s after Limit.
func limited(s state) state {
	out := s &^ (sFresh | sLimit)
	if s&(sFresh|sLimit) != 0 {
		out |= sLimit
	}
	return out
}

// unlimited is s after a Limit that is not positive: Query's fails the
// read and TableQuery's removes the limit, so it bounds nothing.
func unlimited(s state) state {
	out := s &^ (sFresh | sLimit)
	if s&(sFresh|sLimit) != 0 {
		out |= sFresh
	}
	return out
}

// nonPositive reports whether v, a Limit's argument, is a constant that
// is not positive.
func nonPositive(v ssa.Value) bool {
	c, ok := v.(*ssa.Const)
	return ok && c.Value != nil && c.Value.Kind() == constant.Int && constant.Sign(c.Value) <= 0
}

// allowed is s after AllowUnbounded.
func allowed(s state) state {
	if s == 0 {
		return 0
	}
	return sAllow
}

// summary is what a function of the package is known to return.
type summary uint8

const (
	sumPending summary = iota + 1 // being computed: a recursive call
	sumFresh                      // a FreshQuery
	sumOther
)

// checker checks one package.
type checker struct {
	pass *analysis.Pass
	pkg  *ssa.Package
	sums map[*ssa.Function]summary
	ok   directives
}

// directive is the comment that waives the report of a read: on any line
// of the statement that holds the read, or alone on the line before the
// statement. It is for a test or a demo that expects the read to fail.
const directive = "//velox:unbounded-ok"

func runUnbounded(pass *analysis.Pass) (any, error) {
	if !reaches(pass.Pkg, veloxPath, map[*types.Package]bool{}) {
		return nil, nil
	}
	pkg, funcs := buildSSA(pass)
	c := &checker{pass: pass, pkg: pkg, sums: map[*ssa.Function]summary{}, ok: findDirectives(pass, directive)}
	for _, fn := range funcs {
		if obj, ok := fn.Object().(*types.Func); ok && obj.Pkg() == pass.Pkg && c.summary(fn) == sumFresh {
			pass.ExportObjectFact(obj, &FreshQuery{})
		}
		c.report(fn)
	}
	return nil, nil
}

// reaches reports whether p is the package at path, or imports it
// directly or not: only then can its code hold a velox query.
func reaches(p *types.Package, path string, seen map[*types.Package]bool) bool {
	if p.Path() == path {
		return true
	}
	if seen[p] {
		return false
	}
	seen[p] = true
	for _, i := range p.Imports() {
		if reaches(i, path, seen) {
			return true
		}
	}
	return false
}

// buildSSA builds the package's SSA form, as buildssa does, and returns
// its functions, the function literals among them. It is built here, not
// through buildssa, so that a package that cannot hold a velox query costs
// nothing.
func buildSSA(pass *analysis.Pass) (*ssa.Package, []*ssa.Function) {
	prog := ssa.NewProgram(pass.Fset, ssa.BuilderMode(0))
	created := map[*types.Package]bool{pass.Pkg: true}
	var create func(p *types.Package)
	create = func(p *types.Package) {
		for _, i := range p.Imports() {
			if !created[i] {
				created[i] = true
				prog.CreatePackage(i, nil, nil, true)
				create(i)
			}
		}
	}
	create(pass.Pkg)
	pkg := prog.CreatePackage(pass.Pkg, pass.Files, pass.TypesInfo, false)
	pkg.Build()

	var funcs []*ssa.Function
	var add func(f *ssa.Function)
	add = func(f *ssa.Function) {
		funcs = append(funcs, f)
		for _, a := range f.AnonFuncs {
			add(a)
		}
	}
	for _, file := range pass.Files {
		for _, d := range file.Decls {
			if d, ok := d.(*ast.FuncDecl); ok {
				if obj, ok := pass.TypesInfo.Defs[d.Name].(*types.Func); ok {
					if f := prog.FuncValue(obj); f != nil {
						add(f)
					}
				}
			}
		}
	}
	if init := pkg.Func("init"); init != nil {
		add(init) // the package's variables' initializers, and their literals
	}
	return pkg, funcs
}

// callee returns the function a call calls, its generic origin for an
// instance, and the function's object, nil for a function literal. A
// method value's wrapper (go/ssa's $bound) holds its receiver among its
// free variables, not the call's arguments, so it is not followed: its
// call is a call the check cannot see.
func callee(c *ssa.CallCommon) (*ssa.Function, *types.Func) {
	fn := c.StaticCallee()
	if fn == nil || len(fn.FreeVars) > 0 {
		return nil, nil
	}
	if o := fn.Origin(); o != nil {
		fn = o
	}
	obj, _ := fn.Object().(*types.Func)
	if obj != nil {
		obj = obj.Origin()
	}
	return fn, obj
}

// roleOf is the role of a call's function.
func roleOf(c *ssa.CallCommon) (funcRole, *ssa.Function, *types.Func) {
	fn, obj := callee(c)
	if obj == nil {
		return funcRole{}, fn, nil
	}
	return roles[funcKey(obj)], fn, obj
}

// states returns the state of each query value of fn, computed to a
// fixed point over its phis.
func (c *checker) states(fn *ssa.Function) map[ssa.Value]state {
	var vals []ssa.Value
	st := map[ssa.Value]state{}
	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			if v, ok := ins.(ssa.Value); ok && isQuery(v.Type()) {
				vals = append(vals, v)
				st[v] = 0
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for _, v := range vals {
			if s := st[v] | c.transfer(v, st); s != st[v] {
				st[v], changed = s, true
			}
		}
	}
	return st
}

// arg is a call's i-th argument, nil when it has fewer.
func arg(c *ssa.CallCommon, i int) ssa.Value {
	if i < len(c.Args) {
		return c.Args[i]
	}
	return nil
}

// get is v's state: a parameter, a free variable or a constant is unknown.
func get(v ssa.Value, st map[ssa.Value]state) state {
	if s, ok := st[v]; ok {
		return s
	}
	return sUnknown
}

// transfer is v's state from its operands' current states.
func (c *checker) transfer(v ssa.Value, st map[ssa.Value]state) state {
	switch v := v.(type) {
	case *ssa.Phi:
		var s state
		for _, e := range v.Edges {
			s |= get(e, st)
		}
		return s
	case *ssa.Call:
		r, fn, obj := roleOf(v.Common())
		q := arg(v.Common(), r.arg)
		switch r.role {
		case roleNew:
			return sFresh
		case roleKeep, roleFrom:
			return get(q, st)
		case roleLimit:
			if nonPositive(arg(v.Common(), r.arg+1)) {
				return unlimited(get(q, st))
			}
			return limited(get(q, st))
		case roleAllow:
			return allowed(get(q, st))
		case roleNone:
			if c.fresh(fn, obj) {
				return sFresh
			}
		}
	}
	return sUnknown
}

// fresh reports whether a call of fn returns a new query with no bound:
// by its summary in this package, else by its fact.
func (c *checker) fresh(fn *ssa.Function, obj *types.Func) bool {
	if fn != nil && fn.Pkg == c.pkg && len(fn.Blocks) > 0 {
		return c.summary(fn) == sumFresh
	}
	if obj != nil && obj.Pkg() != c.pass.Pkg {
		return c.pass.ImportObjectFact(obj, &FreshQuery{})
	}
	return false
}

// summary computes, once, whether every return of fn is a fresh query.
// A recursive call is not fresh while its function is being computed.
func (c *checker) summary(fn *ssa.Function) summary {
	if s, ok := c.sums[fn]; ok {
		if s == sumPending {
			return sumOther
		}
		return s
	}
	res := fn.Signature.Results()
	if res.Len() != 1 || !isQuery(res.At(0).Type()) || len(fn.Blocks) == 0 {
		c.sums[fn] = sumOther
		return sumOther
	}
	c.sums[fn] = sumPending
	st := c.states(fn)
	out, returns := sumFresh, 0
	for _, b := range fn.Blocks {
		if r, ok := b.Instrs[len(b.Instrs)-1].(*ssa.Return); ok {
			returns++
			if get(r.Results[0], st) != sFresh {
				out = sumOther
			}
		}
	}
	if returns == 0 {
		out = sumOther
	}
	c.sums[fn] = out
	return out
}

// report reports fn's reads of queries that are unbounded on every path.
func (c *checker) report(fn *ssa.Function) {
	var st map[ssa.Value]state
	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			call, ok := ins.(ssa.CallInstruction)
			if !ok {
				continue
			}
			r, _, obj := roleOf(call.Common())
			if r.role != roleRead && r.role != roleGroup {
				continue
			}
			if st == nil {
				st = c.states(fn)
			}
			c.check(call, r, obj, get(arg(call.Common(), r.arg), st))
		}
	}
}

// check reports call, a read of role r by obj, of a query in state s, when
// every path to it leaves the query unbounded for r.
func (c *checker) check(call ssa.CallInstruction, r funcRole, obj *types.Func, s state) {
	name := obj.Name()
	if obj.Signature().Recv() == nil {
		name = "velox." + name
	}
	if s == 0 || c.ok.waives(c.pass, call.Pos()) {
		return
	}
	switch {
	case r.role == roleRead && s == sFresh:
		c.pass.Reportf(call.Pos(), "%s reads a query with no Limit or AllowUnbounded: it fails with velox.ErrUnbounded", name)
	case r.role == roleGroup && s&^(sFresh|sLimit) == 0:
		c.pass.Reportf(call.Pos(), "%s groups a query with no AllowUnbounded, which a Limit does not replace: it fails with velox.ErrUnbounded", name)
	}
}
