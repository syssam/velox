package gogen

import (
	"cmp"
	"fmt"
	"go/token"
	"go/types"
	"slices"
	"strings"

	"github.com/syssam/velox/contrib/graphql/internal/sdl"
	"github.com/syssam/velox/plugin"
)

// writeMutations writes t's mutation inputs, CreateXInput and UpdateXInput,
// each with its method Setters.
func (r *renderer) writeMutations(f *file, t *sdl.Type) error {
	for _, x := range []struct {
		verb string
		fs   []sdl.InputField
	}{{"Create", t.Create}, {"Update", t.Update}} {
		if x.fs == nil {
			continue
		}
		in := x.verb + t.Name + "Input"
		if err := r.writeInput(f, t, in, x.fs); err != nil {
			return err
		}
		if err := r.writeSetters(f, t, in, x.fs, x.verb == "Create"); err != nil {
			return err
		}
	}
	return nil
}

// inputName is a field of a mutation input that sets an entity field: the
// field's Go name, and the input field's GraphQL name.
type inputName struct{ goName, gql string }

// inputNames are the fields of the input fs that set an entity field, as a
// validator's error names them: a value by its field, and an edge's
// <edge>ID by its foreign key, when the key is on the row.
func inputNames(e *plugin.Entity, fs []sdl.InputField) []inputName {
	var out []inputName
	add := func(goName, gql string) {
		if !slices.ContainsFunc(out, func(x inputName) bool { return x.goName == goName }) {
			out = append(out, inputName{goName, gql})
		}
	}
	for _, in := range fs {
		switch {
		case in.Kind == sdl.InputValue:
			add(e.Fields[in.Field].GoName, in.Name)
		case in.Kind == sdl.InputEdgeID && e.Edges[in.Edge].OwnsKey:
			if fk := fieldOf(e, e.Edges[in.Edge].Field); fk != nil {
				add(fk.GoName, in.Name)
			}
		}
	}
	return out
}

// clearRule is what the update input fs's Setters doc says of its clear
// fields, after "A field not given sets nothing": "" when none of them
// meets another field. A clear field wins over its field, and over the ID
// of its edge; a clear field of an edge with a list of IDs clears it, then
// the IDs are added.
func clearRule(fs []sdl.InputField) string {
	var wins, before bool
	for _, fi := range fs {
		switch fi.Kind {
		case sdl.InputClear:
			wins = true
		case sdl.InputClearEdge:
			for _, o := range fs {
				switch {
				case o.Edge != fi.Edge:
				case o.Kind == sdl.InputEdgeID:
					wins = true
				case o.Kind == sdl.InputEdgeIDs || o.Kind == sdl.InputAddIDs:
					before = true
				}
			}
		}
	}
	var rules []string
	if wins {
		rules = append(rules, "a clear field wins over its field")
	}
	if before {
		rules = append(rules, "a clear field of an edge with a list of IDs clears it before they are added")
	}
	if len(rules) == 0 {
		return ""
	}
	return ", and " + strings.Join(rules, ", and ")
}

