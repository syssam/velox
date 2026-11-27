package veloxvet

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"slices"
	"strconv"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ssa"
)

// ZeroDefault reports a field of an inserted entity that the code sets to
// its zero value when the field's column has a Default that is not zero, or
// a DefaultFunc: the insert fills the default into a zero field, so the
// zero is not stored.
var ZeroDefault = &analysis.Analyzer{
	Name:      "zerodefault",
	Doc:       zeroDefaultDoc,
	URL:       "https://pkg.go.dev/github.com/syssam/velox/veloxvet#ZeroDefault",
	Run:       runZeroDefault,
	FactTypes: []analysis.Fact{(*Defaults)(nil)},
}

const zeroDefaultDoc = `report a zero value that an insert replaces with the field's default

An insert fills a field's Default, or its DefaultFunc's value, into the
field when the inserted struct holds the zero value there (ADR 0082). So
with field.Bool("active").Default(true), inserting &gen.User{Active: false}
stores true. The check reports a constant zero (false, 0, "") given to such
a field, in an entity's composite literal or by an assignment e.F = zero,
when the entity reaches db.Insert or db.InsertAll in the same function, and
the insert has no Set or SetInput, which could store the zero. To store it,
name the column in the insert: db.Insert(&u).Set(gen.Users.Active.Set(false)).

The check follows a local variable of the entity type, or of a pointer to
it, and only when the function does nothing else with it than read or set
its fields and insert it. A zero set to its field is reported when it is
set before the insert on a path that reaches it: not in a branch that
returns first. An insert whose query is held in a variable or
passed to a function is not followed. A default is known from the
Descriptor velox-gen writes: a Defaults fact of the entity's type, exported
while the check runs on the generated package.

A comment //velox:zero-ok on any line of the statement that holds the zero,
or alone on the line before the statement, waives the report.`

// Defaults is the fact of a velox entity type whose Descriptor gives some
// columns a Default that is not the zero value, or a DefaultFunc.
type Defaults struct {
	// Handles is the name of the package's variable of the entity's
	// handles, "" when there is none.
	Handles string
	// Fields are those fields, in the Descriptor's order.
	Fields []DefaultField
}

// DefaultField is a field of a Defaults fact.
type DefaultField struct {
	Name    string // the Go field's name
	Default string // the Default as Go source, "" for a DefaultFunc
}

// field returns the field of d named name.
func (d *Defaults) field(name string) (DefaultField, bool) {
	for _, f := range d.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return DefaultField{}, false
}

// AFact marks Defaults as a go/analysis fact.
func (*Defaults) AFact() {}

// String names the fact in analysistest's expectations.
func (*Defaults) String() string { return "Defaults" }

// zeroDirective waives a ZeroDefault report, as directive waives an
// Unbounded one.
const zeroDirective = "//velox:zero-ok"

func runZeroDefault(pass *analysis.Pass) (any, error) {
	if !reaches(pass.Pkg, veloxPath, map[*types.Package]bool{}) {
		return nil, nil
	}
	exportDefaults(pass)
	z := &zeroChecker{pass: pass, ok: findDirectives(pass, zeroDirective), seen: map[token.Pos]bool{}}
	for _, f := range pass.Files {
		for _, d := range f.Decls {
			if d, ok := d.(*ast.FuncDecl); ok && d.Body != nil {
				z.function(d.Body)
			}
		}
	}
	return nil, nil
}

// exportDefaults exports a Defaults fact for each entity type of the
// package that a velox.Descriptor literal describes.
func exportDefaults(pass *analysis.Pass) {
	for _, f := range pass.Files {
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			e := descriptorEntity(pass.TypesInfo.TypeOf(lit))
			if e == nil || e.Obj().Pkg() != pass.Pkg {
				return true
			}
			var fields []DefaultField
			for _, col := range elements(lit, "Columns") {
				if name, def, ok := columnDefault(pass.TypesInfo, col); ok {
					fields = append(fields, DefaultField{name, def})
				}
			}
			if len(fields) > 0 {
				pass.ExportObjectFact(e.Obj(), &Defaults{Handles: handlesVar(pass.Pkg, e), Fields: fields})
			}
			return false
		})
	}
}

