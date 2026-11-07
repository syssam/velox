package codegen

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"maps"
	"path"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/syssam/velox/internal/codegen/loadjson"
	"github.com/syssam/velox/plugin"
)

// reserved are the Go names of the methods an entity has, which no field or
// edge may take.
var reserved = map[string]bool{"VeloxMeta": true, "String": true, "GoString": true, "Policy": true, "Hooks": true, "Interceptors": true}

// Lower maps g to the model Render writes: Go names and types, handle kinds,
// enums, the join entities of many-to-many relations, and the code of
// defaults and checks. It returns every rule the output would break
// (a name taken twice, an import name clash), as *Error values joined with
// errors.Join, ordered by schema name.
//
// Lower does not change g. Two calls with equal Graphs and Configs give equal
// results.
func Lower(g *Graph, cfg Config) (*Out, error) {
	l := &lowerer{
		g: g, cfg: cfg,
		out: &Out{Package: cmp.Or(cfg.Package, "gen"), SchemaPath: cmp.Or(cfg.SchemaPath, g.Package), UserDoc: cfg.UserDoc,
			GenPath: cfg.GenPath},
		joins: map[string]*join{},
	}
	l.owned = map[string]string{
		"velox": "github.com/syssam/velox", "errors": "errors", "fmt": "fmt",
		"regexp": "regexp", "time": "time", "json": "encoding/json", "utf8": "unicode/utf8",
	}
	l.schemaNames()
	l.entities()
	l.relations()
	l.finish()
	for _, ge := range l.g.Entities {
		if l.schemaValue(ge) {
			l.out.NeedsSch = true
		}
	}
	if len(l.errs) > 0 {
		slices.SortStableFunc(l.errs, func(x, y *Error) int { return strings.Compare(x.Schema, y.Schema) })
		errs := make([]error, len(l.errs))
		for i, e := range l.errs {
			errs[i] = e
		}
		return nil, errors.Join(errs...)
	}
	for _, e := range l.out.Entities {
		if e.Bidi && e.Bound.PolicyConds != "" {
			return nil, bidiPolicyError(e)
		}
	}
	return l.out, nil
}

// behaviourKinds are the kinds of an entity's behaviour.
var behaviourKinds = []string{"Policy", "Hooks", "Interceptors"}

// schemaNames picks the names the generated code gives the schema package
// and velox's schema package: their own, schema for velox's, unless a name
// is taken, by the generated code or a field type's package. Then the
// schema package is userschema, and velox's is veloxschema.
func (l *lowerer) schemaNames() {
	taken := maps.Clone(l.owned)
	taken[l.out.Package] = ""
	for _, e := range l.g.Entities {
		for _, f := range e.Fields {
			if f.GoType != nil {
				for _, p := range importNames(f.GoType) {
					if _, ok := taken[p.name]; !ok {
						taken[p.name] = p.path
					}
				}
			}
		}
	}
	free := func(n, p string) bool {
		old, ok := taken[n]
		return !ok || old == p
	}
	pick := func(want, alt, p string) string {
		n := want
		for i := 1; !free(n, p); i++ {
			n = alt
			if i > 1 {
				n = alt + strconv.Itoa(i)
			}
		}
		taken[n] = p
		l.owned[n] = p
		return n
	}
	l.out.SchemaName = pick(cmp.Or(l.cfg.SchemaName, guessName(l.out.SchemaPath)), "userschema", l.out.SchemaPath)
	l.vschema = pick("schema", "veloxschema", schemaPkg)
}

// schemaValue sets the Schema of ge's entity, the value its Descriptor names
// for velox to read its behaviour from, when the schema or a mixin it lists
// declares a Policy, Hooks or Interceptors method. It is a pointer when the
// schema implements schema.Interface through one. It reports whether it
// set it, which the file reads from the schema package.
func (l *lowerer) schemaValue(ge *Entity) bool {
	s := l.state[ge.Name]
	if s == nil {
		return false
	}
	e := s.out
	b := e.Bound
	if !b.Policy && !b.Hooks && !b.Interceptors && len(l.cfg.Mixins[ge.Name]) == 0 {
		return false
	}
	e.Schema = l.out.SchemaName + "." + e.Name + "{}"
	if ge.Pointer {
		e.Schema = "&" + e.Schema
	}
	e.NeedsSch = true
	return true
}

// bidiPolicyError is the *FileError of the Policy of e, the join entity of a
// bidirectional edge, that sets Read or Write where Bound.PolicyConds says.
func bidiPolicyError(e *OutEntity) error {
	at := e.Bound.PolicyConds
	i := strings.LastIndexByte(at, ':')
	line, _ := strconv.Atoi(at[i+1:])
	return &FileError{Path: at[:i], Line: line, Reason: e.Name + ".Policy sets Read or Write: the join entity of a bidirectional edge allows only Insert, " +
		"as a condition would show or delete one row of a link"}
}

// lowerer holds the state of one Lower.
type lowerer struct {
	g     *Graph
	cfg   Config
	out   *Out
	errs  []*Error
	state map[string]*entState // by entity name
	joins map[string]*join     // by join table
	owned map[string]string    // the package names the generated code uses, with their paths
	enums []*enumDecl
	// vschema is the name the generated code gives velox's schema package.
	vschema string
}

// join is a join entity with the edge that made it.
type join struct {
	ent   *OutEntity
	st    *entState
	owner string // the schema of the edge
	where string
}

// enumDecl is a generated enum type with the field that declares it.
type enumDecl struct {
	enum          *OutEnum
	schema, where string
}

// entState is the state of lowering one entity.
type entState struct {
	l       *lowerer
	name    string // the schema or join entity's name, for errors
	out     *OutEntity
	paths   map[string]string // import name → path
	sch     bool              // the code reads the schema package
	regexps int
	reads   int               // values read from descriptors into setup
	descs   map[string]string // a field's name → the setup variable of its descriptor
	setup   []string
	cases   []string
}