// writeInput writes the Go type of the mutation input in, whose fields are
// fs, and, when it has a sensitive field, its String and GoString, which
// leave out the sensitive fields' values.
func (r *renderer) writeInput(f *file, t *sdl.Type, in string, fs []sdl.InputField) error {
	e := t.Entity
	if err := r.declare(in, "schema "+e.Name); err != nil {
		return err
	}
	const method = "Setters"
	sensitive := slices.ContainsFunc(fs, func(fi sdl.InputField) bool { return fi.Kind == sdl.InputValue && e.Fields[fi.Field].Sensitive })
	seen := map[string]string{} // as gqlgen matches names → the Go name
	types := make([]string, len(fs))
	for i, fi := range fs {
		key := strings.ToLower(strings.ReplaceAll(fi.GoName, "_", ""))
		if prev, ok := seen[key]; ok {
			return fmt.Errorf("schema %s: the fields %s and %s of %s are one Go field to gqlgen, which ignores case and underscores; rename one with graphql.FieldName", e.Name, prev, fi.GoName, in)
		}
		seen[key] = fi.GoName
		if fi.GoName == method || sensitive && (fi.GoName == "String" || fi.GoName == "GoString") {
			return fmt.Errorf("schema %s: the field %s of %s is the Go field %s, which is the input's method; rename it with graphql.FieldName", e.Name, fi.Name, in, fi.GoName)
		}
		typ, err := r.inputType(f, t, fi)
		if err != nil {
			return err
		}
		types[i] = typ
	}
	f.p("")
	doc := fmt.Sprintf("%s is the GraphQL input %[1]s. A nil pointer or list is a field not given, and so is a null.", in)
	if slices.ContainsFunc(fs, func(fi sdl.InputField) bool { return fi.Kind == sdl.InputClear || fi.Kind == sdl.InputClearEdge }) {
		doc += " A true clear field clears its field or edge."
	}
	f.doc("", doc)
	f.p("type %s struct {", in)
	for i, fi := range fs {
		switch fi.Kind {
		case sdl.InputValue:
			if c := e.Fields[fi.Field].Comment; c != "" {
				f.doc("\t", c)
			}
		case sdl.InputClear:
			f.doc("\t", fmt.Sprintf("%s sets the field %s to NULL.", fi.GoName, e.Fields[fi.Field].Name))
		case sdl.InputEdgeID:
			f.doc("\t", fmt.Sprintf("%s is the ID of the %s the edge %s leads to.", fi.GoName, fi.Target, e.Edges[fi.Edge].Name))
		case sdl.InputEdgeIDs:
			f.doc("\t", fmt.Sprintf("%s are the IDs of the %s nodes the edge %s leads to.", fi.GoName, fi.Target, e.Edges[fi.Edge].Name))
		case sdl.InputAddIDs:
			f.doc("\t", fmt.Sprintf("%s are the IDs of %s nodes to add to the edge %s.", fi.GoName, fi.Target, e.Edges[fi.Edge].Name))
		case sdl.InputRemoveIDs:
			f.doc("\t", fmt.Sprintf("%s are the IDs of %s nodes to remove from the edge %s; an ID the edge does not lead to is ignored.", fi.GoName, fi.Target, e.Edges[fi.Edge].Name))
		case sdl.InputClearEdge:
			if e.Edges[fi.Edge].Unique {
				f.doc("\t", fmt.Sprintf("%s unlinks the edge %s: it leads to no %s afterwards.", fi.GoName, e.Edges[fi.Edge].Name, fi.Target))
			} else if len(e.Edges[fi.Edge].Unfilled) > 0 {
				f.doc("\t", fmt.Sprintf("%s removes every %s from the edge %s, before the IDs to remove.", fi.GoName, fi.Target, e.Edges[fi.Edge].Name))
			} else {
				f.doc("\t", fmt.Sprintf("%s removes every %s from the edge %s, before the IDs to remove and to add.", fi.GoName, fi.Target, e.Edges[fi.Edge].Name))
			}
		}
		f.p("\t%s %s%s", fi.GoName, types[i], r.graphqlTag(fi.GoName, fi.Name))
	}
	f.p("}")
	if !sensitive {
		return nil
	}
	f.p("")

	// String: a pointer prints as what it points to, or <nil>, and JSON as
	// its text.
	var names, args []string
	f.p("// String returns the input's fields except its sensitive ones.")
	f.p("func (in %s) String() string {", in)
	for i, fi := range fs {
		if fi.Kind == sdl.InputValue && e.Fields[fi.Field].Sensitive {
			continue
		}
		v := "in." + fi.GoName
		json := fi.Kind == sdl.InputValue && fi.GoType == "veloxgql.JSON"
		switch {
		case strings.HasPrefix(types[i], "*") && json:
			v = f.gqlgraph() + ".ShowJSON(" + v + ")"
		case strings.HasPrefix(types[i], "*"):
			v = f.gqlgraph() + ".Show(" + v + ")"
		case json:
			v = "string(" + v + ")"
		}
		names = append(names, fi.GoName+"=%v")
		args = append(args, v)
	}
	f.p("\treturn %s", sprintf(f.use("fmt", "fmt"), in, names, args))
	f.p("}")
	f.p("")
	f.p("// GoString is String, so that %%#v does not print the sensitive fields either.")
	f.p("func (in %s) GoString() string { return in.String() }", in)
	return nil
}