// descriptorEntity is E when t is velox.Descriptor[E] of a named E.
func descriptorEntity(t types.Type) *types.Named {
	n, ok := types.Unalias(t).(*types.Named)
	if !ok || n.TypeArgs().Len() != 1 {
		return nil
	}
	obj := n.Origin().Obj()
	if obj.Pkg() == nil || obj.Pkg().Path() != veloxPath || obj.Name() != "Descriptor" {
		return nil
	}
	e, _ := types.Unalias(n.TypeArgs().At(0)).(*types.Named)
	return e
}

// elements returns the composite literals of the slice literal that lit
// gives its field key.
func elements(lit *ast.CompositeLit, key string) []*ast.CompositeLit {
	var out []*ast.CompositeLit
	for _, el := range lit.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok || !isIdent(kv.Key, key) {
			continue
		}
		if s, ok := kv.Value.(*ast.CompositeLit); ok {
			for _, x := range s.Elts {
				if x, ok := x.(*ast.CompositeLit); ok {
					out = append(out, x)
				}
			}
		}
	}
	return out
}

// columnDefault returns the field a ColumnDescriptor literal names and its
// default as Go source, "" for a DefaultFunc, when it has a DefaultFunc or a
// constant Default that is not zero.
func columnDefault(info *types.Info, col *ast.CompositeLit) (string, string, bool) {
	var field, def string
	has := false
	for _, el := range col.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, _ := kv.Key.(*ast.Ident)
		if key == nil {
			continue
		}
		v := info.Types[kv.Value].Value
		switch key.Name {
		case "Field":
			if v != nil && v.Kind() == constant.String {
				field = constant.StringVal(v)
			}
		case "Default":
			if v != nil && !isZero(v) {
				def, has = source(v), true
			}
		case "DefaultFunc":
			if !isIdent(kv.Value, "nil") {
				def, has = "", true
			}
		}
	}
	return field, def, has && field != ""
}

// handlesVar is the name of pkg's variable of e's handles, EHandles.
func handlesVar(pkg *types.Package, e *types.Named) string {
	want := e.Obj().Name() + "Handles"
	for _, name := range pkg.Scope().Names() {
		if v, ok := pkg.Scope().Lookup(name).(*types.Var); ok {
			if n, ok := types.Unalias(v.Type()).(*types.Named); ok && n.Obj().Name() == want && n.Obj().Pkg() == pkg {
				return name
			}
		}
	}
	return ""
}

func isIdent(e ast.Expr, name string) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == name
}

// source is v as Go source: a float as its shortest decimal, which
// ExactString prints as a fraction (3/2 for 1.5), a string quoted.
func source(v constant.Value) string {
	if v.Kind() == constant.Float {
		f, _ := constant.Float64Val(v)
		return strconv.FormatFloat(f, 'g', -1, 64)
	}
	return v.ExactString()
}

// isZero reports whether v is its kind's zero value.
func isZero(v constant.Value) bool {
	switch v.Kind() {
	case constant.Bool:
		return !constant.BoolVal(v)
	case constant.String:
		return constant.StringVal(v) == ""
	case constant.Int, constant.Float, constant.Complex:
		return constant.Sign(v) == 0
	}
	return false
}

// zeroChecker checks the functions of one package.
type zeroChecker struct {
	pass    *analysis.Pass
	ok      directives
	parents map[ast.Node]ast.Node
	seen    map[token.Pos]bool // the zeros reported, once each
	instrs  []ssa.Instruction  // the package's SSA instructions, built on first use
}