func (s *entState) fail(where, format string, args ...any) {
	s.l.errs = append(s.l.errs, &Error{Schema: s.name, Where: where, Reason: fmt.Sprintf(format, args...)})
}

// use adds the import path p, which the code refers to as name. Two paths with
// one name are an error.
func (s *entState) use(p, name, where string) {
	if owner, ok := s.l.owned[name]; ok && owner != p {
		s.fail(where, "imports %s, but the generated code uses the name %s for %s", p, name, owner)
		return
	}
	if old, ok := s.paths[name]; ok && old != p {
		s.fail(where, "imports %s and %s both named %s", old, p, name)
		return
	}
	s.paths[name] = p
}

// entities lowers the graph's entities (not their relations yet).
func (l *lowerer) entities() {
	l.state = make(map[string]*entState, len(l.g.Entities))
	for _, e := range l.g.Entities {
		s := &entState{l: l, name: e.Name, paths: map[string]string{}}
		s.out = &OutEntity{
			Name: e.Name, Label: e.Label, Handles: plugin.GoPlural(e.Name), Table: e.Table, TableSchema: e.TableSchema,
			Bound: l.cfg.Bound[e.Name],
		}
		if raw, ok := e.Annotations["Comment"]; ok {
			var c struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(raw, &c); err != nil {
				s.fail("", "its Comment annotation is not a comment: %v", err)
			}
			s.out.Comment = c.Text
		}
		for i, f := range e.Fields {
			if isDeclared(f) {
				s.exported(f)
			}
			s.out.Fields = append(s.out.Fields, s.field(e, i, f))
		}
		s.out.PrimaryKey = []int{0}
		for i, ix := range e.Indexes {
			oi := OutIndex{Columns: []int{}, Unique: ix.Unique && ix.Where == ""} // a partial index is not unique for the table
			for _, c := range ix.Columns {
				if j := columnIndex(e, c); j >= 0 {
					oi.Columns = append(oi.Columns, j)
				} else {
					s.fail(indexAt(i), "column %q is not a field's", c)
				}
			}
			s.out.Indexes = append(s.out.Indexes, oi)
		}
		// A field's Unique is a single-column unique index, after the
		// declared ones, which the runtime's uniqueness rules read. The ID is
		// unique by being the primary key. A declared full unique index on the
		// field alone already is that index, and is not repeated.
		for i, f := range e.Fields {
			if !f.Unique || i == 0 {
				continue
			}
			ix := OutIndex{Columns: []int{i}, Unique: true}
			if !slices.ContainsFunc(s.out.Indexes, func(d OutIndex) bool { return d.Unique && slices.Equal(d.Columns, ix.Columns) }) {
				s.out.Indexes = append(s.out.Indexes, ix)
			}
		}
		l.state[e.Name] = s
		l.out.Entities = append(l.out.Entities, s.out)
	}
}

// exported refuses a GoType of f that names an unexported type, such as
// schema.cents or the element of []*other.point, with f's position. The
// generated code refers to a GoType by its Ident, through an import of its
// package, so each name in it must be exported. (A type of the generated
// package itself cannot be a GoType: the schema package, which the
// generated code imports, would import it back.)
func (s *entState) exported(f *Field) {
	if f.GoType == nil {
		return
	}
	if q := unexportedName(f.GoType.Ident); q != "" {
		s.l.errs = append(s.l.errs, &Error{Pos: f.Pos, Schema: s.name, Where: fieldAt(f.Name),
			Reason: fmt.Sprintf("its Go type %s names %s, which is unexported: the generated package cannot refer to it; export it", f.GoType.Ident, q)})
	}
}

// unexportedName is the first qualified name in the type expression ident,
// such as "schema.cents" in "[]*schema.cents", whose name is unexported, or "".
func unexportedName(ident string) string {
	x, err := parser.ParseExpr(ident)
	if err != nil {
		return ""
	}
	var q string
	ast.Inspect(x, func(n ast.Node) bool {
		if s, ok := n.(*ast.SelectorExpr); ok && q == "" && !ast.IsExported(s.Sel.Name) {
			if pkg, ok := s.X.(*ast.Ident); ok {
				q = pkg.Name + "." + s.Sel.Name
			}
		}
		return q == ""
	})
	return q
}

func columnIndex(e *Entity, column string) int {
	return slices.IndexFunc(e.Fields, func(f *Field) bool { return f.Column == column })
}

// colConst is the name of the constant for column i of o, which renderEntity
// declares: the position the handles, the descriptor and Check share.
func colConst(o *OutEntity, i int) string {
	if i < 0 || i >= len(o.Fields) {
		return strconv.Itoa(i) // reported as an error elsewhere; the code is not written
	}
	return camel(o.Label) + "Col" + o.Fields[i].GoName
}

// edgeConst is the name of the constant for edge i of o.
func edgeConst(o *OutEntity, i int) string {
	return camel(o.Label) + "Edge" + o.Rels[i].GoName
}

func fieldIndex(e *Entity, name string) int {
	return slices.IndexFunc(e.Fields, func(f *Field) bool { return f.Name == name })
}

// nilKinds are the kinds of Go types whose zero value is nil, so a field of one
// is never a pointer: nil already means NULL. They are the kinds a column can
// hold NULL for without a pointer; rule 49 refuses a JSON field of any other
// kind that holds nil.
var nilKinds = map[string]bool{"slice": true, "map": true, "ptr": true}