// jsonOf is the Go type a JSON field fd's input, a veloxgql.JSON, decodes
// into, as the file names it; "" for any other field, whose input binds its
// Go type.
func (r *renderer) jsonOf(f *file, e *plugin.Entity, fd *plugin.Field, fi sdl.InputField) (string, error) {
	if fd.Type != "json" || fi.GoType != "veloxgql.JSON" { // a list binds its Go slice
		return "", nil
	}
	elem := cmp.Or(fd.ElemType, strings.TrimPrefix(fd.GoType, "*"))
	if elem == "" {
		return "", fmt.Errorf("schema %s, field %s: the model gives no Go type for the JSON field", e.Name, fd.Name)
	}
	return r.goType(f, e, fd, elem)
}

// setters writes the body of an input's Setters: each field given becomes
// a setter appended to set.
type setters struct {
	r      *renderer
	f      *file
	t      *sdl.Type
	in     string // the input's GraphQL type, as "CreatePostInput"
	fs     []sdl.InputField
	create bool
	h      string // the entity's handles variable, as "Posts"
	done   map[int]bool
}

// writeSetters writes the method Setters of the input in, whose fields are
// fs: plain Go that turns each field given into a column's or an edge's
// setter, and ends with gqlgraph.Validate, which runs the columns' validators.
func (r *renderer) writeSetters(f *file, t *sdl.Type, in string, fs []sdl.InputField, create bool) error {
	e := t.Entity
	vx, impl := f.velox(), f.gqlgraph()
	s := &setters{r: r, f: f, t: t, in: in, fs: fs, create: create, h: cmp.Or(e.Handles, plugin.GoPlural(e.Name)), done: map[int]bool{}}

	f.p("")
	if create {
		var doc strings.Builder
		doc.WriteString("Setters returns the input's fields and edges as setters for db.Insert(x).SetInput. A field not given sets nothing: x keeps its value, or the field's Default.")
		for _, fd := range leftOut(t) {
			// A key's zero value names no row: the foreign key refuses it.
			if ed := ownedEdgeOf(e, fd.Name); ed != nil {
				fmt.Fprintf(&doc, " %s leaves out %s, the key of the edge %s: set it on x, or in a velox.Hook; a zero key names no row, and the database's foreign key refuses the insert with a velox.ConstraintError.", in, fd.GoName, ed.Name)
				continue
			}
			fmt.Fprintf(&doc, " %s leaves out %s, which an insert requires: set it on x, or in a velox.Hook, or it is stored as the zero value.", in, fd.GoName)
		}
		f.doc("", doc.String())
	} else {
		f.doc("", fmt.Sprintf("Setters returns the input's fields and edges as setters for db.Update[%s]().SetInput. A field not given sets nothing%s.", e.Name, clearRule(fs)))
	}
	f.p("//")
	f.doc("", "It does no I/O. A value a validator refuses, or a list of more than gqlgraph.MaxIDs IDs, is BAD_USER_INPUT naming the field. See \"Mutations\" in the documentation of package github.com/syssam/velox/contrib/graphql.")
	f.p("func (in %s) Setters() ([]%s.Setter[%s], error) {", in, vx, e.Name)
	f.p("\tvar set []%s.Setter[%s]", vx, e.Name)
	for i, fi := range fs {
		if s.done[i] {
			continue
		}
		var err error
		switch fi.Kind {
		case sdl.InputValue:
			err = s.value(i)
		case sdl.InputClear:
			fd := e.Fields[fi.Field]
			null, nerr := s.setNull(e, fd)
			if nerr != nil {
				return nerr
			}
			f.p("\tif in.%s {", fi.GoName)
			s.add("\t\t", null)
			f.p("\t}")
		default:
			err = s.edge(fi.Edge)
		}
		if err != nil {
			return err
		}
	}
	f.p("\treturn %s.Validate(%q, &%s, set)", impl, in, typeVar(t))
	f.p("}")
	return nil
}

