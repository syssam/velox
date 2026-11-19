package codegen

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"math"
	"slices"
	"strings"
)

// enginePkg is the import path of velox's engine, which declares the type
// behind velox.Row.
const enginePkg = veloxPkg + "/internal/engine"

// behaviourCheck checks the uses that behaviour methods reach against the
// entities they serve (CheckBehaviour).
type behaviourCheck struct {
	sc       *sources
	g        *Graph
	errs     []error
	warnings []warning
	seen     map[string]bool // a refusal's call and entity, reported once
	warned   map[string]bool // a warning's position
}

func newBehaviourCheck(sc *sources, g *Graph) *behaviourCheck {
	return &behaviourCheck{sc: sc, g: g, seen: map[string]bool{}, warned: map[string]bool{}}
}

// check checks u, which root r reaches.
func (ck *behaviourCheck) check(r edgeRoot, u use) {
	e := ck.g.Entity(r.entity)
	if e == nil {
		return
	}
	if u.name == "" && u.kind != useSub || u.kind == useSub && u.entity == "" {
		ck.warn(u)
		return
	}
	who := e.Name
	if r.mixin {
		who += ", which lists " + r.typ + ","
	}
	if u.kind != useMutation {
		if e, who = ck.reach(e, who, u.chain); e == nil {
			return
		}
	}
	refuse := func(at ast.Expr, format string, args ...any) {
		key := fmt.Sprintf("%d|%s", u.pos, who)
		if ck.seen[key] {
			return
		}
		ck.seen[key] = true
		pos := ck.sc.fset.Position(u.pos)
		ck.errs = append(ck.errs, &FileError{Path: pos.Filename, Line: pos.Line, Reason: fmt.Sprintf("%s: %s (%s.%s)",
			exprString(at), fmt.Sprintf(format, args...), r.typ, r.method)})
	}
	switch u.kind {
	case useEdge:
		if e.Edge(u.name) == nil {
			refuse(u.call, "%s has no relation %q", who, u.name)
		}
		return
	case useSub:
		ck.checkSub(e, who, u, refuse)
		return
	}
	f, i := columnOf(e, u.name)
	if f == nil {
		at := u.call
		if u.named != nil {
			at = u.named
		}
		refuse(at, "%s has no column %q", who, u.name)
		return
	}
	col := namedColumn{e: e, f: f, id: i == 0}
	what := fmt.Sprintf("column %q of %s", u.name, who)
	switch u.op {
	case "EQ", "NEQ", "GT", "GTE", "LT", "LTE", "In", "NotIn":
		if u.spread {
			return
		}
		for _, a := range u.args {
			if reason := ck.value(col, a, false); reason != "" {
				refuse(u.call, "%s %s", what, reason)
				return
			}
		}
	case "Contains", "HasPrefix", "HasSuffix", "EqualFold", "ContainsFold":
		if k := col.kind(); k != "" && kindClassOf(k) != "string" {
			refuse(u.call, "%s is %s, not text", what, article(col.typeName()))
		}
	case "IsNull", "NotNull":
		if col.notNull() {
			refuse(u.call, "%s is not nullable", what)
		}
	case "Set", "Fill":
		if len(u.args) == 1 {
			if reason := ck.value(col, u.args[0], true); reason != "" {
				refuse(u.call, "%s %s", what, reason)
			}
		}
	}
}

// reach follows chain from e, which who names for errors: through each
// relation to its target, and to the entity a FieldInSelect names. It returns
// the entity the chain ends at and who for it, a nil entity when a step is not
// there, which is refused at its own call.
func (ck *behaviourCheck) reach(e *Entity, who string, chain []string) (*Entity, string) {
	for _, name := range chain {
		owner := e
		if ent, ok := strings.CutPrefix(name, subStep); ok {
			if e, _ = ck.entity(ent); e == nil {
				return nil, ""
			}
			who = fmt.Sprintf("%s, the subquery of %s's FieldInSelect,", e.Name, owner.Name)
			continue
		}
		ed := e.Edge(name)
		if ed == nil {
			return nil, ""
		}
		if e = ck.g.Entity(ed.Target); e == nil {
			return nil, ""
		}
		who = fmt.Sprintf("%s, the target of %s's relation %s,", e.Name, owner.Name, name)
	}
	return e, who
}