// flows reports whether the statement assign, which sets a field, can run
// before call on some path: the block of its store reaches the call's
// block in the package's SSA form, or precedes the call in one block. When
// either is not found, or they are in different functions, it reports
// true, as the check did before it had paths.
func (z *zeroChecker) flows(assign ast.Node, call *ast.CallExpr) bool {
	if z.instrs == nil {
		_, funcs := buildSSA(z.pass)
		for _, fn := range funcs {
			for _, b := range fn.Blocks {
				z.instrs = append(z.instrs, b.Instrs...)
			}
		}
	}
	var store, run ssa.Instruction
	for _, ins := range z.instrs {
		switch ins := ins.(type) {
		case *ssa.Store:
			if assign != nil && assign.Pos() <= ins.Pos() && ins.Pos() < assign.End() && store == nil {
				store = ins
			}
		case *ssa.Call:
			if ins.Pos() == call.Lparen {
				run = ins
			}
		}
	}
	if store == nil || run == nil || store.Parent() != run.Parent() {
		return true
	}
	from, to := store.Block(), run.Block()
	if from == to {
		return slices.Index(from.Instrs, store) < slices.Index(to.Instrs, run)
	}
	seen := map[*ssa.BasicBlock]bool{}
	work := slices.Clone(from.Succs)
	for len(work) > 0 {
		b := work[len(work)-1]
		work = work[:len(work)-1]
		if b == to {
			return true
		}
		if !seen[b] {
			seen[b] = true
			work = append(work, b.Succs...)
		}
	}
	return false
}

// zero is a constant zero given to a field with a default: the expression,
// the field and the entity's fact.
type zero struct {
	expr  ast.Expr
	field string
	defs  *Defaults
}

// function checks the body of one function, its literals included.
func (z *zeroChecker) function(body *ast.BlockStmt) {
	z.parents = map[ast.Node]ast.Node{}
	var stack []ast.Node
	var inserts []*ast.CallExpr
	ast.Inspect(body, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		if len(stack) > 0 {
			z.parents[n] = stack[len(stack)-1]
		}
		stack = append(stack, n)
		if call, ok := n.(*ast.CallExpr); ok && z.insertName(call) != "" {
			inserts = append(inserts, call)
		}
		return true
	})
	for _, call := range inserts {
		if len(call.Args) != 1 || !z.plain(call) {
			continue
		}
		for _, zr := range z.zeros(body, call, call.Args[0]) {
			z.report(zr)
		}
	}
}

// insertName is "Insert", "InsertAll" or "InsertBulk" when call calls that
// method of velox.DB, else "".
func (z *zeroChecker) insertName(call *ast.CallExpr) string {
	fun := ast.Unparen(call.Fun)
	switch f := fun.(type) {
	case *ast.IndexExpr:
		fun = f.X
	case *ast.IndexListExpr:
		fun = f.X
	}
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	obj, ok := z.pass.TypesInfo.Uses[sel.Sel].(*types.Func)
	if !ok {
		return ""
	}
	switch k := funcKey(obj.Origin()); k {
	case veloxPath + ".DB.Insert", veloxPath + ".DB.InsertAll", veloxPath + ".DB.InsertBulk":
		return obj.Name()
	}
	return ""
}

// insertKeeps are InsertQuery's methods that write no column of the rows
// it inserts; insertRuns are those that fill the defaults.
var (
	insertKeeps = map[string]bool{
		"OnConflict": true, "Update": true, "UpdateAll": true, "UpdateSet": true,
		"UpdateWhere": true, "OnConflictWhere": true, "OnConstraint": true, "DoNothing": true,
	}
	insertRuns = map[string]bool{"Exec": true, "SQL": true}
)