// add writes the append of the setter x to set.
func (s *setters) add(indent, x string) { s.f.p("%sset = append(set, %s)", indent, x) }

// fail writes the return of err.
func (s *setters) fail(indent string) {
	s.f.p("%sif err != nil {", indent)
	s.f.p("%s\treturn nil, err", indent)
	s.f.p("%s}", indent)
}

// at is the input field fi as errors name it, quoted:
// "CreatePostInput.authorID".
func (s *setters) at(fi sdl.InputField) string { return fmt.Sprintf("%q", s.in+"."+fi.Name) }

// local is the name of a variable that holds the input field fi decoded:
// its name, unless that is a keyword, a predeclared name, a name the body
// uses or a package the file imports.
func (s *setters) local(fi sdl.InputField) string {
	n := camel(fi.GoName)
	switch {
	case token.IsKeyword(n), types.Universe.Lookup(n) != nil, n == "set", n == "in", n == "err", n == "v":
		return n + "Val"
	}
	for _, name := range s.f.imports {
		if name == n {
			return n + "Val"
		}
	}
	return n
}

// open writes the start of the block of the input field fi, which runs
// when the field is given: none for a Required one. It returns the block's
// indent, the field's value there, and the block's end.
func (s *setters) open(fi sdl.InputField) (indent, src string, end func()) {
	if fi.Required {
		return "\t", "in." + fi.GoName, func() {}
	}
	s.f.p("\tif v := in.%s; v != nil {", fi.GoName)
	return "\t\t", s.deref(fi), func() { s.f.p("\t}") }
}

// deref is the value of the optional input field fi in v, given: *v, or v
// for a slice (renderer.sliceValue).
func (s *setters) deref(fi sdl.InputField) string {
	if s.r.sliceValue(fi) {
		return "v"
	}
	return "*v"
}

// pair is the index in s.fs of the field of kind that clears what fs[i]
// sets, on update; -1 for none. It marks it done.
func (s *setters) pair(i int, kind sdl.InputKind) int {
	if s.create {
		return -1
	}
	fi := s.fs[i]
	j := slices.IndexFunc(s.fs, func(x sdl.InputField) bool {
		if x.Kind != kind {
			return false
		}
		if kind == sdl.InputClear {
			return x.Field == fi.Field
		}
		return x.Edge == fi.Edge
	})
	if j >= 0 {
		s.done[j] = true
	}
	return j
}

// value writes the setter of the input field s.fs[i], a field's value, and
// of its clear field on update.
func (s *setters) value(i int) error {
	f, e, fi := s.f, s.t.Entity, s.fs[i]
	fd := e.Fields[fi.Field]
	var indent, src string
	var end func()
	if c := s.pair(i, sdl.InputClear); c >= 0 {
		null, err := s.setNull(e, fd)
		if err != nil {
			return err
		}
		f.p("\tif in.%s {", s.fs[c].GoName)
		s.add("\t\t", null)
		f.p("\t} else if v := in.%s; v != nil {", fi.GoName)
		indent, src, end = "\t\t", s.deref(fi), func() { f.p("\t}") }
	} else {
		indent, src, end = s.open(fi)
	}
	col := s.h + "." + fd.GoName
	jt, err := s.r.jsonOf(f, e, fd, fi)
	if err != nil {
		return err
	}
	if jt != "" {
		v := s.local(fi)
		f.p("%s%s, err := %s.FromJSON[%s](%s, %s)", indent, v, f.gqlgraph(), jt, s.at(fi), src)
		s.fail(indent)
		s.add(indent, col+".Set("+v+")")
		end()
		return nil
	}
	conv, err := s.r.valueConv(f, e, fd, fi)
	if err != nil {
		return err
	}
	v := src
	if conv != "" {
		v = conv + "(" + src + ")"
	}
	s.add(indent, col+".Set("+v+")")
	end()
	return nil
}

