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
		out: &Out{Package: cmp.Or(cfg.Package, "veloxgen"), SchemaPath: cmp.Or(cfg.SchemaPath, g.Package), UserDoc: cfg.UserDoc,
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
			l.out.EntityTable = true
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
		l.out.Privacy = l.out.Privacy || e.Bound.Privacy
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
// bidirectional edge, that sets Read, Write, Query or Mutation where
// Bound.PolicyConds says.
func bidiPolicyError(e *OutEntity) error {
	at := e.Bound.PolicyConds
	i := strings.LastIndexByte(at, ':')
	line, _ := strconv.Atoi(at[i+1:])
	return &FileError{Path: at[:i], Line: line, Reason: e.Name + ".Policy sets Read, Write, Query or Mutation: the join entity of a bidirectional edge allows only Insert, " +
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
	// enumChecks are the validators of the enum fields' Go types, by
	// enumCheckKey, and checkList the same in the order of their first
	// field.
	enumChecks map[string]*enumCheck
	checkList  []*enumCheck
	// vschema is the name the generated code gives velox's schema package.
	vschema string
}

// join is a join entity with the edge that made it.
type join struct {
	ent   *OutEntity
	st    *entState
	owner string // the schema of the edge
	at    loc    // the edge
}

// enumDecl is a generated enum type with the field that declares it.
type enumDecl struct {
	enum   *OutEnum
	schema string
	at     loc             // the field
	users  map[string]bool // the entities with a field of the type
}

// enumCheck is the validator of an enum field's Go type, which every Check
// of a field of the type calls, with the entities that call it.
type enumCheck struct {
	check  *OutCheck
	schema string
	at     loc // the first field
	users  map[string]bool
	paths  []string // the import paths of the Go type
}

// entState is the state of lowering one entity.
type entState struct {
	l       *lowerer
	name    string // the schema or join entity's name, for errors
	pos     string // the line of an error whose element has none: the schema's, or the edge's of a join entity
	out     *OutEntity
	paths   map[string]string // import name → path
	sch     bool              // the code reads the schema package
	regexps int
	reads   int               // values read from descriptors into setup
	descs   map[string]string // a field's name → the expression of its descriptor, fds[i]
	names   []string          // the quoted names FieldDescriptors reads, in the order of fds
	descsOf string            // the schema value FieldDescriptors reads them from
	setup   []string
	cases   []string
}

// fail records an error of the element at, at its line or else s.pos.
func (s *entState) fail(at loc, format string, args ...any) {
	s.l.errs = append(s.l.errs, &Error{Pos: cmp.Or(at.pos, s.pos), Schema: s.name, Where: at.where, Reason: fmt.Sprintf(format, args...)})
}

// use adds the import path p, which the code refers to as name. Two paths with
// one name are an error.
func (s *entState) use(p, name string, at loc) {
	if owner, ok := s.l.owned[name]; ok && owner != p {
		s.fail(at, "imports %s, but the generated code uses the name %s for %s", p, name, owner)
		return
	}
	if old, ok := s.paths[name]; ok && old != p {
		s.fail(at, "imports %s and %s both named %s", old, p, name)
		return
	}
	s.paths[name] = p
}

// entities lowers the graph's entities (not their relations yet).
func (l *lowerer) entities() {
	l.state = make(map[string]*entState, len(l.g.Entities))
	for _, e := range l.g.Entities {
		s := &entState{l: l, name: e.Name, pos: e.Pos, paths: map[string]string{}}
		s.out = &OutEntity{
			Name: e.Name, Label: e.Label, Handles: plugin.GoPlural(e.Name), Table: e.Table, TableSchema: e.TableSchema,
			Bound: l.cfg.Bound[e.Name],
		}
		if raw, ok := e.Annotations["Comment"]; ok {
			var c struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(raw, &c); err != nil {
				s.fail(loc{}, "its Comment annotation is not a comment: %v", err)
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
					s.fail(indexLoc(i, e.Indexes[i]), "column %q is not a field's", c)
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
	at := fieldLoc(f)
	o := &OutField{
		GoName: f.GoName, Column: f.Column, Name: f.Name, Comment: f.Comment,
		Sensitive: f.Sensitive, Immutable: f.Immutable, Version: f.Version, JSON: f.Type == "json", Implicit: f.Implicit != "",
	}
	if reserved[f.GoName] {
		s.fail(at, "its Go name %s is reserved: it is a method of the entity", f.GoName)
	}
	o.DBDefault = dbDefaultDialects(f.SchemaDefault)
	if e != nil && f.Nillable {
		// A Required edge whose key is NULL-able: velox refuses it nil, as
		// ent's create check does, since the database cannot.
		for _, ed := range e.Edges {
			if ed.Required && ed.Field == f.Name && ownFK(ed) {
				o.RequiredEdge = ed.Name
			}
		}
	}
	elem, kind := s.elemType(f, at)
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
			s.use(p.path, p.name, at)
		}
	}
	if f.GoType == nil && f.Type == "time" {
		s.use("time", "time", at)
	}
	if o.JSON {
		// The codec: the generated file, not the core, imports encoding/json.
		s.use("encoding/json", "json", at)
	}
	var h string
	switch {
	case o.JSON:
		// Stored as JSON: no condition or order compares it, as equality and
		// order of JSON differ by dialect, so its handle only sets it.
		h = "JSONCol"
	case f.Type == "time" || elem == "time.Time":
		h = "TimeCol"
	case f.Type == "other" && decimalField(f):
		// A decimal column holds a number, whatever its Go type (a struct,
		// or text): it sums and adds as one, and on MySQL its Add and Sub
		// cast their argument to the column's type.
		h = "NumberCol"
		o.Decimal = mysqlDecimal(f)
	case numeric[kind]:
		h = "NumberCol"
	case f.Type == "enum":
		// An enum compares by equality and orders, as ent's predicates; it
		// has no Contains or Like, which no value of it would need.
		h = "Col"
	case kind == "string":
		h = "StringCol"
	case f.Type == "other":
		// Its own SQL type (a range, a point): the database compares and
		// orders it, as veloxe's predicates did.
		h = "OtherCol"
	default:
		h = "Col"
	}
	if o.Pointer {
		h = "Null" + h
	}
	o.Handle = fmt.Sprintf("velox.%s[%s, %s]", h, s.name, elem)
	if f.Sensitive && f.StructTag != "" && reflect.StructTag(f.StructTag).Get("json") != "-" {
		s.fail(at, "a Sensitive field's StructTag must not expose it to JSON")
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
	s.defaults(e, f, o, at)
	o.DefaultDoc = s.defaultDoc(f, o)
	s.checks(e, f, o, at)
	return o
}

// defaultDoc is the sentence that says what an insert of f's zero value
// stores, when f has a default that replaces it, and "" otherwise. An
// insert fills a default into a zero field before its setters and hooks
// run, so a setter stores the zero over it (ADR 0082). A constant default
// that is itself the zero value replaces nothing a value field can hold.
// A Nillable field's sentence names SetNull, whose NULL an insert stores
// over the default (ADR 0124).
func (s *entState) defaultDoc(f *Field, o *OutField) string {
	if o.Implicit {
		return ""
	}
	h := s.out.Handles + "." + o.GoName
	var what, zero string // the default as said, and the zero a setter stores
	switch {
	case o.DefaultCode != "":
		var lit string
		lit, zero = constDefault(f, o)
		if lit == "" && !o.Pointer {
			return "" // the default is the zero value
		}
		what = "the default, " + cmp.Or(lit, o.DefaultCode)
	case f.DefaultFunc:
		what = "the value of its default function"
	case o.DefaultFuncCode != "" && f.Type == "json" && sliceLiteral(f.Default, o.ElemType) != "":
		what = "the default, " + sliceLiteral(f.Default, o.ElemType)
	case o.DefaultFuncCode != "":
		what = "the schema's default"
	default:
		return ""
	}
	if o.Pointer {
		return fmt.Sprintf("An insert of nil stores %s; %s.SetNull() in the insert stores NULL.", what, h)
	}
	if f.Nillable {
		return fmt.Sprintf("An insert of the zero value stores %s; %s.SetNull() in the insert stores NULL.", what, h)
	}
	if zero == "" {
		return fmt.Sprintf("An insert of the zero value stores %s.", what)
	}
	return fmt.Sprintf("An insert of the zero value stores %s; %s.Set(%s) in the insert stores %[3]s.", what, h, zero)
}

// constDefault is f's constant default as a doc comment says it, "" when it
// is the zero value of f's type, and that zero as a setter's argument, ""
// when the zero is no value of the field (an enum's).
func constDefault(f *Field, o *OutField) (lit, zero string) {
	raw := strings.TrimSpace(string(f.Default))
	switch {
	case f.Type == "bool":
		if raw == "false" {
			return "", "false"
		}
		return raw, "false"
	case numeric[f.Type]:
		if v, err := strconv.ParseFloat(raw, 64); err == nil && v == 0 {
			return "", "0"
		}
		return raw, "0"
	case f.Type == "string":
		var v string
		_ = json.Unmarshal(f.Default, &v)
		if v == "" {
			return "", `""`
		}
		return strconv.Quote(v), `""`
	}
	return o.DefaultCode, ""
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
func (s *entState) elemType(f *Field, at loc) (elem, kind string) {
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
		// Two fields whose enum types get one name and hold the same
		// values, in any order, share the type the first declares, as
		// veloxe did; with other values, checkDeclared reports the clash.
		for _, d := range s.l.enums {
			if d.enum.Type == t && sameEnum(d.enum, en) {
				d.users[s.name] = true
				return t, "string"
			}
		}
		s.l.enums = append(s.l.enums, &enumDecl{enum: en, schema: s.name, at: at, users: map[string]bool{s.name: true}})
		return t, "string"
	}
	s.fail(at, "a field of type %s needs a Go type", f.Type)
	return "any", "interface"
}

// sameEnum reports whether a and b have the same constants with the same
// values, in any order.
func sameEnum(a, b *OutEnum) bool {
	if len(a.Consts) != len(b.Consts) {
		return false
	}
	pairs := make(map[[2]string]bool, len(a.Consts))
	for i, c := range a.Consts {
		pairs[[2]string{c, a.Values[i]}] = true
	}
	for i, c := range b.Consts {
		if !pairs[[2]string{c, b.Values[i]}] {
			return false
		}
	}
	return true
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
// FieldDescriptors reads fields' descriptors by their names.
const schemaPkg = "github.com/syssam/velox/schema"

// descriptor returns the expression of f's descriptor in the setup block,
// in plain mode: fds[i], an element of the one FieldDescriptors call that
// complete puts first in the block, which reads the schema's Mixin and
// Fields once for every descriptor the entity needs.
func (s *entState) descriptor(e *Entity, f *Field) string {
	if v, ok := s.descs[f.Name]; ok {
		return v
	}
	s.sch = true
	if s.descs == nil {
		s.descs = map[string]string{}
	}
	v := "fds[" + strconv.Itoa(len(s.names)) + "]"
	s.descs[f.Name] = v
	s.names = append(s.names, strconv.Quote(f.Name))
	s.descsOf = fmt.Sprintf("&%s.%s{}", s.l.out.SchemaName, e.Name)
	return v
}

// schemaFunc returns the expression, of the type typ, of a function of f
// that the schema holds: its descriptor's member (DefaultFunc, Validators[0]).
// ref names the function. It is the function itself, pkg.Name, when ref
// names one the generated package can import, of the type typ; else a
// variable of the setup block that reads the descriptor once.
func (s *entState) schemaFunc(e *Entity, f *Field, ref *loadjson.FuncRef, member, typ string, at loc) string {
	if ref != nil && ref.Pkg != "" && ref.Type == typ {
		if p, ok := s.l.cfg.Funcs[ref.Pkg]; ok && p.Gen {
			name := p.Name
			if ref.Pkg == s.l.out.SchemaPath {
				name, s.sch = s.l.out.SchemaName, true
			}
			if s.free(ref.Pkg, name) {
				if ref.Pkg != s.l.out.SchemaPath {
					s.use(ref.Pkg, name, at)
				}
				return name + "." + ref.Name
			}
		}
	}
	s.use(schemaPkg, s.l.vschema, at)
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
func (s *entState) defaults(e *Entity, f *Field, o *OutField, at loc) {
	elem := o.ElemType
	// call returns the column's closure, which calls the function member.
	call := func(member string, ref *loadjson.FuncRef) string {
		if f.Type == "time" && elem != "time.Time" {
			// A time default is a func() time.Time, which the builder let
			// through only for a GoType that converts from time.Time.
			s.use("time", "time", at)
			d := s.schemaFunc(e, f, ref, member, "func() time.Time", at)
			return fmt.Sprintf("func() any { return %s(%s()) }", elem, d)
		}
		d := s.schemaFunc(e, f, ref, member, "func() "+elem, at)
		return fmt.Sprintf("func() any { return %s() }", d)
	}
	if len(f.Default) > 0 {
		lit, ok := s.literal(f, elem, at)
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
			s.use("encoding/json", "json", at)
			o.DefaultFuncCode = fmt.Sprintf("func() any { var v %s; _ = json.Unmarshal([]byte(%s), &v); return v }", elem, strconv.Quote(compact(f.Default)))
		case !f.DefaultFunc: // else DefaultFunc below replaces it, and nothing reads this
			d := s.schemaValue(e, f, "Default", at)
			o.DefaultFuncCode = fmt.Sprintf("func() any { return %s }", d)
		}
	}
	if f.DefaultFunc {
		if f.Type == "time" || f.Type == "uuid" {
			o.DefaultFuncCode = call("Default", f.DefaultRef) // their Default takes a func
		} else {
			o.DefaultFuncCode = call("DefaultFunc", f.DefaultRef)
		}
	}
	if f.UpdateDefault {
		o.UpdateDefaultCode = call("UpdateDefault", f.UpdateDefaultRef)
	}
	if f.Type == "json" && f.Optional && !f.Nillable && len(f.Default) == 0 && !f.DefaultFunc && f.GoType != nil {
		switch f.GoType.Kind {
		case "slice", "map":
			// Unset, an Optional JSON slice or map is written empty, [] or
			// {}, as veloxe zero-filled it (decision 0090), not NULL; it
			// reads back empty. Set to nil by an update, it is NULL.
			o.ZeroFillCode = fmt.Sprintf("func() any { return %s{} }", elem)
		case "ptr":
			// Unset, an Optional JSON pointer is written as its element's
			// zero, as veloxe, whose field held the element, zero-filled it
			// (decision 0090); it reads back a pointer to that zero. Set to
			// nil by an update, it is NULL.
			o.ZeroFillCode = fmt.Sprintf("func() any { return new(%s) }", strings.TrimPrefix(elem, "*"))
		}
	}
}

// schemaValue returns the expression of a value of f's descriptor, its
// member, of type any, that is not a function: a variable of the setup
// block.
func (s *entState) schemaValue(e *Entity, f *Field, member string, at loc) string {
	s.use(schemaPkg, s.l.vschema, at)
	fd := s.descriptor(e, f)
	v := "d" + strconv.Itoa(s.reads)
	s.reads++
	s.setup = append(s.setup, fmt.Sprintf("%s := %s.%s", v, fd, member))
	return v
}

// literal writes f's literal Default as a Go literal of the element type
// elem. It returns "" for a Default that is not a literal on a bool, number,
// string or enum, which is read from the descriptor; !ok means it is wrong.
func (s *entState) literal(f *Field, elem string, at loc) (lit string, ok bool) {
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
			s.fail(at, "its Default %s is not a bool", raw)
			return "", false
		}
		return conv(strconv.FormatBool(v)), true
	case numeric[f.Type]:
		if !fits(f.Type, raw) {
			s.fail(at, "its Default %s is not a %s", raw, f.Type)
			return "", false
		}
		return elem + "(" + raw + ")", true
	case f.Type == "string", f.Type == "enum":
		var v string
		if json.Unmarshal(f.Default, &v) != nil {
			s.fail(at, "its Default %s is not a string", raw)
			return "", false
		}
		if f.Type == "enum" && f.GoType == nil {
			for _, en := range f.Enums {
				if en.Value == v {
					return elem + plugin.GoName(en.Name), true
				}
			}
			s.fail(at, "its Default %s is not one of the enum values", raw)
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

// checkWriter writes one field's case of Check's switch.
type checkWriter struct {
	s  *entState
	f  *Field
	o  *OutField
	at loc
	b  strings.Builder
}

// line writes one line of the case, indented by depth tabs.
func (w *checkWriter) line(depth int, format string, args ...any) {
	w.b.WriteString(strings.Repeat("\t", depth))
	fmt.Fprintf(&w.b, format, args...)
	w.b.WriteByte('\n')
}

// fail writes the return of a check that fails with msg.
func (w *checkWriter) fail(msg string) {
	w.s.useStd("errors", w.at)
	w.line(2, "return errors.New(%s)", strconv.Quote(msg))
}

// conv returns x as the type t, when its type is another.
func (w *checkWriter) conv(t string) string {
	if w.o.ElemType == t {
		return "x"
	}
	return t + "(x)"
}

// checks writes the case of Check's switch for f's column, when f has
// checks, an enum or validators.
func (s *entState) checks(e *Entity, f *Field, o *OutField, at loc) {
	if len(f.Checks) == 0 && f.Type != "enum" && f.Validators == 0 {
		return
	}
	w := &checkWriter{s: s, f: f, o: o, at: at}
	w.line(0, "case %sCol%s:", camel(s.out.Label), o.GoName)
	w.line(1, "x := v.(%s)", o.ElemType)
	if o.Unset != "" {
		// Optional: the zero value means unset, and unset is valid.
		w.line(1, "if %s {", o.Unset)
		w.line(2, "return nil")
		w.line(1, "}")
	}
	header := w.b.Len() // what is written past here is a check that can fail
	if f.Type == "enum" {
		w.enum()
	}
	for _, c := range f.Checks {
		w.check(c)
	}
	w.validators(e)
	if w.b.Len() == header {
		return // every check was one that cannot fail on a value that is set
	}
	s.cases = append(s.cases, w.b.String())
}

// enum writes the check that an enum's value is one of its values.
func (w *checkWriter) enum() {
	f := w.f
	vals := make([]string, len(f.Enums))
	plain := make([]string, len(f.Enums))
	for i, en := range f.Enums {
		vals[i] = strconv.Quote(en.Value)
		plain[i] = en.Value
	}
	msg := strconv.Quote("is not one of " + strings.Join(plain, ", "))
	if f.GoType == nil {
		// The enum type is generated, and has IsValid.
		w.line(1, "if !x.IsValid() {")
		w.line(2, "return errors.New(%s)", msg)
		w.line(1, "}")
		w.s.useStd("errors", w.at)
		return
	}
	// The Go type is the application's: one validator per type and values,
	// which every Check of a field of it calls.
	c := w.s.l.enumCheck(w.s, f, vals, msg, w.at)
	w.line(1, "if err := %s(x); err != nil {", c.Func)
	w.line(2, "return err")
	w.line(1, "}")
}

// enumCheckKey identifies the validator of the Go type ident with the
// quoted values vals.
func enumCheckKey(ident string, vals []string) string {
	return ident + "\x00" + strings.Join(vals, "\x00")
}

// enumCheck returns the validator of f's Go type with the quoted values
// vals, which fails with the quoted message msg, making it on the first
// field of the type and values. It is named veloxCheck and the type's name,
// with its package's name too when another type of that name has one.
func (l *lowerer) enumCheck(s *entState, f *Field, vals []string, msg string, at loc) *OutCheck {
	key := enumCheckKey(f.GoType.Ident, vals)
	if c, ok := l.enumChecks[key]; ok {
		c.users[s.name] = true
		return c.check
	}
	if l.enumChecks == nil {
		l.enumChecks = map[string]*enumCheck{}
	}
	qual, name, ok := strings.Cut(f.GoType.Ident, ".")
	if !ok {
		qual, name = "", qual
	}
	fn := "veloxCheck" + plugin.GoName(name)
	taken := func(n string) bool {
		return slices.ContainsFunc(l.checkList, func(c *enumCheck) bool { return c.check.Func == n })
	}
	if taken(fn) {
		fn = "veloxCheck" + plugin.GoName(qual) + plugin.GoName(name)
	}
	for i, base := 2, fn; taken(fn); i++ {
		fn = base + strconv.Itoa(i)
	}
	c := &enumCheck{
		check:  &OutCheck{Func: fn, Type: f.GoType.Ident, Values: vals, Msg: msg},
		schema: s.name, at: at, users: map[string]bool{s.name: true},
	}
	for _, p := range importNames(f.GoType) {
		c.paths = append(c.paths, p.path)
	}
	l.enumChecks[key] = c
	l.checkList = append(l.checkList, c)
	return c.check
}

// check writes the comparison of the check c, or reports why it cannot.
func (w *checkWriter) check(c loadjson.Check) {
	s, f, at := w.s, w.f, w.at
	args := make([]string, len(c.Args))
	for i, a := range c.Args {
		args[i] = strings.TrimSpace(string(a))
	}
	switch c.Kind {
	case "NotEmpty":
		w.notEmpty()
	case "MinLen", "MaxLen":
		w.byteLen(c.Kind, args)
	case "MinRuneLen", "MaxRuneLen":
		w.runeLen(c.Kind, args)
	case "Match":
		w.match(c, args)
	case "Range", "Min", "Max", "Positive", "Negative", "NonNegative":
		if !numeric[f.Type] {
			s.fail(at, "check %s is for number fields, not %s", c.Kind, f.Type)
			return
		}
		num := func(i int) (lit, text string, ok bool) { return w.num(c.Kind, args, i) }
		s.numberCheck(c.Kind, num, w.line, w.fail)
	default:
		s.fail(at, "unknown check %s", c.Kind)
	}
}

// num returns the argument i of the check kind as a literal of the element
// type, and as written; it reports a missing or ill-typed argument.
func (w *checkWriter) num(kind string, args []string, i int) (lit, text string, ok bool) {
	if i >= len(args) {
		w.s.fail(w.at, "check %s needs an argument", kind)
		return "", "", false
	}
	if !fits(w.f.Type, args[i]) {
		w.s.fail(w.at, "check %s: %s is not a %s", kind, args[i], w.f.Type)
		return "", "", false
	}
	if w.o.ElemType == w.f.Type {
		return args[i], args[i], true // an untyped constant is already of the type
	}
	return w.o.ElemType + "(" + args[i] + ")", args[i], true
}

// notEmpty writes the NotEmpty check.
func (w *checkWriter) notEmpty() {
	if w.o.Unset != "" {
		return // "" is unset and passes: the check could never fail
	}
	switch w.f.Type {
	case "string":
		w.line(1, `if x == "" {`)
	case "bytes":
		w.line(1, "if len(x) == 0 {")
	default:
		w.s.fail(w.at, "check NotEmpty is for string and bytes fields, not %s", w.f.Type)
		return
	}
	w.fail("must not be empty")
	w.line(1, "}")
}

// length returns the one argument of the length check kind, and whether it
// is one; it reports why it is not.
func (w *checkWriter) length(kind string, args []string) (int, bool) {
	if len(args) != 1 {
		w.s.fail(w.at, "check %s needs one argument", kind)
		return 0, false
	}
	n, err := strconv.Atoi(args[0])
	if err != nil {
		w.s.fail(w.at, "check %s: %s is not a length", kind, args[0])
		return 0, false
	}
	return n, true
}

// byteLen writes the MinLen or MaxLen check.
func (w *checkWriter) byteLen(kind string, args []string) {
	if w.f.Type != "string" && w.f.Type != "bytes" {
		w.s.fail(w.at, "check %s is for string and bytes fields, not %s", kind, w.f.Type)
		return
	}
	n, ok := w.length(kind, args)
	if !ok {
		return
	}
	if kind == "MinLen" && n <= 1 && w.o.Unset != "" {
		return // only "" is shorter, and "" is unset
	}
	if kind == "MinLen" {
		w.line(1, "if len(x) < %s {", args[0])
		w.fail("length is less than " + args[0])
	} else {
		w.line(1, "if len(x) > %s {", args[0])
		w.fail("length is more than " + args[0])
	}
	w.line(1, "}")
}

// runeLen writes the MinRuneLen or MaxRuneLen check.
func (w *checkWriter) runeLen(kind string, args []string) {
	if w.f.Type != "string" {
		w.s.fail(w.at, "check %s is for string fields, not %s", kind, w.f.Type)
		return
	}
	n, ok := w.length(kind, args)
	if !ok {
		return
	}
	if kind == "MinRuneLen" && n <= 1 && w.o.Unset != "" {
		return // only "" has fewer characters, and "" is unset
	}
	w.s.useStd("unicode/utf8", w.at)
	if kind == "MinRuneLen" {
		w.line(1, "if utf8.RuneCountInString(%s) < %s {", w.conv("string"), args[0])
		w.fail("length in characters is less than " + args[0])
	} else {
		w.line(1, "if utf8.RuneCountInString(%s) > %s {", w.conv("string"), args[0])
		w.fail("length in characters is more than " + args[0])
	}
	w.line(1, "}")
}

// match writes the Match check, whose regexp is compiled once in setup.
func (w *checkWriter) match(c loadjson.Check, args []string) {
	s := w.s
	var pat string
	if w.f.Type != "string" || len(args) != 1 || json.Unmarshal(c.Args[0], &pat) != nil {
		s.fail(w.at, "check Match is for string fields, with a pattern")
		return
	}
	s.useStd("regexp", w.at)
	s.setup = append(s.setup, fmt.Sprintf("re%d := regexp.MustCompile(%s)", s.regexps, strconv.Quote(pat)))
	w.line(1, "if !re%d.MatchString(%s) {", s.regexps, w.conv("string"))
	s.regexps++
	w.fail("does not match " + pat)
	w.line(1, "}")
}

// validators writes the call of each of the field's Validate functions.
func (w *checkWriter) validators(e *Entity) {
	f := w.f
	for j := range f.Validators {
		v := base(f)
		if v == "" {
			w.s.fail(w.at, "Validate is not allowed on a field of type %s", f.Type)
			break
		}
		var ref *loadjson.FuncRef
		if j < len(f.ValidatorRefs) {
			ref = &f.ValidatorRefs[j]
		}
		fn := w.s.schemaFunc(e, f, ref, fmt.Sprintf("Validators[%d]", j), fmt.Sprintf("func(%s) error", v), w.at)
		w.line(1, "if err := %s(%s); err != nil {", fn, w.conv(v))
		w.line(2, "return err")
		w.line(1, "}")
	}
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
func (s *entState) useStd(p string, at loc) {
	s.use(p, p, at)
}

// relations lowers the edges of every entity, and makes the join entities of
// the many-to-many ones.
func (l *lowerer) relations() {
	for _, e := range l.g.Entities {
		s := l.state[e.Name]
		for _, ed := range e.Edges {
			at := edgeLoc(ed)
			if reserved[ed.GoName] {
				s.fail(at, "its Go name %s is reserved: it is a method of the entity", ed.GoName)
			}
			t := l.g.Entity(ed.Target)
			if t == nil {
				s.fail(at, "type %s is not a schema of this package", ed.Target)
				continue
			}
			r := &OutRel{GoName: ed.GoName, Name: ed.Name, Target: ed.Target, Many: ed.Rel.Kind == "O2M" || ed.Rel.Kind == "M2M", Writes: "Add", Comment: ed.Comment}
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
					s.fail(at, "the foreign key field %q is not a field", ed.Field)
					continue
				}
				switch tf := l.state[t.Name].out.Fields[targetCol]; {
				case ownFK(ed):
					r.Writes = "Set"
				case !tf.Pointer && !tf.NilIsNull:
					r.Writes = t.Name + "." + tf.GoName
				}
				owner, target := colConst(s.out, ownerCol), colConst(l.state[t.Name].out, targetCol)
				r.Desc = fmt.Sprintf("velox.EdgeTo[%s](%q, %s, %s)", t.Name, ed.Name, owner, target)
				if ed.Rel.Kind == "O2O" && ed.Bidi {
					// Both rows hold the key: the core writes the partner's.
					r.Desc = fmt.Sprintf("velox.EdgeBidi[%s](%q, %s, %s)", t.Name, ed.Name, owner, target)
				}
			}
			s.out.Rels = append(s.out.Rels, r)
		}
	}
}

// through writes the expression of the many-to-many edge ed of e, making its
// join entity when no edge has yet.
func (l *lowerer) through(s *entState, e, t *Entity, ed *Edge) string {
	if ed.Rel.EdgeSchema != "" {
		return l.edgeSchema(s, t, ed)
	}
	at := edgeLoc(ed)
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
			s.fail(at, "join table %s has no Go name for its entity", ed.Rel.Table)
			name = "Join"
		}
		js := &entState{l: l, name: name, pos: ed.Pos, paths: map[string]string{}}
		if owner.TableSchema != target.TableSchema {
			s.fail(at, "join table %s lies between tables in different schemas (%q and %q)", ed.Rel.Table, owner.TableSchema, target.TableSchema)
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
		j = &join{ent: js.out, st: js, owner: owner.Name, at: at}
		if ed.Inverse {
			j.at = loc{edgeAt(ed.Ref), ed.Pos}
			if ref := t.Edge(ed.Ref); ref != nil && ref.Pos != "" {
				j.at.pos = ref.Pos
			}
		}
		l.joins[ed.Rel.Table] = j
	} else {
		for i, ent := range []*Entity{owner, target} {
			if f := j.ent.Fields[i]; f.Column != cols[i] || f.ElemType != l.state[ent.Name].out.Fields[0].ElemType {
				s.fail(at, "join table %s is also declared with other columns or ID types, by %s", ed.Rel.Table, j.owner)
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

// edgeSchema writes the expression of the many-to-many edge ed whose links
// the edge schema named by ed.Rel.EdgeSchema holds: its entity stands where
// a join entity would, and the columns Add cannot fill follow the keys.
func (l *lowerer) edgeSchema(s *entState, t *Entity, ed *Edge) string {
	es := l.g.Entity(ed.Rel.EdgeSchema)
	jo, jt := columnIndex(es, ed.Rel.Columns[0]), columnIndex(es, ed.Rel.Columns[1])
	out := l.state[es.Name].out
	args := []string{colConst(s.out, 0), colConst(out, jo), colConst(out, jt), colConst(l.state[t.Name].out, 0)}
	for _, i := range unfilled(es, jo, jt) {
		args = append(args, colConst(out, i))
	}
	return fmt.Sprintf("velox.EdgeThrough[%s, %s](%q, %s)", es.Name, t.Name, ed.Name, strings.Join(args, ", "))
}

// unfilled are the columns of the edge schema es, other than its keys jo and
// jt, that an insert of a row Add makes cannot fill: a field that is
// required and has no default of any kind, and a key that velox neither
// generates nor fills with a DefaultFunc. An Optional field is written as
// its zero (ADR 0090); an Optional JSON field and a Nillable one have a
// default or NULL; a Version starts at its zero. A database default fills
// the column only when it is declared for every dialect.
func unfilled(es *Entity, jo, jt int) []int {
	var cols []int
	for i, f := range es.Fields {
		filled := f.Optional || f.Nillable || f.Pointer || f.Default != nil || f.DefaultFunc || f.Version || everyDialect(f.SchemaDefault)
		if i == 0 {
			filled = filled || f.AutoID || integer(f.Type)
		}
		if i != jo && i != jt && !filled {
			cols = append(cols, i)
		}
	}
	return cols
}

// finish completes the entities' files, adds the join entities, and checks
// that the generated names are distinct.
func (l *lowerer) finish() {
	l.placeChecks()
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
		if len(d.users) == 1 {
			// One entity has fields of it: its file declares it.
			d.enum.Owner = d.schema
		}
		l.out.Enums = append(l.out.Enums, d.enum)
	}
	l.checkDeclared(joins)
	l.checkJoinTables(joins)
	l.checkLabels()
}

// placeChecks gives each enum validator to the file that declares it: the
// file of the one entity whose Check calls it, which then imports errors,
// or else velox.go, which then imports errors and the Go type's packages.
func (l *lowerer) placeChecks() {
	slices.SortFunc(l.checkList, func(a, b *enumCheck) int { return cmp.Compare(a.check.Func, b.check.Func) })
	for _, c := range l.checkList {
		if len(c.users) == 1 {
			s := l.state[c.schema]
			s.out.Checks = append(s.out.Checks, c.check)
			s.useStd("errors", c.at)
			continue
		}
		l.out.Checks = append(l.out.Checks, c.check)
		l.out.Imports = append(l.out.Imports, c.paths...)
	}
	if len(l.out.Checks) > 0 {
		l.out.Imports = append(l.out.Imports, "errors")
	}
	slices.Sort(l.out.Imports)
	l.out.Imports = slices.Compact(l.out.Imports)
}

// checkDeclared reports each name the package declares at its top level
// that collides with another.
func (l *lowerer) checkDeclared(joins []*join) {
	// Every name the package declares at its top level.
	declared := map[string]string{}
	add := func(name, kind, schema string, at loc) bool {
		if old, ok := declared[name]; ok {
			l.errs = append(l.errs, &Error{Pos: at.pos, Schema: schema, Where: at.where, Reason: fmt.Sprintf("%s %s collides with %s %s", kind, name, old, name)})
			return false
		}
		declared[name] = kind
		return true
	}
	for _, e := range l.g.Entities {
		add(e.Name, "schema", e.Name, loc{pos: e.Pos})
	}
	// The names that follow from one that collides are not reported again.
	joinOK := make([]bool, len(joins))
	for i, j := range joins {
		joinOK[i] = add(j.ent.Name, "join entity", j.owner, j.at)
	}
	for _, c := range l.checkList {
		add(c.check.Func, "enum validator", c.schema, c.at)
	}
	enumOK := make([]bool, len(l.enums))
	for i, d := range l.enums {
		enumOK[i] = add(d.enum.Type, "enum type", d.schema, d.at)
	}
	for i, d := range l.enums {
		for _, c := range d.enum.Consts {
			if enumOK[i] {
				add(c, "enum constant", d.schema, d.at)
			}
		}
	}
	// An entity with relations declares XEdges, the struct that holds them,
	// and holds it in its field Edges.
	for _, e := range l.g.Entities {
		o := l.state[e.Name].out
		if len(o.Rels) == 0 {
			continue
		}
		add(e.Name+"Edges", "edges type", e.Name, loc{pos: e.Pos})
		for _, f := range e.Fields {
			if f.GoName == "Edges" {
				l.errs = append(l.errs, &Error{Pos: cmp.Or(f.Pos, e.Pos), Schema: e.Name, Where: fieldAt(f.Name), Reason: "its Go name Edges is reserved: it holds the entity's relations"})
			}
		}
	}
	for _, e := range l.g.Entities {
		add(plugin.GoPlural(e.Name), "handles variable", e.Name, loc{pos: e.Pos})
	}
	for i, j := range joins {
		if joinOK[i] {
			add(j.ent.Handles, "handles variable", j.owner, j.at)
		}
	}
	// Each handles variable has a type of its own, XHandles.
	for _, e := range l.g.Entities {
		add(e.Name+"Handles", "handles type", e.Name, loc{pos: e.Pos})
	}
	for i, j := range joins {
		if joinOK[i] {
			add(j.ent.Name+"Handles", "handles type", j.owner, j.at)
		}
	}
	// The unexported names: each entity's meta variable, descriptor
	// function and the constants of its columns and edges.
	for _, e := range l.g.Entities {
		addUnexported(add, l.state[e.Name].out, e.Name, loc{pos: e.Pos})
	}
	for i, j := range joins {
		if joinOK[i] {
			addUnexported(add, j.ent, j.owner, j.at)
		}
	}
}

// addUnexported adds the unexported names renderEntity declares for o.
func addUnexported(add func(name, kind, schema string, at loc) bool, o *OutEntity, schema string, at loc) {
	id := camel(o.Label)
	add(id+"Meta", "meta variable", schema, at)
	add(id+"Descriptor", "descriptor function", schema, at)
	for i, f := range o.Fields {
		w := at
		if w.where == "" {
			w.where = fieldAt(f.Name)
		}
		add(colConst(o, i), "column constant", schema, w)
	}
	for i, r := range o.Rels {
		w := at
		if w.where == "" {
			w.where = edgeAt(r.Name)
		}
		add(edgeConst(o, i), "edge constant", schema, w)
	}
}

// checkJoinTables reports a join table that is also an entity's table.
func (l *lowerer) checkJoinTables(joins []*join) {
	// A join table is not an entity's table.
	for _, j := range joins {
		for _, e := range l.g.Entities {
			if e.Table == j.ent.Table && e.TableSchema == j.ent.TableSchema {
				l.errs = append(l.errs, &Error{Pos: j.at.pos, Schema: j.owner, Where: j.at.where, Reason: fmt.Sprintf("join table %s is also the table of schema %s", j.ent.Table, e.Name)})
			}
		}
	}
}

// checkLabels reports two entities that share a label.
func (l *lowerer) checkLabels() {
	// The generated functions of an entity are named by its label, so no two
	// entities share one.
	labels := map[string]string{}
	for _, o := range l.out.Entities {
		if old, ok := labels[o.Label]; ok && old != o.Name { // one name is reported as a collision already
			schema, at := o.Name, loc{}
			if e := l.g.Entity(o.Name); e != nil {
				at.pos = e.Pos
			}
			if j := l.joins[o.Table]; o.Join && j != nil {
				schema, at = j.owner, j.at
			}
			l.errs = append(l.errs, &Error{Pos: at.pos, Schema: schema, Where: at.where, Reason: fmt.Sprintf("%s %s has label %s, which is also %s's", kindOf(o), o.Name, o.Label, old)})
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
	if len(s.names) > 0 {
		read := fmt.Sprintf("fds := %s.FieldDescriptors(%s, %s)", s.l.vschema, s.descsOf, strings.Join(s.names, ", "))
		s.setup = append([]string{read}, s.setup...)
	}
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

// dbDefaultDialects is the dialects a field's SchemaDefault (its own, or a
// sqlschema default lowered into it) gives a database default on, as the
// velox constants its ColumnDescriptor's DBDefault lists, in a fixed order.
func dbDefaultDialects(m map[string]string) []string {
	var out []string
	for _, d := range defaultDialects {
		if m[d.key] != "" {
			out = append(out, d.name)
		}
	}
	return out
}

// defaultDialects are the dialects a SchemaDefault may name: its key, and
// the velox constant of the dialect.
var defaultDialects = [...]struct{ key, name string }{{"postgres", "velox.Postgres"}, {"mysql", "velox.MySQL"}, {"sqlite", "velox.SQLite"}}

// everyDialect reports whether the SchemaDefault m names every dialect.
func everyDialect(m map[string]string) bool { return len(dbDefaultDialects(m)) == len(defaultDialects) }