// plain reports whether the query an Insert or InsertAll call starts is
// run by a chain of methods that set no column, alone or as a row of an
// InsertBulk. A query held in a variable, passed to a function or given
// Set or SetInput is not.
func (z *zeroChecker) plain(call *ast.CallExpr) bool {
	if z.insertName(call) == "InsertBulk" {
		return false
	}
	var e ast.Node = call
	for {
		p := z.parents[e]
		switch p := p.(type) {
		case *ast.ParenExpr:
			e = p
			continue
		case *ast.CallExpr:
			if z.insertName(p) == "InsertBulk" {
				e = p
				continue
			}
			return false
		case *ast.SelectorExpr:
			if p.X != e {
				return false
			}
			if insertRuns[p.Sel.Name] {
				return true
			}
			if !insertKeeps[p.Sel.Name] {
				return false
			}
			e = z.callOf(p)
			if e == nil {
				return false
			}
			continue
		}
		return false
	}
}

// callOf is the call of the method sel names, through an explicit
// instantiation, or nil.
func (z *zeroChecker) callOf(sel *ast.SelectorExpr) ast.Node {
	var e ast.Node = sel
	p := z.parents[e]
	switch p.(type) {
	case *ast.IndexExpr, *ast.IndexListExpr:
		e, p = p, z.parents[p]
	}
	if c, ok := p.(*ast.CallExpr); ok && c.Fun == e {
		return c
	}
	return nil
}

// zeros returns the constant zeros the inserted argument arg holds in
// fields with defaults.
func (z *zeroChecker) zeros(body *ast.BlockStmt, call *ast.CallExpr, arg ast.Expr) []zero {
	arg = ast.Unparen(arg)
	if u, ok := arg.(*ast.UnaryExpr); ok && u.Op == token.AND {
		switch x := ast.Unparen(u.X).(type) {
		case *ast.CompositeLit:
			return z.literal(x, nil)
		case *ast.Ident:
			return z.variable(body, call, x, false)
		}
		return nil
	}
	switch x := arg.(type) {
	case *ast.CompositeLit: // InsertAll's []E{...}
		if _, ok := types.Unalias(z.pass.TypesInfo.TypeOf(x)).Underlying().(*types.Slice); !ok {
			return nil // a named slice type counts, as InsertAll takes its elements
		}
		var out []zero
		for _, el := range x.Elts {
			if el, ok := ast.Unparen(el).(*ast.CompositeLit); ok {
				out = append(out, z.literal(el, nil)...)
			}
		}
		return out
	case *ast.Ident:
		if _, ok := types.Unalias(z.pass.TypesInfo.TypeOf(x)).(*types.Pointer); ok {
			return z.variable(body, call, x, true)
		}
	}
	return nil
}

// literalField is the field name and value of the i-th element el of a
// struct literal of type st: by its key, or by its position when it has
// none.
func literalField(st *types.Struct, i int, el ast.Expr) (name string, val ast.Expr, ok bool) {
	if kv, isKV := el.(*ast.KeyValueExpr); isKV {
		key, isIdent := kv.Key.(*ast.Ident)
		if !isIdent {
			return "", nil, false
		}
		return key.Name, kv.Value, true
	}
	if st == nil || i >= st.NumFields() {
		return "", nil, false
	}
	return st.Field(i).Name(), el, true
}

// literal returns the constant zeros lit, an entity's composite literal,
// gives fields with defaults, but those in skip.
func (z *zeroChecker) literal(lit *ast.CompositeLit, skip map[string]bool) []zero {
	defs := z.defaults(z.pass.TypesInfo.TypeOf(lit))
	if defs == nil {
		return nil
	}
	var out []zero
	st, _ := types.Unalias(z.pass.TypesInfo.TypeOf(lit)).Underlying().(*types.Struct)
	for i, el := range lit.Elts {
		name, val, ok := literalField(st, i, el)
		if !ok || skip[name] {
			continue
		}
		if _, has := defs.field(name); has && z.constZero(val) {
			out = append(out, zero{val, name, defs})
		}
	}
	return out
}

// defaults is the Defaults fact of t, an entity type or a pointer to one,
// or nil.
func (z *zeroChecker) defaults(t types.Type) *Defaults {
	if p, ok := types.Unalias(t).(*types.Pointer); ok {
		t = p.Elem()
	}
	n, ok := types.Unalias(t).(*types.Named)
	if !ok {
		return nil
	}
	var d Defaults
	if !z.pass.ImportObjectFact(n.Origin().Obj(), &d) {
		return nil
	}
	return &d
}