// field lowers the field f, column i of e (nil for a join entity).
func (s *entState) field(e *Entity, i int, f *Field) *OutField {
	where := fieldAt(f.Name)
	o := &OutField{
		GoName: f.GoName, Column: f.Column, Name: f.Name, Comment: f.Comment,
		Sensitive: f.Sensitive, Immutable: f.Immutable, JSON: f.Type == "json", Implicit: f.Implicit != "",
	}
	if reserved[f.GoName] {
		s.fail(where, "its Go name %s is reserved: it is a method of the entity", f.GoName)
	}
	if e != nil && f.Nillable {
		// A Required edge whose key is NULL-able: velox refuses it nil, as
		// ent's create check does, since the database cannot.
		for _, ed := range e.Edges {
			if ed.Required && ed.Field == f.Name && ownFK(ed) {
				o.RequiredEdge = ed.Name
			}
		}
	}
	elem, kind := s.elemType(f, where)
	o.ElemType = elem
	// Only Nillable makes a pointer: a Default never changes the Go type.
	o.Pointer = f.Nillable && i != 0 && !nilKinds[kind]
	// A column holds NULL when the plugin model's Nullable says so: a
	// Nillable field, or an Optional JSON one. A pointer says so itself; a
	// slice or a map, whose nil is NULL there, says it in its ColumnDescriptor: a
	// JSON one, and a Nillable bytes field.
	o.NilIsNull = i != 0 && !o.Pointer && (kind == "slice" || kind == "map") &&
		(o.JSON && (f.Optional || f.Nillable) || f.Type == "bytes" && f.Nillable)
	// An Optional field that is a value is unset at its zero value, which
	// its validators let pass.
	// A bytes field is a slice, never a pointer, and nil is unset.
	if f.Optional && !o.Pointer && i != 0 && (!nilKinds[kind] || f.Type == "bytes") {
		o.Unset = unset(f, o.ElemType)
	}
	o.GoType = elem
	if o.Pointer {
		o.GoType = "*" + elem
	}
	if f.GoType != nil {
		for _, p := range importNames(f.GoType) {
			s.use(p.path, p.name, where)
		}
	}
	if f.GoType == nil && f.Type == "time" {
		s.use("time", "time", where)
	}
	if o.JSON {
		// The codec: the generated file, not the core, imports encoding/json.
		s.use("encoding/json", "json", where)
	}
	var h string
	switch {
	case o.JSON:
		// Stored as JSON: no condition or order compares it, as equality and
		// order of JSON differ by dialect, so its handle only sets it.
		h = "JSONCol"
	case f.Type == "time" || elem == "time.Time":
		h = "OrderedCol"
	case numeric[kind]:
		h = "NumberCol"
	case f.Type == "enum":
		// An enum compares by equality and orders, as ent's predicates; it
		// has no Contains or Like, which no value of it would need.
		h = "Col"
	case kind == "string":
		h = "StringCol"
	default:
		h = "Col"
	}
	if o.Pointer {
		h = "Null" + h
	}
	o.Handle = fmt.Sprintf("velox.%s[%s, %s]", h, s.name, elem)
	if f.Sensitive && f.StructTag != "" && reflect.StructTag(f.StructTag).Get("json") != "-" {
		s.fail(where, "a Sensitive field's StructTag must not expose it to JSON")
	}
	switch {
	case f.StructTag != "":
		o.Tag = quoteTag(f.StructTag)
	case f.Sensitive, f.Implicit != "":
		// An implicit key is ent's unexported field: the edge is how JSON
		// and String show it.
		o.Tag = quoteTag(`json:"-"`)
	case !o.Pointer && !f.Optional && (scalar(f, kind) || kind == "struct" || kind == "array"):
		// A required value is always there: omitempty would drop a stored 0,
		// false or "", omitzero a zero time, and a client could not tell it
		// from a missing field.
		o.Tag = quoteTag(fmt.Sprintf(`json:"%s"`, f.Name))
	case !o.Pointer && (kind == "struct" || kind == "array"):
		// An optional struct or array: omitempty never omits one, and
		// omitzero omits its zero value, such as a zero time.Time or UUID.
		o.Tag = quoteTag(fmt.Sprintf(`json:"%s,omitzero"`, f.Name))
	default:
		o.Tag = quoteTag(fmt.Sprintf(`json:"%s,omitempty"`, f.Name))
	}
	s.defaults(e, f, o, where)
	s.checks(e, i, f, o, where)
	return o
}

// scalar reports whether f holds a bool, a number, a string or an enum: a
// value that has a zero that is a real value, unlike a slice or a map, whose
// nil means NULL.
func scalar(f *Field, kind string) bool {
	switch f.Type {
	case "bool", "string", "enum":
		return true
	}
	return numeric[f.Type] && kind != "slice" && kind != "map"
}

func quoteTag(tag string) string {
	if strings.Contains(tag, "`") {
		return strconv.Quote(tag)
	}
	return "`" + tag + "`"
}

// elemType is the Go type of f without a pointer, and the reflect kind of it.
// It declares the enum type of an enum field with no Go type.
func (s *entState) elemType(f *Field, where string) (elem, kind string) {
	if f.GoType != nil {
		return f.GoType.Ident, f.GoType.Kind
	}
	switch {
	case f.Type == "bool", numeric[f.Type]:
		return f.Type, f.Type
	case f.Type == "string":
		return "string", "string"
	case f.Type == "bytes":
		return "[]byte", "slice"
	case f.Type == "time":
		return "time.Time", "struct"
	case f.Type == "enum":
		t := s.name + f.GoName
		en := &OutEnum{Type: t, Of: s.name + "'s " + f.Name + " field"}
		for _, v := range f.Enums {
			en.Consts = append(en.Consts, t+plugin.GoName(v.Name))
			en.Values = append(en.Values, v.Value)
		}
		s.l.enums = append(s.l.enums, &enumDecl{enum: en, schema: s.name, where: where})
		return t, "string"
	}
	s.fail(where, "a field of type %s needs a Go type", f.Type)
	return "any", "interface"
}