// setNull is the setter that clears the nullable field fd of e: SetNull of
// a pointer field's handle, or Set(nil) of one whose Go type holds nil (a
// slice, a map, a pointer or an interface), whose ColumnDescriptor says
// Nullable, so the core binds the nil as SQL NULL. A field of another Go
// type cannot be cleared: an error, which velox-gen's rule 48 keeps from
// its models.
func (s *setters) setNull(e *plugin.Entity, fd *plugin.Field) (string, error) {
	col := s.h + "." + fd.GoName
	switch {
	case fd.Pointer:
		return col + ".SetNull()", nil
	case fd.Kind == "slice" || fd.Kind == "map" || fd.Kind == "ptr" || fd.Kind == "interface" || fd.Type == "bytes":
		return col + ".Set(nil)", nil
	}
	return "", fmt.Errorf("schema %s, field %s: the model says its column holds NULL, and its Go type %s holds no nil; make the field Nillable", e.Name, fd.Name, cmp.Or(fd.ElemType, fd.GoType))
}

// edge writes the setters of every input field of the edge ei: of the key
// on the row for an edge that owns it, else the edge's Clear, Remove and
// Add, in that order, which is the order the write runs them in.
func (s *setters) edge(ei int) error {
	e := s.t.Entity
	ed := e.Edges[ei]
	tt := s.r.typeOf[ed.Target]
	if tt == nil {
		return fmt.Errorf("schema %s, edge %s: its target %s has no GraphQL type, which is a bug", e.Name, ed.Name, ed.Target)
	}
	k, err := s.r.idKeyOf(s.f, e, tt)
	if err != nil {
		return err
	}
	es := &edgeSetters{setters: s, ed: ed, tt: tt, k: k, field: map[sdl.InputKind]int{}}
	for i, fi := range s.fs {
		if fi.Edge == ei && fi.Kind != sdl.InputValue && fi.Kind != sdl.InputClear {
			es.field[fi.Kind] = i
			s.done[i] = true
		}
	}
	if ed.OwnsKey && ed.Kind != plugin.M2M {
		return es.ownedKey()
	}
	return es.relation()
}

// edgeSetters writes the setters of one edge's input fields.
type edgeSetters struct {
	*setters
	ed    *plugin.Edge
	tt    *sdl.Type             // the target's type
	k     idKey                 // the Go type of the target's IDs
	field map[sdl.InputKind]int // the index in fs of the edge's input of each kind
}

// idx returns the index in fs of the edge's input of the given kind, or -1
// for none.
func (es *edgeSetters) idx(kind sdl.InputKind) int {
	if i, ok := es.field[kind]; ok {
		return i
	}
	return -1
}

// one writes the input field i, an <edge>ID, cleared first by the field clr
// with the setter null when clr is not -1; set writes its setter of the
// key, which gqlgen decoded.
func (es *edgeSetters) one(i, clr int, null string, set func(indent, key string)) {
	f, fi := es.f, es.fs[i]
	var indent, src string
	var end func()
	if clr >= 0 {
		f.p("\tif in.%s {", es.fs[clr].GoName)
		es.add("\t\t", null)
		f.p("\t} else if v := in.%s; v != nil {", fi.GoName)
		indent, src, end = "\t\t", "*v", func() { f.p("\t}") }
	} else {
		indent, src, end = es.open(fi)
	}
	set(indent, src)
	end()
}

// clearOnly writes the clear field clr, with the setter null, of an edge
// that has no <edge>ID input.
func (es *edgeSetters) clearOnly(clr int, null string) {
	es.f.p("\tif in.%s {", es.fs[clr].GoName)
	es.add("\t\t", null)
	es.f.p("\t}")
}