// constZero reports whether e is a constant zero of a basic type.
func (z *zeroChecker) constZero(e ast.Expr) bool {
	tv := z.pass.TypesInfo.Types[e]
	return tv.Value != nil && isZero(tv.Value)
}

// variable returns the constant zeros that id, a local variable inserted
// by call (as &id, or as id when ptr), holds in fields with defaults, when
// the function only reads and sets its fields besides. A field the
// function sets to anything else, or whose address it takes, is left out.
func (z *zeroChecker) variable(body *ast.BlockStmt, call *ast.CallExpr, id *ast.Ident, ptr bool) []zero {
	v, ok := z.pass.TypesInfo.Uses[id].(*types.Var)
	if !ok || v.Pos() < body.Pos() || v.Pos() >= body.End() {
		return nil
	}
	defs := z.defaults(v.Type())
	if defs == nil {
		return nil
	}
	t := &tracked{z: z, v: v, id: id, call: call, ptr: ptr, defs: defs, skip: map[string]bool{}}
	ast.Inspect(body, func(n ast.Node) bool {
		if n, ok := n.(*ast.Ident); ok && !t.bad {
			t.visit(n)
		}
		return !t.bad
	})
	if t.bad {
		return nil
	}
	var out []zero
	if t.init != nil {
		out = z.literal(t.init, t.skip)
	}
	for _, s := range t.sets {
		if !t.skip[s.field] {
			out = append(out, s)
		}
	}
	return out
}

// tracked is what variable learns of a local variable v, inserted as id
// by call.
type tracked struct {
	z    *zeroChecker
	v    *types.Var
	id   *ast.Ident
	call *ast.CallExpr
	ptr  bool
	defs *Defaults

	init *ast.CompositeLit // the literal v starts with
	sets []zero            // the constant zeros set to v's fields
	skip map[string]bool   // the fields set to anything else
	bad  bool              // v is used in a way the check does not follow
}

// visit records what n, an identifier, does with t.v.
func (t *tracked) visit(n *ast.Ident) {
	info := t.z.pass.TypesInfo
	if info.Defs[n] == t.v {
		t.init, t.bad = nil, true
		if lit, ok := t.z.initOf(n, t.ptr); ok {
			t.init, t.bad = lit, false
		}
		return
	}
	if info.Uses[n] != t.v || n == t.id || t.z.inserted(n) {
		return
	}
	field, rhs, ok := t.z.fieldUse(n)
	if !ok {
		t.bad = true
		return
	}
	if _, has := t.defs.field(field); !has {
		return
	}
	switch {
	case rhs != nil && t.z.constZero(rhs):
		if rhs.Pos() < t.call.Pos() && t.z.flows(t.z.parents[rhs], t.call) {
			t.sets = append(t.sets, zero{rhs, field, t.defs})
		}
	case rhs != nil || t.z.addressed(n):
		t.skip[field] = true
	}
}

// initOf returns the composite literal the definition id of a variable
// gives it: E{...} or, for a pointer, &E{...}. ok is false when the
// variable starts with anything else but its zero value.
func (z *zeroChecker) initOf(id *ast.Ident, ptr bool) (*ast.CompositeLit, bool) {
	var rhs ast.Expr
	switch p := z.parents[id].(type) {
	case *ast.AssignStmt:
		if len(p.Lhs) != len(p.Rhs) {
			return nil, false
		}
		for i, l := range p.Lhs {
			if l == id {
				rhs = p.Rhs[i]
			}
		}
	case *ast.ValueSpec:
		if len(p.Values) == 0 {
			return nil, !ptr // var u E is the zero value; var u *E is nil
		}
		if len(p.Names) != len(p.Values) {
			return nil, false
		}
		for i, n := range p.Names {
			if n == id {
				rhs = p.Values[i]
			}
		}
	}
	rhs = ast.Unparen(rhs)
	if ptr {
		if c, ok := rhs.(*ast.CallExpr); ok && len(c.Args) == 1 && isIdent(ast.Unparen(c.Fun), "new") {
			if _, isBuiltin := z.pass.TypesInfo.Uses[ast.Unparen(c.Fun).(*ast.Ident)].(*types.Builtin); isBuiltin {
				return nil, true // new(E)
			}
		}
		u, ok := rhs.(*ast.UnaryExpr)
		if !ok || u.Op != token.AND {
			return nil, false
		}
		rhs = ast.Unparen(u.X)
	}
	lit, ok := rhs.(*ast.CompositeLit)
	return lit, ok
}