// importNames pairs each import path of g with the name the type's Ident
// refers to it by: the Ident's qualifier that is the path's last element, or
// else the one qualifier left over.
func importNames(g *loadjson.GoType) []struct{ path, name string } {
	qs := qualifiers(g.Ident)
	names := make([]string, len(g.Imports))
	var restP []int
	for i, p := range g.Imports {
		if b := path.Base(p); slices.Contains(qs, b) {
			names[i] = b
		} else {
			restP = append(restP, i)
		}
	}
	var restQ []string
	for _, q := range qs {
		if !slices.Contains(names, q) {
			restQ = append(restQ, q)
		}
	}
	for _, i := range restP {
		names[i] = path.Base(g.Imports[i])
		if len(restP) == 1 && len(restQ) == 1 {
			names[i] = restQ[0]
		}
	}
	out := make([]struct{ path, name string }, len(g.Imports))
	for i, p := range g.Imports {
		out[i].path, out[i].name = p, names[i]
	}
	return out
}

// qualifiers returns the package names a type expression refers to, such as
// "map[string]*http.Request" → [http], in order and once each.
func qualifiers(ident string) []string {
	var qs []string
	isID := func(c byte) bool {
		return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
	}
	for i := 0; i < len(ident); {
		if !isID(ident[i]) {
			i++
			continue
		}
		j := i
		for j < len(ident) && isID(ident[j]) {
			j++
		}
		if j < len(ident) && ident[j] == '.' && !slices.Contains(qs, ident[i:j]) {
			qs = append(qs, ident[i:j])
		}
		i = j
		if i < len(ident) && ident[i] == '.' {
			i++
			for i < len(ident) && isID(ident[i]) { // skip the selected name
				i++
			}
		}
	}
	return qs
}

// schemaPkg is the import path of the schema DSL package, whose
// FieldDescriptor reads a field's descriptor by its name.
const schemaPkg = "github.com/syssam/velox/schema"

// descriptor returns the variable of the setup block that holds f's
// descriptor, read from the schema package by the field's name, in plain
// mode. The first call for f declares it, so each descriptor is read once.
func (s *entState) descriptor(e *Entity, f *Field) string {
	if v, ok := s.descs[f.Name]; ok {
		return v
	}
	s.sch = true
	if s.descs == nil {
		s.descs = map[string]string{}
	}
	v := "f" + strconv.Itoa(len(s.descs))
	s.descs[f.Name] = v
	s.setup = append(s.setup, fmt.Sprintf("%s := %s.FieldDescriptor(&%s.%s{}, %s)", v, s.l.vschema, s.l.out.SchemaName, e.Name, strconv.Quote(f.Name)))
	return v
}

// schemaFunc returns the expression, of the type typ, of a function of f
// that the schema holds: its descriptor's member (DefaultFunc, Validators[0]).
// ref names the function. It is the function itself, pkg.Name, when ref
// names one the generated package can import, of the type typ; else a
// variable of the setup block that reads the descriptor once.
func (s *entState) schemaFunc(e *Entity, f *Field, ref *loadjson.FuncRef, member, typ, where string) string {
	if ref != nil && ref.Pkg != "" && ref.Type == typ {
		if p, ok := s.l.cfg.Funcs[ref.Pkg]; ok && p.Gen {
			name := p.Name
			if ref.Pkg == s.l.out.SchemaPath {
				name, s.sch = s.l.out.SchemaName, true
			}
			if s.free(ref.Pkg, name) {
				if ref.Pkg != s.l.out.SchemaPath {
					s.use(ref.Pkg, name, where)
				}
				return name + "." + ref.Name
			}
		}
	}
	s.use(schemaPkg, s.l.vschema, where)
	fd := s.descriptor(e, f)
	v := "d" + strconv.Itoa(s.reads)
	s.reads++
	s.setup = append(s.setup, fmt.Sprintf("%s := %s.%s.(%s)", v, fd, member, typ))
	return v
}

// free reports whether the entity's file can import the package at p by
// name: no other package the generated code uses has the name.
func (s *entState) free(p, name string) bool {
	if owner, ok := s.l.owned[name]; ok && owner != p {
		return false
	}
	old, ok := s.paths[name]
	return !ok || old == p
}

// defaults writes the code of f's Default, DefaultFunc and UpdateDefault.
// A function is called by its name when it has one the generated code can
// use (see schemaFunc), and is read from the schema once otherwise, so the
// closure of a column reads no descriptor per row.
func (s *entState) defaults(e *Entity, f *Field, o *OutField, where string) {
	elem := o.ElemType
	// call returns the column's closure, which calls the function member.
	call := func(member, suffix string, ref *loadjson.FuncRef) string {
		if f.Type == "time" && elem != "time.Time" {
			// A time default is a func() time.Time, which the builder let
			// through only for a GoType that converts from time.Time.
			s.use("time", "time", where)
			d := s.schemaFunc(e, f, ref, member, "func() time.Time", where)
			return fmt.Sprintf("func() any { return %s(%s()) }", elem, d)
		}
		d := s.schemaFunc(e, f, ref, member, "func() "+elem, where)
		return fmt.Sprintf("func() any { return %s() }", d)
	}
	if len(f.Default) > 0 {
		lit, ok := s.literal(f, elem, where)
		switch {
		case ok && lit != "":
			o.DefaultCode = lit
		case !ok:
			// An error is recorded.
		case f.Type == "json" && sliceLiteral(f.Default, elem) != "":
			lit := sliceLiteral(f.Default, elem)
			o.DefaultFuncCode = fmt.Sprintf("func() any { return %s }", lit)
		case f.Type == "bytes" || f.Type == "json":
			// Decoded on each call, so that no two rows share a slice or a map.
			s.use("encoding/json", "json", where)
			o.DefaultFuncCode = fmt.Sprintf("func() any { var v %s; _ = json.Unmarshal([]byte(%s), &v); return v }", elem, strconv.Quote(compact(f.Default)))
		case !f.DefaultFunc: // else DefaultFunc below replaces it, and nothing reads this
			d := s.schemaValue(e, f, "Default", where)
			o.DefaultFuncCode = fmt.Sprintf("func() any { return %s }", d)
		}
	}
	if f.DefaultFunc {
		if f.Type == "time" || f.Type == "uuid" {
			o.DefaultFuncCode = call("Default", "Default", f.DefaultRef) // their Default takes a func
		} else {
			o.DefaultFuncCode = call("DefaultFunc", "Default", f.DefaultRef)
		}
	}
	if f.UpdateDefault {
		o.UpdateDefaultCode = call("UpdateDefault", "UpdateDefault", f.UpdateDefaultRef)
	}
}