// checkSub checks u, a FieldInSelect that the entity e holds: the entity it
// names is one of the package's, both columns it names are there and of one
// type, or an integer column and a text key, which velox compares as text
// (ADR 0091), as the statement requires.
func (ck *behaviourCheck) checkSub(e *Entity, who string, u use, refuse func(ast.Expr, string, ...any)) {
	target, reason := ck.entity(u.entity)
	if target == nil {
		refuse(u.call, "%s: %s", who, reason)
		return
	}
	var own, tgt *namedColumn
	if u.name != "" {
		f, i := columnOf(e, u.name)
		if f == nil {
			refuse(u.call, "%s has no column %q", who, u.name)
			return
		}
		own = &namedColumn{e: e, f: f, id: i == 0}
	}
	if u.key != "" {
		f, i := columnOf(target, u.key)
		if f == nil {
			refuse(u.call, "%s has no column %q", target.Name, u.key)
			return
		}
		tgt = &namedColumn{e: target, f: f, id: i == 0}
	}
	if own != nil && tgt != nil {
		ok, kk := own.kind(), tgt.kind()
		textKey := integerKind[ok] && kk == "string"
		if ok != "" && kk != "" && own.typeName() != tgt.typeName() && !textKey {
			refuse(u.call, "column %q of %s is %s and column %q of %s is %s: a subquery compares columns of one type, or an integer column with a text key",
				u.name, e.Name, article(own.typeName()), u.key, target.Name, article(tgt.typeName()))
			return
		}
	}
}

// integerKind holds the reflect kinds of integers: such a column is compared
// with a text key as text.
var integerKind = map[string]bool{
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
}

// warn records the warning of u, a column, a relation or an entity named by
// a value velox-gen cannot read.
func (ck *behaviourCheck) warn(u use) {
	pos := ck.sc.fset.Position(u.pos)
	key := fmt.Sprintf("%s:%d", pos.Filename, pos.Line)
	at := fmt.Sprintf("%s:%d", key, pos.Column) // one warning per call, however many roots reach it
	if ck.warned[at] {
		return
	}
	ck.warned[at] = true
	call := u.call
	if u.named != nil {
		call = u.named
	}
	what := "column"
	switch u.kind {
	case useEdge:
		what = "relation"
	case useSub:
		what = "entity"
	}
	ck.warnings = append(ck.warnings, warning{pos, fmt.Sprintf("%s: %s names its %s with a value velox-gen cannot read; velox checks it when the statement runs", key, exprString(call), what)})
}

// entity returns the entity of the package that a FieldInSelect names, as
// velox's EntityTable finds it: the one of that name or, failing that, the one
// of that table. When there is none, or the name is ambiguous, it returns nil
// and the reason.
func (ck *behaviourCheck) entity(name string) (*Entity, string) {
	byName := ck.g.Entity(name)
	var tables []*Entity
	for _, e := range ck.g.Entities {
		if e.Table == name {
			tables = append(tables, e)
		}
	}
	switch {
	case byName != nil && (len(tables) > 1 || len(tables) == 1 && tables[0] != byName):
		return nil, fmt.Sprintf("%q is the name of entity %s and the table of another; name the entity", name, byName.Name)
	case byName != nil:
		return byName, ""
	case len(tables) == 1:
		return tables[0], ""
	case len(tables) > 1:
		return nil, fmt.Sprintf("%q is the table of %d entities; name the entity", name, len(tables))
	}
	return nil, fmt.Sprintf("%q is neither a schema nor a table of this package", name)
}

// columnOf returns e's field whose column is name, and its index.
func columnOf(e *Entity, name string) (*Field, int) {
	for i, f := range e.Fields {
		if f.Column == name {
			return f, i
		}
	}
	return nil, -1
}

// namedColumn is a column a call names.
type namedColumn struct {
	e  *Entity
	f  *Field
	id bool // the entity's ID
}

// kind is the reflect kind of the column's value, as a condition or a
// mutation by name takes it: its Go type without the pointer of a nullable
// field; "" when velox-gen cannot tell.
func (c namedColumn) kind() string {
	f := c.f
	if f.GoType != nil {
		switch f.GoType.Kind {
		case "ptr", "interface", "":
			return ""
		}
		return f.GoType.Kind
	}
	switch {
	case f.Type == "bool", numeric[f.Type], f.Type == "string":
		return f.Type
	case f.Type == "enum":
		return "string"
	case f.Type == "bytes":
		return "slice"
	case f.Type == "time":
		return "struct"
	}
	return ""
}

// typeName is the column's value type as Go writes it, for errors.
func (c namedColumn) typeName() string {
	f := c.f
	switch {
	case f.GoType != nil:
		return f.GoType.Ident
	case f.Type == "bytes":
		return "[]byte"
	case f.Type == "time":
		return "time.Time"
	case f.Type == "enum":
		return c.e.Name + f.GoName
	}
	return f.Type
}

// notNull reports whether the column never holds NULL: the ID, and a field
// that is neither Nillable nor an Optional JSON field.
func (c namedColumn) notNull() bool {
	return c.id || !c.f.Nillable && (c.f.Type != "json" || !c.f.Optional)
}