// fieldUse classifies a use of a variable: a selection of one of its
// fields, read or set to rhs (nil for a read). ok is false for any other
// use, and for a field changed by ++, --, an op= or a multiple assignment.
func (z *zeroChecker) fieldUse(id *ast.Ident) (field string, rhs ast.Expr, ok bool) {
	sel, isSel := z.parents[id].(*ast.SelectorExpr)
	if !isSel || sel.X != id {
		return "", nil, false
	}
	if s := z.pass.TypesInfo.Selections[sel]; s == nil || s.Kind() != types.FieldVal {
		return "", nil, s != nil && s.Kind() == types.MethodVal && !z.pointerMethod(s)
	}
	switch p := z.parents[sel].(type) {
	case *ast.AssignStmt:
		for i, l := range p.Lhs {
			if l != sel {
				continue
			}
			if p.Tok != token.ASSIGN || len(p.Lhs) != len(p.Rhs) {
				return sel.Sel.Name, sel, true // changed, not to a constant
			}
			return sel.Sel.Name, p.Rhs[i], true
		}
	case *ast.IncDecStmt:
		return sel.Sel.Name, sel, true
	}
	return sel.Sel.Name, nil, true
}

// pointerMethod reports whether a method selection may change its
// receiver: its receiver is a pointer.
func (z *zeroChecker) pointerMethod(s *types.Selection) bool {
	f, ok := s.Obj().(*types.Func)
	if !ok {
		return true
	}
	_, ptr := f.Signature().Recv().Type().(*types.Pointer)
	return ptr
}

// inserted reports whether id is what an Insert or InsertAll call
// inserts: its argument, or the operand of its argument &id.
func (z *zeroChecker) inserted(id *ast.Ident) bool {
	var e ast.Node = id
	if u, ok := z.parents[e].(*ast.UnaryExpr); ok && u.Op == token.AND {
		e = u
	}
	c, ok := z.parents[e].(*ast.CallExpr)
	return ok && len(c.Args) == 1 && c.Args[0] == e && z.insertName(c) != "" && z.insertName(c) != "InsertBulk"
}

// addressed reports whether the field selection over id has its address
// taken.
func (z *zeroChecker) addressed(id *ast.Ident) bool {
	sel := z.parents[id]
	u, ok := z.parents[sel].(*ast.UnaryExpr)
	return ok && u.Op == token.AND
}

// report reports zr unless a directive waives it.
func (z *zeroChecker) report(zr zero) {
	if z.seen[zr.expr.Pos()] || z.ok.waives(z.pass, zr.expr.Pos()) {
		return
	}
	z.seen[zr.expr.Pos()] = true
	val := source(z.pass.TypesInfo.Types[zr.expr].Value)
	f, _ := zr.defs.field(zr.field)
	def := f.Default
	stores := "the insert stores " + def
	if def == "" {
		stores = "the insert stores its DefaultFunc's value"
	}
	how := "a Set of the column in the insert stores " + val
	if zr.defs.Handles != "" {
		how = zr.defs.Handles + "." + zr.field + ".Set(" + val + ") in the insert stores " + val
	}
	z.pass.Reportf(zr.expr.Pos(), "%s is %s, its zero value, which the field's default replaces: %s; %s", zr.field, val, stores, how)
}