// schemaValue returns the expression of a value of f's descriptor, its
// member, of type any, that is not a function: a variable of the setup
// block.
func (s *entState) schemaValue(e *Entity, f *Field, member, where string) string {
	s.use(schemaPkg, s.l.vschema, where)
	fd := s.descriptor(e, f)
	v := "d" + strconv.Itoa(s.reads)
	s.reads++
	s.setup = append(s.setup, fmt.Sprintf("%s := %s.%s", v, fd, member))
	return v
}

// literal writes f's literal Default as a Go literal of the element type
// elem. It returns "" for a Default that is not a literal on a bool, number,
// string or enum, which is read from the descriptor; !ok means it is wrong.
func (s *entState) literal(f *Field, elem, where string) (lit string, ok bool) {
	raw := string(f.Default)
	conv := func(l string) string {
		if f.GoType != nil || elem != f.Type {
			return elem + "(" + l + ")"
		}
		return l
	}
	switch {
	case f.Type == "bool":
		var v bool
		if json.Unmarshal(f.Default, &v) != nil {
			s.fail(where, "its Default %s is not a bool", raw)
			return "", false
		}
		return conv(strconv.FormatBool(v)), true
	case numeric[f.Type]:
		if !fits(f.Type, raw) {
			s.fail(where, "its Default %s is not a %s", raw, f.Type)
			return "", false
		}
		return elem + "(" + raw + ")", true
	case f.Type == "string", f.Type == "enum":
		var v string
		if json.Unmarshal(f.Default, &v) != nil {
			s.fail(where, "its Default %s is not a string", raw)
			return "", false
		}
		if f.Type == "enum" && f.GoType == nil {
			for _, en := range f.Enums {
				if en.Value == v {
					return elem + plugin.GoName(en.Name), true
				}
			}
			s.fail(where, "its Default %s is not one of the enum values", raw)
			return "", false
		}
		q := strconv.Quote(v)
		if f.GoType != nil {
			q = elem + "(" + q + ")"
		}
		return q, true
	}
	return "", true
}