// value returns why the value x does not fit the column c, "" when it fits
// or velox-gen cannot tell. set says x is a Set or Fill value, for which
// nil sets NULL.
func (ck *behaviourCheck) value(c namedColumn, x ast.Expr, set bool) string {
	tv := ck.sc.info.Types[x]
	if tv.IsNil() {
		switch {
		case !set:
			return "is compared with nil: compare it with IsNull or NotNull"
		case c.notNull():
			return "is not nullable: it takes no nil"
		}
		return ""
	}
	t := tv.Type
	if t == nil || types.IsInterface(t) {
		return ""
	}
	want := c.kind()
	if want == "" {
		return ""
	}
	got := typeKind(t)
	if got == "" {
		return ""
	}
	if kindClassOf(got) != kindClassOf(want) {
		return fmt.Sprintf("is %s, not %s", article(c.typeName()), article(types.TypeString(t, (*types.Package).Name)))
	}
	if tv.Value != nil && !constFits(tv.Value, want) {
		return fmt.Sprintf("is %s, which cannot hold %s", article(c.typeName()), tv.Value)
	}
	return ""
}

// typeKind is the reflect kind of a value of type t, "" for one velox-gen
// does not compare.
func typeKind(t types.Type) string {
	switch u := t.Underlying().(type) {
	case *types.Basic:
		switch u.Kind() {
		case types.Bool:
			return "bool"
		case types.String:
			return "string"
		case types.Int, types.Int8, types.Int16, types.Int32, types.Int64,
			types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uint64, types.Uintptr,
			types.Float32, types.Float64, types.Complex64, types.Complex128:
			return u.Name()
		}
	case *types.Struct:
		return "struct"
	case *types.Array:
		return "array"
	case *types.Slice:
		return "slice"
	case *types.Map:
		return "map"
	case *types.Pointer:
		return "ptr"
	case *types.Signature:
		return "func"
	case *types.Chan:
		return "chan"
	}
	return ""
}

// kindClassOf is the class of the reflect kind k whose values velox
// converts into one another at run time: one for the integers and one for
// the floats.
func kindClassOf(k string) string {
	switch k {
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "uintptr":
		return "integer"
	case "float32", "float64":
		return "float"
	}
	return k
}

// constFits reports whether the constant v fits a value of the kind k, whose
// class is v's: a number in range. velox's conversion at run time refuses
// one out of range. An int or uint is taken to be 64 bits wide.
func constFits(v constant.Value, k string) bool {
	switch kindClassOf(k) {
	case "integer":
		v = constant.ToInt(v)
		if v.Kind() != constant.Int {
			return true
		}
		lo, hi := intRange(k)
		return constant.Compare(v, token.GEQ, lo) && constant.Compare(v, token.LEQ, hi)
	case "float":
		if k != "float32" {
			return true
		}
		f, _ := constant.Float64Val(constant.ToFloat(v))
		return math.Abs(f) <= math.MaxFloat32
	}
	return true
}

// intRange is the least and the greatest value of the integer kind k.
func intRange(k string) (lo, hi constant.Value) {
	switch k {
	case "int8":
		return constant.MakeInt64(math.MinInt8), constant.MakeInt64(math.MaxInt8)
	case "int16":
		return constant.MakeInt64(math.MinInt16), constant.MakeInt64(math.MaxInt16)
	case "int32":
		return constant.MakeInt64(math.MinInt32), constant.MakeInt64(math.MaxInt32)
	case "uint8":
		return constant.MakeInt64(0), constant.MakeUint64(math.MaxUint8)
	case "uint16":
		return constant.MakeInt64(0), constant.MakeUint64(math.MaxUint16)
	case "uint32":
		return constant.MakeInt64(0), constant.MakeUint64(math.MaxUint32)
	case "uint", "uint64", "uintptr":
		return constant.MakeInt64(0), constant.MakeUint64(math.MaxUint64)
	}
	return constant.MakeInt64(math.MinInt64), constant.MakeInt64(math.MaxInt64)
}

// article is s with its indefinite article: "an int64", "a uint8".
func article(s string) string {
	if s != "" && strings.ContainsRune("aeiouAEIOU", rune(s[0])) && !strings.HasPrefix(s, "uint") {
		return "an " + s
	}
	return "a " + s
}

// warning is a warning's text and the position it names.
type warning struct {
	pos  token.Position
	text string
}

// warningList is the warnings' texts, by file and line.
func (ck *behaviourCheck) warningList() []string {
	slices.SortFunc(ck.warnings, func(a, b warning) int {
		if c := strings.Compare(a.pos.Filename, b.pos.Filename); c != 0 {
			return c
		}
		return a.pos.Line - b.pos.Line
	})
	var out []string
	for _, w := range ck.warnings {
		out = append(out, w.text)
	}
	return out
}

// byLine is errs with its *FileErrors by file and line, in a stable order:
// one call's refusals stay in the order of the methods that reach it.
func byLine(errs []error) []error {
	slices.SortStableFunc(errs, func(a, b error) int {
		fa, oka := a.(*FileError)
		fb, okb := b.(*FileError)
		if !oka || !okb {
			return 0
		}
		if c := strings.Compare(fa.Path, fb.Path); c != 0 {
			return c
		}
		return fa.Line - fb.Line
	})
	return errs
}