// ownedKey writes the setters of an edge whose key is on the row.
//
// The key is on the row: the edge's Set, which checks the key under the
// target's Read policy and sets the column; the column's SetNull clears
// it.
func (es *edgeSetters) ownedKey() error {
	e, ed := es.t.Entity, es.ed
	fk, err := fkOf(e, ed, e)
	if err != nil {
		return err
	}
	clr := es.idx(sdl.InputClearEdge)
	var null string
	if clr >= 0 {
		if null, err = es.setNull(e, fk); err != nil {
			return err
		}
	}
	// Set converts the key to the column's Go type, which must take it.
	if !sameGoType(fk, es.k.fd) {
		if err := es.r.converts(e, fk, es.tt.Key); err != nil {
			return err
		}
	}
	if i := es.idx(sdl.InputEdgeID); i >= 0 {
		es.one(i, clr, null, func(indent, key string) {
			es.add(indent, es.h+"."+ed.GoName+".Set("+key+")")
		})
	} else if clr >= 0 {
		es.clearOnly(clr, null)
	}
	return nil
}

// relation writes the setters of a many-to-many edge's join rows, or of
// the target's rows that hold the key: the edge's setters, with the row's
// key.
func (es *edgeSetters) relation() error {
	if err := es.checkRelationKey(); err != nil {
		return err
	}
	f := es.f
	rel := es.h + "." + es.ed.GoName
	clr := es.idx(sdl.InputClearEdge)
	if i := es.idx(sdl.InputEdgeID); i >= 0 {
		es.one(i, clr, rel+".Clear()", func(indent, key string) { es.add(indent, rel+".Add("+key+")") })
		return nil
	}
	if clr >= 0 {
		es.clearOnly(clr, rel+".Clear()")
	}
	for _, x := range []struct {
		kind sdl.InputKind
		op   string
	}{{sdl.InputRemoveIDs, "Remove"}, {sdl.InputAddIDs, "Add"}, {sdl.InputEdgeIDs, "Add"}} {
		i := es.idx(x.kind)
		if i < 0 {
			continue
		}
		fi := es.fs[i]
		v := es.local(fi)
		f.p("\tif len(in.%s) > 0 {", fi.GoName)
		f.p("\t\t%s, err := %s.IDs(%s, in.%s)", v, f.gqlgraph(), es.at(fi), fi.GoName)
		es.fail("\t\t")
		es.add("\t\t", rel+"."+x.op+"("+v+"...)")
		f.p("\t}")
	}
	return nil
}

// checkRelationKey refuses a relation edge that velox-graphql cannot write:
// the row's key is more than one column, or the key on the target's rows
// is never NULL or cannot take the row's key.
func (es *edgeSetters) checkRelationKey() error {
	e, ed, tt := es.t.Entity, es.ed, es.tt
	if len(e.PrimaryKey) != 1 {
		return fmt.Errorf("schema %s, edge %s: velox-graphql writes it by the key of %s, which is %d columns; skip its inputs with graphql.Skip(graphql.SkipMutationCreateInput, graphql.SkipMutationUpdateInput)", e.Name, ed.Name, e.Name, len(e.PrimaryKey))
	}
	if ed.Kind == plugin.M2M {
		return nil
	}
	// sdl gives a key that is never NULL no input that re-points it: it
	// is an owner's on every row.
	tfk, err := fkOf(e, ed, tt.Entity)
	if err != nil {
		return err
	}
	if !tfk.Nullable {
		return fmt.Errorf("schema %s, edge %s: an input re-points %s's rows, whose key %s is never NULL, which is a bug", e.Name, ed.Name, tt.Entity.Name, tfk.Name)
	}
	// The write sets the row's key on the target's rows, converted.
	if ok := e.Fields[e.PrimaryKey[0]]; !sameGoType(tfk, ok) {
		return es.r.converts(tt.Entity, tfk, cmp.Or(es.t.Key, tfk.Type))
	}
	return nil
}