// sliceLiteral is the Go literal of the JSON array raw as a value of elem,
// when elem is a slice of bool, number or string ([]string{"new"}), and "" for
// any other default, which is decoded from JSON. A call makes a new slice, so
// no two rows share one.
func sliceLiteral(raw []byte, elem string) string {
	t, ok := strings.CutPrefix(elem, "[]")
	if !ok {
		return ""
	}
	switch {
	case t == "bool", t == "string", numeric[t]:
	default:
		return ""
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil || items == nil {
		return ""
	}
	lits := make([]string, len(items))
	for i, it := range items {
		switch t {
		case "bool":
			var v bool
			if json.Unmarshal(it, &v) != nil {
				return ""
			}
			lits[i] = strconv.FormatBool(v)
		case "string":
			var v string
			if json.Unmarshal(it, &v) != nil {
				return ""
			}
			lits[i] = strconv.Quote(v)
		default:
			if !fits(t, string(it)) {
				return ""
			}
			lits[i] = string(it)
		}
	}
	return elem + "{" + strings.Join(lits, ", ") + "}"
}

// compact is the JSON raw without insignificant white space.
func compact(raw []byte) string {
	var b bytes.Buffer
	if json.Compact(&b, raw) != nil {
		return string(raw)
	}
	return b.String()
}

// fits reports whether the JSON number raw is a value of the number type t:
// an integer in its range (no sign on an unsigned type), or a float that does
// not overflow.
func fits(t, raw string) bool {
	bits := 64
	if n := strings.TrimLeft(t, "uinfloat"); n != "" {
		bits, _ = strconv.Atoi(n)
	}
	var err error
	switch {
	case strings.HasPrefix(t, "float"):
		_, err = strconv.ParseFloat(raw, bits)
	case strings.HasPrefix(t, "u"):
		_, err = strconv.ParseUint(raw, 10, bits)
	default:
		_, err = strconv.ParseInt(raw, 10, bits)
	}
	return err == nil
}

// base is the Go type of the values a field's Validate functions take: the
// builder's, whatever the field's Go type is.
func base(f *Field) string {
	switch {
	case f.Type == "bool", numeric[f.Type], f.Type == "string":
		return f.Type
	case f.Type == "bytes":
		return "[]byte"
	}
	return ""
}

// unset is the condition on x, the value of the Optional value field f of
// the Go type elem, that means it is unset: its zero value. It is "" for a
// type whose zero value cannot be told by ==.
func unset(f *Field, elem string) string {
	switch {
	case f.Type == "bytes":
		return "len(x) == 0"
	case f.Type == "time":
		return "x.IsZero()"
	case f.Type == "bool":
		return "!x"
	case numeric[f.Type]:
		return "x == 0"
	case f.Type == "string", f.Type == "enum":
		return `x == ""`
	case f.Type == "uuid":
		return "x == *new(" + elem + ")"
	}
	return ""
}

// checks writes the case of Check's switch for column col, when f
// has checks, an enum or validators.
func (s *entState) checks(e *Entity, col int, f *Field, o *OutField, where string) {
	if len(f.Checks) == 0 && f.Type != "enum" && f.Validators == 0 {
		return
	}
	var b strings.Builder
	line := func(depth int, format string, args ...any) {
		b.WriteString(strings.Repeat("\t", depth))
		fmt.Fprintf(&b, format, args...)
		b.WriteByte('\n')
	}
	fail := func(msg string) {
		s.useStd("errors", where)
		line(2, "return errors.New(%s)", strconv.Quote(msg))
	}
	line(0, "case %sCol%s:", camel(s.out.Label), o.GoName)
	line(1, "x := v.(%s)", o.ElemType)
	if o.Unset != "" {
		// Optional: the zero value means unset, and unset is valid.
		line(1, "if %s {", o.Unset)
		line(2, "return nil")
		line(1, "}")
	}
	header := b.Len() // what is written past here is a check that can fail
	if f.Type == "enum" {
		vals := make([]string, len(f.Enums))
		plain := make([]string, len(f.Enums))
		for i, en := range f.Enums {
			vals[i] = strconv.Quote(en.Value)
			plain[i] = en.Value
		}
		msg := strconv.Quote("is not one of " + strings.Join(plain, ", "))
		if f.GoType == nil {
			// The enum type is generated, and has IsValid.
			line(1, "if !x.IsValid() {")
			line(2, "return errors.New(%s)", msg)
			line(1, "}")
		} else {
			line(1, "switch x {")
			line(1, "case %s:", strings.Join(vals, ", "))
			line(1, "default:")
			line(1, "\treturn errors.New(%s)", msg)
			line(1, "}")
		}
		s.useStd("errors", where)
	}
	// conv is x as the type t, when its type is another.
	conv := func(t string) string {
		if o.ElemType == t {
			return "x"
		}
		return t + "(x)"
	}
	for _, c := range f.Checks {
		args := make([]string, len(c.Args))
		for i, a := range c.Args {
			args[i] = strings.TrimSpace(string(a))
		}
		// num is the argument i as a literal of the element type, and as written.
		num := func(i int) (lit, text string, ok bool) {
			if i >= len(args) {
				s.fail(where, "check %s needs an argument", c.Kind)
				return "", "", false
			}
			if !fits(f.Type, args[i]) {
				s.fail(where, "check %s: %s is not a %s", c.Kind, args[i], f.Type)
				return "", "", false
			}
			if o.ElemType == f.Type {
				return args[i], args[i], true // an untyped constant is already of the type
			}
			return o.ElemType + "(" + args[i] + ")", args[i], true
		}
		switch c.Kind {
		case "NotEmpty":
			if o.Unset != "" {
				continue // "" is unset and passes: the check could never fail
			}
			switch f.Type {
			case "string":
				line(1, `if x == "" {`)
			case "bytes":
				line(1, "if len(x) == 0 {")
			default:
				s.fail(where, "check NotEmpty is for string and bytes fields, not %s", f.Type)
				continue
			}
			fail("must not be empty")
			line(1, "}")
		case "MinLen", "MaxLen":
			if f.Type != "string" && f.Type != "bytes" {
				s.fail(where, "check %s is for string and bytes fields, not %s", c.Kind, f.Type)
				continue
			}
			if len(args) != 1 {
				s.fail(where, "check %s needs one argument", c.Kind)
				continue
			}
			n, err := strconv.Atoi(args[0])
			if err != nil {
				s.fail(where, "check %s: %s is not a length", c.Kind, args[0])
				continue
			}
			if c.Kind == "MinLen" && n <= 1 && o.Unset != "" {
				continue // only "" is shorter, and "" is unset
			}
			if c.Kind == "MinLen" {
				line(1, "if len(x) < %s {", args[0])
				fail("length is less than " + args[0])
			} else {
				line(1, "if len(x) > %s {", args[0])
				fail("length is more than " + args[0])
			}
			line(1, "}")
		case "MinRuneLen", "MaxRuneLen":
			if f.Type != "string" {
				s.fail(where, "check %s is for string fields, not %s", c.Kind, f.Type)
				continue
			}
			if len(args) != 1 {
				s.fail(where, "check %s needs one argument", c.Kind)
				continue
			}
			n, err := strconv.Atoi(args[0])
			if err != nil {
				s.fail(where, "check %s: %s is not a length", c.Kind, args[0])
				continue
			}
			if c.Kind == "MinRuneLen" && n <= 1 && o.Unset != "" {
				continue // only "" has fewer characters, and "" is unset
			}
			s.useStd("unicode/utf8", where)
			if c.Kind == "MinRuneLen" {
				line(1, "if utf8.RuneCountInString(%s) < %s {", conv("string"), args[0])
				fail("length in characters is less than " + args[0])
			} else {
				line(1, "if utf8.RuneCountInString(%s) > %s {", conv("string"), args[0])
				fail("length in characters is more than " + args[0])
			}
			line(1, "}")
		case "Match":
			var pat string
			if f.Type != "string" || len(args) != 1 || json.Unmarshal(c.Args[0], &pat) != nil {
				s.fail(where, "check Match is for string fields, with a pattern")
				continue
			}
			s.useStd("regexp", where)
			s.setup = append(s.setup, fmt.Sprintf("re%d := regexp.MustCompile(%s)", s.regexps, strconv.Quote(pat)))
			line(1, "if !re%d.MatchString(%s) {", s.regexps, conv("string"))
			s.regexps++
			fail("does not match " + pat)
			line(1, "}")
		case "Range", "Min", "Max", "Positive", "Negative", "NonNegative":
			if !numeric[f.Type] {
				s.fail(where, "check %s is for number fields, not %s", c.Kind, f.Type)
				continue
			}
			s.numberCheck(c.Kind, num, line, fail)
		default:
			s.fail(where, "unknown check %s", c.Kind)
		}
	}
	for j := range f.Validators {
		v := base(f)
		if v == "" {
			s.fail(where, "Validate is not allowed on a field of type %s", f.Type)
			break
		}
		var ref *loadjson.FuncRef
		if j < len(f.ValidatorRefs) {
			ref = &f.ValidatorRefs[j]
		}
		fn := s.schemaFunc(e, f, ref, fmt.Sprintf("Validators[%d]", j), fmt.Sprintf("func(%s) error", v), where)
		line(1, "if err := %s(%s); err != nil {", fn, conv(v))
		line(2, "return err")
		line(1, "}")
	}
	if b.Len() == header {
		return // every check was one that cannot fail on a value that is set
	}
	s.cases = append(s.cases, b.String())
}

// numberCheck writes the comparison of a number check.
func (s *entState) numberCheck(kind string, arg func(int) (string, string, bool), line func(int, string, ...any), fail func(string)) {
	cmpLine := func(op, lit, msg string) {
		line(1, "if x %s %s {", op, lit)
		fail(msg)
		line(1, "}")
	}
	switch kind {
	case "Range":
		lo, lt, ok1 := arg(0)
		hi, ht, ok2 := arg(1)
		if ok1 && ok2 {
			cmpLine("<", lo, "is less than "+lt)
			cmpLine(">", hi, "is more than "+ht)
		}
	case "Min":
		if lit, text, ok := arg(0); ok {
			cmpLine("<", lit, "is less than "+text)
		}
	case "Max":
		if lit, text, ok := arg(0); ok {
			cmpLine(">", lit, "is more than "+text)
		}
	case "Positive":
		cmpLine("<=", "0", "is not positive")
	case "Negative":
		cmpLine(">=", "0", "is not negative")
	case "NonNegative":
		cmpLine("<", "0", "is negative")
	}
}

// useStd records that the code uses the standard package p.
func (s *entState) useStd(p, where string) {
	s.use(p, p, where)
}

// relations lowers the edges of every entity, and makes the join entities of
// the many-to-many ones.
func (l *lowerer) relations() {
	for _, e := range l.g.Entities {
		s := l.state[e.Name]
		for _, ed := range e.Edges {
			where := edgeAt(ed.Name)
			if reserved[ed.GoName] {
				s.fail(where, "its Go name %s is reserved: it is a method of the entity", ed.GoName)
			}
			t := l.g.Entity(ed.Target)
			if t == nil {
				s.fail(where, "type %s is not a schema of this package", ed.Target)
				continue
			}
			r := &OutRel{GoName: ed.GoName, Name: ed.Name, Target: ed.Target, Many: ed.Rel.Kind == "O2M" || ed.Rel.Kind == "M2M", Comment: ed.Comment}
			if ed.Rel.Kind == "M2M" {
				r.Desc = l.through(s, e, t, ed)
			} else {
				ownerCol, targetCol := 0, 0
				if ownFK(ed) {
					ownerCol = fieldIndex(e, ed.Field)
				} else {
					targetCol = fieldIndex(t, ed.Field)
				}
				if ownerCol < 0 || targetCol < 0 {
					s.fail(where, "the foreign key field %q is not a field", ed.Field)
					continue
				}
				owner, target := colConst(s.out, ownerCol), colConst(l.state[t.Name].out, targetCol)
				r.Desc = fmt.Sprintf("velox.EdgeTo[%s](%q, %s, %s)", t.Name, ed.Name, owner, target)
				if ed.Rel.Kind == "O2O" && ed.Bidi {
					// Both rows hold the key: the core writes the partner's.
					r.Desc = fmt.Sprintf("velox.EdgeBidi[%s](%q, %s, %s)", t.Name, ed.Name, owner, target)
				}
			}
			if slices.Contains(l.cfg.Linked[e.Name], ed.Name) {
				// The entity's behaviour names the relation with velox.HasEdge.
				r.Desc = fmt.Sprintf("velox.Linked[%s](%s)", t.Name, r.Desc)
			}
			s.out.Rels = append(s.out.Rels, r)
		}
	}
}

// through writes the expression of the many-to-many edge ed of e, making its
// join entity when no edge has yet.
func (l *lowerer) through(s *entState, e, t *Entity, ed *Edge) string {
	where := edgeAt(ed.Name)
	// The join entity's columns are the assoc edge's owner's, then its
	// target's. An inverse edge lists them the other way round.
	owner, target, cols := e, t, ed.Rel.Columns
	if ed.Inverse {
		owner, target, cols = t, e, []string{ed.Rel.Columns[1], ed.Rel.Columns[0]}
	}
	j := l.joins[ed.Rel.Table]
	if j == nil {
		// The join table's name is already a plural, so the entity is
		// named by its singular and the handles by the table itself:
		// category_children gives CategoryChild and CategoryChildren,
		// user_data UserDatum and UserData. A table whose singular is
		// itself, such as user_group, takes GoPlural's handles.
		name := plugin.GoName(plugin.Singular(ed.Rel.Table))
		handles := plugin.GoName(ed.Rel.Table)
		if handles == name {
			handles = plugin.GoPlural(name)
		}
		if !ident(name) {
			s.fail(where, "join table %s has no Go name for its entity", ed.Rel.Table)
			name = "Join"
		}
		js := &entState{l: l, name: name, paths: map[string]string{}}
		if owner.TableSchema != target.TableSchema {
			s.fail(where, "join table %s lies between tables in different schemas (%q and %q)", ed.Rel.Table, owner.TableSchema, target.TableSchema)
		}
		js.out = &OutEntity{
			TableSchema: owner.TableSchema, Name: name, Label: plugin.Snake(name), Handles: handles, Table: ed.Rel.Table,
			PrimaryKey: []int{0, 1}, Join: true, Bidi: ed.Bidi, Bound: l.cfg.Bound[name],
		}
		for i, ent := range []*Entity{owner, target} {
			id := ent.Fields[0]
			f := &Field{Name: cols[i], Type: id.Type, GoType: id.GoType, GoName: plugin.GoName(cols[i]), Column: cols[i]}
			js.out.Fields = append(js.out.Fields, js.field(nil, i, f))
		}
		// Errors name the assoc edge, whichever of the pair makes the entity first.
		j = &join{ent: js.out, st: js, owner: owner.Name, where: where}
		if ed.Inverse {
			j.where = edgeAt(ed.Ref)
		}
		l.joins[ed.Rel.Table] = j
	} else {
		for i, ent := range []*Entity{owner, target} {
			if f := j.ent.Fields[i]; f.Column != cols[i] || f.ElemType != l.state[ent.Name].out.Fields[0].ElemType {
				s.fail(where, "join table %s is also declared with other columns or ID types, by %s", ed.Rel.Table, j.owner)
				break
			}
		}
	}
	// This edge's own columns, as seen from it.
	jo, jt := slices.IndexFunc(j.ent.Fields, func(f *OutField) bool { return f.Column == ed.Rel.Columns[0] }),
		slices.IndexFunc(j.ent.Fields, func(f *OutField) bool { return f.Column == ed.Rel.Columns[1] })
	if jo < 0 || jt < 0 {
		jo, jt = 0, 1 // reported above
	}
	return fmt.Sprintf("velox.EdgeThrough[%s, %s](%q, %s, %s, %s, %s)", j.ent.Name, t.Name, ed.Name,
		colConst(s.out, 0), colConst(j.ent, jo), colConst(j.ent, jt), colConst(l.state[t.Name].out, 0))
}

// finish completes the entities' files, adds the join entities, and checks
// that the generated names are distinct.
func (l *lowerer) finish() {
	for _, e := range l.g.Entities {
		l.complete(l.state[e.Name])
	}
	joins := make([]*join, 0, len(l.joins))
	for _, j := range l.joins {
		joins = append(joins, j)
	}
	slices.SortFunc(joins, func(a, b *join) int { return cmp.Compare(a.ent.Name, b.ent.Name) })
	for _, j := range joins {
		l.complete(j.st)
		l.out.Entities = append(l.out.Entities, j.ent)
	}
	slices.SortFunc(l.enums, func(a, b *enumDecl) int { return cmp.Compare(a.enum.Type, b.enum.Type) })
	for _, d := range l.enums {
		l.out.Enums = append(l.out.Enums, d.enum)
	}

	// Every name the package declares at its top level.
	declared := map[string]string{}
	add := func(name, kind, schema, where string) bool {
		if old, ok := declared[name]; ok {
			l.errs = append(l.errs, &Error{Schema: schema, Where: where, Reason: fmt.Sprintf("%s %s collides with %s %s", kind, name, old, name)})
			return false
		}
		declared[name] = kind
		return true
	}
	for _, e := range l.g.Entities {
		add(e.Name, "schema", e.Name, "")
	}
	// The names that follow from one that collides are not reported again.
	joinOK := make([]bool, len(joins))
	for i, j := range joins {
		joinOK[i] = add(j.ent.Name, "join entity", j.owner, j.where)
	}
	enumOK := make([]bool, len(l.enums))
	for i, d := range l.enums {
		enumOK[i] = add(d.enum.Type, "enum type", d.schema, d.where)
	}
	for i, d := range l.enums {
		for _, c := range d.enum.Consts {
			if enumOK[i] {
				add(c, "enum constant", d.schema, d.where)
			}
		}
	}
	// An entity with relations declares XEdges, the struct that holds them.
	for _, e := range l.g.Entities {
		if len(l.state[e.Name].out.Rels) > 0 {
			add(e.Name+"Edges", "edges type", e.Name, "")
		}
	}
	for _, e := range l.g.Entities {
		add(plugin.GoPlural(e.Name), "handles variable", e.Name, "")
	}
	for i, j := range joins {
		if joinOK[i] {
			add(j.ent.Handles, "handles variable", j.owner, j.where)
		}
	}
	// Each handles variable has a type of its own, XHandles.
	for _, e := range l.g.Entities {
		add(e.Name+"Handles", "handles type", e.Name, "")
	}
	for i, j := range joins {
		if joinOK[i] {
			add(j.ent.Name+"Handles", "handles type", j.owner, j.where)
		}
	}
	// A join table is not an entity's table, and the generated functions of an
	// entity are named by its label, so no two entities share one.
	for _, j := range joins {
		for _, e := range l.g.Entities {
			if e.Table == j.ent.Table && e.TableSchema == j.ent.TableSchema {
				l.errs = append(l.errs, &Error{Schema: j.owner, Where: j.where, Reason: fmt.Sprintf("join table %s is also the table of schema %s", j.ent.Table, e.Name)})
			}
		}
	}
	labels := map[string]string{}
	for _, o := range l.out.Entities {
		if old, ok := labels[o.Label]; ok && old != o.Name { // one name is reported as a collision already
			schema, where := o.Name, ""
			if j := l.joins[o.Table]; o.Join && j != nil {
				schema, where = j.owner, j.where
			}
			l.errs = append(l.errs, &Error{Schema: schema, Where: where, Reason: fmt.Sprintf("%s %s has label %s, which is also %s's", kindOf(o), o.Name, o.Label, old)})
			continue
		}
		labels[o.Label] = o.Name
	}
}

func kindOf(o *OutEntity) string {
	if o.Join {
		return "join entity"
	}
	return "schema"
}

// complete writes the check code, the imports and NeedsSch of an entity.
func (l *lowerer) complete(s *entState) {
	o := s.out
	o.CheckCode = strings.TrimRight(strings.Join(s.cases, ""), "\n")
	o.CheckSetup = strings.Join(s.setup, "\n")
	for _, p := range s.paths {
		o.Imports = append(o.Imports, p)
	}
	slices.Sort(o.Imports)
	if _, ok := s.paths[s.l.vschema]; ok && s.l.vschema != "schema" {
		o.ImportAs = map[string]string{schemaPkg: s.l.vschema}
	}
	o.NeedsSch = s.sch
	l.out.NeedsSch = l.out.NeedsSch || s.sch
}
