package gen

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/syssam/velox2/internal/gen/loadjson"
	"github.com/syssam/velox2/plugin"
)

// Build resolves m into a Graph and checks every rule of the model. It returns
// all the rules m breaks, as *Error values joined with errors.Join, ordered by
// schema name and then by rule.
//
// Build does not change m. The Graph shares m's annotations, checks and other
// reference values.
func Build(m *loadjson.Model) (*Graph, error) {
	b := &builder{pkg: m.Package, g: &Graph{Package: m.Package, Entities: []*Entity{}}}
	b.entities(m)
	b.resolveEdges()
	b.foreignKeys()
	b.indexes()
	b.validate()
	if len(b.errs) > 0 {
		slices.SortStableFunc(b.errs, func(x, y ruleError) int {
			return cmp.Or(strings.Compare(x.err.Schema, y.err.Schema), cmp.Compare(x.rule, y.rule))
		})
		errs := make([]error, len(b.errs))
		for i, e := range b.errs {
			errs[i] = e.err
		}
		return nil, errors.Join(errs...)
	}
	return b.g, nil
}

// builder holds the state of one Build.
type builder struct {
	pkg   string
	g     *Graph
	nodes []*node // in the order of g.Entities
	errs  []ruleError
}

// ruleError is an Error with the number of the rule it reports, which orders
// the errors of one schema.
type ruleError struct {
	rule int
	err  *Error
}

// node is an entity with the model it came from.
type node struct {
	ent    *Entity
	schema *loadjson.Schema
	edges  []*edgeState // in the order of ent.Edges
}

// edgeState is an edge with what resolving it found.
type edgeState struct {
	ed     *Edge
	le     *loadjson.Edge
	owner  *node
	target *node      // nil when the target is not a schema of the package
	pair   *edgeState // the other edge of an inverse pair
	column string     // the foreign-key column a storage key set, on a non-M2M relation
}

// fail records that schema s breaks rule, at the element at.
func (b *builder) fail(rule int, s string, at loc, format string, args ...any) {
	b.errs = append(b.errs, ruleError{rule, &Error{Pos: at.pos, Schema: s, Where: at.where, Reason: fmt.Sprintf(format, args...)}})
}

func fieldAt(name string) string { return fmt.Sprintf("field %q", name) }

func edgeAt(name string) string { return fmt.Sprintf("edge %q", name) }

func indexAt(i int) string { return fmt.Sprintf("index %d", i) }

// loc is an element an Error names: its Where and its position.
type loc struct{ where, pos string }

func fieldLoc(f *Field) loc { return loc{fieldAt(f.Name), f.Pos} }

func edgeLoc(ed *Edge) loc { return loc{edgeAt(ed.Name), ed.Pos} }

func indexLoc(i int, ix *Index) loc { return loc{indexAt(i), ix.Pos} }

// numeric are the field types of Go numbers.
var numeric = map[string]bool{
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"float32": true, "float64": true,
}

// integer reports whether the field type t is a Go integer.
func integer(t string) bool { return numeric[t] && t != "float32" && t != "float64" }

// idTypes are the field types an ID may have.
var idTypes = map[string]bool{
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"string": true, "uuid": true,
}

// newField resolves a field of the model. Its position moves out of the
// embedded loadjson.Field, whose Pos would be in the Graph's JSON.
func newField(lf *loadjson.Field) *Field {
	f := &Field{Field: *lf, Pos: lf.Pos, GoName: plugin.GoName(lf.Name), Column: cmp.Or(lf.StorageKey, lf.Name), Pointer: lf.Nillable}
	f.Field.Pos = ""
	return f
}

// entities makes one Entity per schema, with its table, its fields and its ID
// (steps 1 and 2).
func (b *builder) entities(m *loadjson.Model) {
	schemas := slices.Clone(m.Schemas)
	slices.SortStableFunc(schemas, func(x, y *loadjson.Schema) int { return strings.Compare(x.Name, y.Name) })
	for _, s := range schemas {
		e := &Entity{Name: s.Name, Label: plugin.Snake(s.Name), Fields: []*Field{}, Edges: []*Edge{}, Annotations: s.Annotations}
		e.Table = plugin.Plural(e.Label)
		if raw, ok := s.Annotations["Table"]; ok {
			var t struct{ Schema, Table string }
			if err := json.Unmarshal(raw, &t); err == nil {
				e.Table, e.TableSchema = cmp.Or(t.Table, e.Table), t.Schema
			}
		}
		id := -1
		for i, lf := range s.Fields {
			if lf.Name == "id" && id < 0 {
				id = i
			}
			e.Fields = append(e.Fields, newField(lf))
		}
		if id >= 0 {
			f := e.Fields[id]
			copy(e.Fields[1:id+1], e.Fields[:id])
			e.Fields[0] = f
		} else {
			auto := &Field{Field: loadjson.Field{Name: "id", Type: "int64"}, GoName: "ID", Column: "id", AutoID: true}
			e.Fields = append([]*Field{auto}, e.Fields...)
		}
		b.g.Entities = append(b.g.Entities, e)
		b.nodes = append(b.nodes, &node{ent: e, schema: s})
	}
}

// node returns the node of the entity named name, or nil.
func (b *builder) node(name string) *node {
	for _, n := range b.nodes {
		if n.ent.Name == name {
			return n
		}
	}
	return nil
}

// assoc returns n's first edge named name that is not an inverse, or nil.
func (n *node) assoc(name string) *edgeState {
	for _, s := range n.edges {
		if s.ed.Name == name && !s.le.Inverse {
			return s
		}
	}
	return nil
}

// edge returns n's first edge named name, or nil.
func (n *node) edge(name string) *edgeState {
	for _, s := range n.edges {
		if s.ed.Name == name {
			return s
		}
	}
	return nil
}

// declared returns n's first field named name that the schema declares, or nil.
func (n *node) declared(name string) *Field {
	for _, f := range n.ent.Fields {
		if f.Name == name && isDeclared(f) {
			return f
		}
	}
	return nil
}

// resolveEdges makes the edges and resolves their relations, as ent's resolve
// does (step 3). Inverse edges are resolved first, together with their assoc
// edges; the assoc edges no inverse claimed are resolved alone after them.
func (b *builder) resolveEdges() {
	for _, n := range b.nodes {
		for _, le := range n.schema.Edges {
			ed := &Edge{
				Name: le.Name, GoName: plugin.GoName(le.Name), Target: le.Type, Unique: le.Unique, Required: le.Required,
				Inverse: le.Inverse, Immutable: le.Immutable, Symbols: slices.Clone(le.Symbols),
				Comment: le.Comment, Annotations: le.Annotations, Pos: le.Pos,
			}
			s := &edgeState{ed: ed, le: le, owner: n}
			if le.TypePkg == b.pkg {
				s.target = b.node(le.Type)
			}
			// An inline inverse has its assoc edge's type, which reports it.
			if s.target == nil && !le.Inline {
				t := le.Type
				if le.TypePkg != b.pkg && le.TypePkg != "" {
					t = path.Base(le.TypePkg) + "." + t
				}
				b.fail(5, n.ent.Name, edgeLoc(ed), "type %s is not a schema of this package", t)
			}
			n.ent.Edges = append(n.ent.Edges, ed)
			n.edges = append(n.edges, s)
		}
	}
	for _, n := range b.nodes {
		for _, s := range n.edges {
			if s.le.Inverse {
				b.resolveInverse(n, s)
			}
		}
	}
	for _, n := range b.nodes {
		for _, s := range n.edges {
			if !s.le.Inverse && s.target != nil && s.ed.Rel.Kind == "" {
				b.resolveAssoc(n, s)
			}
		}
	}
}

// resolveInverse resolves the inverse edge s of n and its assoc edge.
func (b *builder) resolveInverse(n *node, s *edgeState) {
	le, ed, t := s.le, s.ed, s.target
	if t == nil {
		return // rule 5
	}
	if le.Inline && t != n {
		b.fail(28, n.ent.Name, edgeLoc(ed), "From(...) inline is allowed only on an edge to its own schema")
		return
	}
	if le.Ref == "" {
		b.fail(6, n.ent.Name, edgeLoc(ed), "an inverse edge needs Ref(name)")
		return
	}
	r := t.assoc(le.Ref)
	if r == nil {
		b.fail(7, n.ent.Name, edgeLoc(ed), "%s has no edge %q to refer to", t.ent.Name, le.Ref)
		return
	}
	if r.target != n {
		if r.target != nil {
			b.fail(8, n.ent.Name, edgeLoc(ed), "%s.%s points to %s, not to this schema", t.ent.Name, r.le.Name, r.le.Type)
		}
		return
	}
	if r.pair != nil {
		b.fail(33, n.ent.Name, edgeLoc(ed), "%s.%s already has the inverse edge %q", t.ent.Name, r.le.Name, r.pair.le.Name)
		return
	}
	if le.Required && r.le.Required {
		b.fail(9, n.ent.Name, edgeLoc(ed), "cannot be required in both directions with %s.%s", t.ent.Name, r.le.Name)
	}
	s.pair, r.pair = r, s
	ref := r.ed
	ed.Ref, ref.Ref = ref.Name, ed.Name
	e := n.ent
	table, cols := e.Table, []string{t.ent.Label + "_" + plugin.Snake(ref.Name)}
	switch au, iu := ref.Unique, le.Unique; {
	case au && iu:
		ed.Rel.Kind, ref.Rel.Kind = "O2O", "O2O"
	case !au && iu:
		ed.Rel.Kind, ref.Rel.Kind = "M2O", "O2M"
	case au && !iu:
		ed.Rel.Kind, ref.Rel.Kind = "O2M", "M2O"
		table = t.ent.Table
	default:
		ed.Rel.Kind, ref.Rel.Kind = "M2M", "M2M"
		table = t.ent.Label + "_" + ref.Name
		// The assoc edge's owner's column, then its target's.
		c1, c2 := t.ent.Label+"_id", e.Label+"_id"
		if c1 == c2 {
			c2 = plugin.Singular(le.Name) + "_id"
		}
		cols = []string{c1, c2}
	}
	// The storage key of the pair is the assoc edge's, or else the inverse's.
	if hasKey(r.le) && hasKey(le) {
		b.fail(32, n.ent.Name, edgeLoc(ed), "storage keys are declared on both %s.%s and this edge", t.ent.Name, r.le.Name)
	}
	switch {
	case hasKey(r.le):
		table, cols = b.applyKey(r, ref.Rel.Kind, table, cols)
	case hasKey(le):
		table, cols = b.applyKey(s, ed.Rel.Kind, table, cols)
	}
	ed.Rel.Table, ref.Rel.Table = table, table
	if ed.Rel.Kind == "M2M" {
		b.checkJoinColumns(s, table, cols)
	}
	ref.Rel.Columns = cols
	ed.Rel.Columns = slices.Clone(cols)
	if ed.Rel.Kind == "M2M" {
		slices.Reverse(ed.Rel.Columns) // the inverse's owner is the assoc's target
	} else {
		c := cmp.Or(r.column, s.column)
		s.column, r.column = c, c
	}
}

// resolveAssoc resolves the assoc edge s of n, which no inverse edge claimed.
func (b *builder) resolveAssoc(n *node, s *edgeState) {
	le, ed, t := s.le, s.ed, s.target
	cols := []string{n.ent.Label + "_" + plugin.Snake(le.Name)}
	switch {
	case !le.Unique && t == n:
		ed.Rel.Kind, ed.Bidi = "M2M", true
		ed.Rel.Table = n.ent.Label + "_" + le.Name
		cols = []string{n.ent.Label + "_id", plugin.Singular(le.Name) + "_id"}
	case le.Unique && t == n:
		ed.Rel.Kind, ed.Bidi = "O2O", true
		ed.Rel.Table = n.ent.Table
	case le.Unique:
		ed.Rel.Kind = "M2O"
		ed.Rel.Table = n.ent.Table
	default:
		ed.Rel.Kind = "O2M"
		ed.Rel.Table = t.ent.Table
	}
	if hasKey(le) {
		ed.Rel.Table, cols = b.applyKey(s, ed.Rel.Kind, ed.Rel.Table, cols)
	}
	if ed.Rel.Kind == "M2M" {
		b.checkJoinColumns(s, ed.Rel.Table, cols)
	}
	ed.Rel.Columns = cols
}

// checkJoinColumns reports a join table whose two columns have one name (rule
// 35), as the singular fallback gives for an edge to its own schema named
// after it: edge "nodes" on Node makes node_id twice. s is the edge whose name
// gave the second column.
func (b *builder) checkJoinColumns(s *edgeState, table string, cols []string) {
	if cols[0] == cols[1] {
		b.fail(35, s.owner.ent.Name, edgeLoc(s.ed), "join table %s has two columns named %s; name them with edge.Columns", table, cols[0])
	}
}

// hasKey reports whether le declares a storage key.
func hasKey(le *loadjson.Edge) bool { return le.Table != "" || len(le.Columns) > 0 }

// applyKey applies the storage key of s, one edge of a relation of kind, to
// the relation's table and columns, and returns them. For M2M the columns are
// the assoc edge's owner's and its target's, whichever edge declares them, as
// in ent. The parts of the key ent refuses are reported (rules 29 and 32) and
// not applied.
func (b *builder) applyKey(s *edgeState, kind, table string, cols []string) (string, []string) {
	le, where := s.le, edgeLoc(s.ed)
	m2m := kind == "M2M"
	switch {
	case le.Table == "":
	case m2m:
		table = le.Table
	default:
		b.fail(29, s.owner.ent.Name, where, "edge.Table is allowed only on many-to-many edges")
	}
	switch n := len(le.Columns); {
	case n == 0:
	case m2m && n == 1:
		b.fail(32, s.owner.ent.Name, where, "a many-to-many edge needs two columns")
	case !m2m && n > 1:
		b.fail(32, s.owner.ent.Name, where, "edge.Columns is allowed only on many-to-many edges; use edge.Column")
	default:
		cols = slices.Clone(le.Columns)
		if !m2m {
			s.column = cols[0]
		}
	}
	return table, cols
}

// ownFK reports whether the foreign key of the edge is in its owner's table,
// as ent's Edge.OwnFK does.
func ownFK(ed *Edge) bool {
	switch ed.Rel.Kind {
	case "M2O":
		return true
	case "O2O":
		return ed.Inverse || ed.Bidi
	}
	return false
}

// foreignKeys binds every non-M2M relation to the field that holds its foreign
// key, adding that field when no edge binds one (step 4: every relation has its key field).
func (b *builder) foreignKeys() {
	for _, n := range b.nodes {
		for _, s := range n.edges {
			b.foreignKey(n, s)
		}
	}
}

func (b *builder) foreignKey(n *node, s *edgeState) {
	le, ed := s.le, s.ed
	if ed.Rel.Kind == "" {
		return // unresolved: an error is recorded
	}
	own := ownFK(ed)
	if le.Field != "" {
		f := n.declared(le.Field)
		if f == nil {
			b.fail(10, n.ent.Name, edgeLoc(ed), "field %q is not declared", le.Field)
		}
		if !own {
			b.fail(11, n.ent.Name, edgeLoc(ed), "binds field %q, but the foreign key is in table %s", le.Field, ed.Rel.Table)
		}
		if f == nil || !own {
			return
		}
		id := s.target.ent.Fields[0]
		if !sameType(&f.Field, &id.Field) {
			reason := fmt.Sprintf("field %q is %s; %s's ID is %s", f.Name, typeName(&f.Field), s.target.ent.Name, typeName(&id.Field))
			if id.AutoID && integer(f.Type) && integer(id.Type) {
				// A schema ported from ent, whose default ID is an int.
				reason += `; declare it field.Int64, or declare the ID with field.Int("id")`
			}
			b.fail(12, n.ent.Name, edgeLoc(ed), "%s", reason)
		}
		switch {
		case f.Optional == le.Required:
			b.fail(13, n.ent.Name, edgeLoc(ed), "field %q must be Optional exactly when the edge is not Required", f.Name)
		case f.Optional && !f.Nillable:
			// Unset, a value field would write 0, which no row's key is.
			b.fail(38, n.ent.Name, edgeLoc(ed), "field %q must be Nillable when the edge is not Required: an unset foreign key is NULL", f.Name)
		}
		switch {
		case le.Immutable && !f.Immutable:
			b.fail(34, n.ent.Name, edgeLoc(ed), "is Immutable but field %q is not", f.Name)
		case f.Immutable && !le.Immutable:
			b.fail(34, n.ent.Name, edgeLoc(ed), "field %q is Immutable but the edge is not", f.Name)
		}
		if c := s.column; c != "" && c != f.Column {
			b.fail(31, n.ent.Name, edgeLoc(ed), "column %q differs from field %q's column %q", c, f.Name, f.Column)
		}
		b.bind(s, f.Name, f.Column)
		return
	}
	switch {
	case own && (s.pair == nil || s.pair.le.Field == ""):
		b.implicit(n, s.target, s, le.Required, ed.Rel.Kind == "O2O", le.Immutable)
	case ed.Rel.Kind == "O2M" && s.pair == nil:
		// No edge on the target's side: the foreign key references n's ID.
		b.implicit(s.target, n, s, false, false, false)
	}
}

// implicit adds to holder the foreign-key field of the edge s, which references
// ref's ID.
func (b *builder) implicit(holder, ref *node, s *edgeState, required, unique, immutable bool) {
	id := ref.ent.Fields[0]
	name := s.ed.Rel.Columns[0]
	lf := loadjson.Field{
		Name: name, Type: id.Type, Size: id.Size, SchemaType: id.SchemaType,
		Optional: !required, Nillable: !required, Unique: unique, Immutable: immutable,
	}
	if id.GoType != nil {
		g := *id.GoType
		lf.GoType = &g
	}
	holder.ent.Fields = append(holder.ent.Fields, &Field{
		Field: lf, GoName: plugin.GoName(name), Column: name, Pointer: !required, Implicit: s.ed.Name,
	})
	b.bind(s, name, name)
}

// bind records that the field named name, stored in column, holds the foreign
// key of s and of its pair.
func (b *builder) bind(s *edgeState, name, column string) {
	s.ed.Field, s.ed.Rel.Columns[0] = name, column
	if p := s.pair; p != nil {
		p.ed.Field, p.ed.Rel.Columns[0] = name, column
	}
}

// typeName names the type of f in errors: its Go type, or its field type.
func typeName(f *loadjson.Field) string {
	if f.GoType != nil {
		return f.GoType.Ident
	}
	return f.Type
}

// sameType reports whether a foreign-key field of type f can hold an ID of
// type id. The field types must be equal; a uuid, json or other type must also
// have the same Go type.
func sameType(f, id *loadjson.Field) bool {
	if f.Type != id.Type {
		return false
	}
	switch f.Type {
	case "uuid", "json", "other":
		return typeName(f) == typeName(id)
	}
	return true
}

// indexes resolves the indexes of every entity (step 5).
func (b *builder) indexes() {
	for _, n := range b.nodes {
		e := n.ent
		for i, li := range n.schema.Indexes {
			ix := &Index{Columns: []string{}, Unique: li.Unique, Where: li.Where, Annotations: li.Annotations, Pos: li.Pos}
			if len(li.Fields) == 0 && len(li.Edges) == 0 {
				b.fail(25, e.Name, indexLoc(i, ix), "names no fields or edges")
			}
			for _, name := range li.Fields {
				f := e.Field(name)
				if f == nil {
					b.fail(23, e.Name, indexLoc(i, ix), "names unknown field %q", name)
					continue
				}
				ix.Columns = append(ix.Columns, f.Column)
			}
			for _, name := range li.Edges {
				s := n.edge(name)
				switch {
				case s == nil:
					b.fail(23, e.Name, indexLoc(i, ix), "names unknown edge %q", name)
				case s.ed.Rel.Kind == "":
				case !ownFK(s.ed):
					b.fail(24, e.Name, indexLoc(i, ix), "edge %q has no column in table %s", name, e.Table)
				default:
					ix.Columns = append(ix.Columns, s.ed.Rel.Columns[0])
				}
			}
			ix.Name = cmp.Or(li.StorageKey, strings.Join(append([]string{e.Table}, ix.Columns...), "_"))
			e.Indexes = append(e.Indexes, ix)
		}
	}
}

// validate checks the rules the earlier steps do not (step 6).
func (b *builder) validate() {
	for _, n := range b.nodes {
		b.validateFields(n.ent)
		b.validateEdges(n)
		b.validateNames(n.ent)
	}
}

// shown is a Go name as an error shows it: "" when it is empty.
func shown(goName string) string {
	if goName == "" {
		return `""`
	}
	return goName
}

// validateNames checks the Go names the generator generates from (rule 30): each
// field's and edge's must be an identifier, unique among the fields and among
// the edges, and each enum value's unique within its field. An edge with a
// field's Go name is rule 4; elements of one name are rules 1, 2 and 3.
func (b *builder) validateNames(e *Entity) {
	for i, f := range e.Fields {
		if !ident(f.GoName) {
			b.fail(30, e.Name, fieldLoc(f), "its Go name %s is not a Go identifier", shown(f.GoName))
			continue
		}
		for _, g := range e.Fields[:i] {
			if g.GoName == f.GoName && g.Name != f.Name {
				b.fail(30, e.Name, fieldLoc(f), "its Go name %s is also field %q's", f.GoName, g.Name)
				break
			}
		}
	}
	for i, ed := range e.Edges {
		if !ident(ed.GoName) {
			b.fail(30, e.Name, edgeLoc(ed), "its Go name %s is not a Go identifier", shown(ed.GoName))
			continue
		}
		for _, g := range e.Edges[:i] {
			if g.GoName == ed.GoName && g.Name != ed.Name {
				b.fail(30, e.Name, edgeLoc(ed), "its Go name %s is also edge %q's", ed.GoName, g.Name)
				break
			}
		}
	}
	for _, f := range e.Fields {
		if f.Type != "enum" {
			continue
		}
		for i, v := range f.Enums {
			name := plugin.GoName(v.Name)
			if !ident(name) {
				continue // rule 20
			}
			for _, w := range f.Enums[:i] {
				if w.Value != v.Value && plugin.GoName(w.Name) == name {
					b.fail(30, e.Name, fieldLoc(f), "values %q and %q have one Go name %s", w.Value, v.Value, name)
					break
				}
			}
		}
	}
}

// isDeclared reports whether the schema declares f: it is neither the ID velox
// added nor an implicit foreign key.
func isDeclared(f *Field) bool { return !f.AutoID && f.Implicit == "" }

func (b *builder) validateFields(e *Entity) {
	seen := map[string]bool{}
	for _, f := range e.Fields {
		if !isDeclared(f) {
			continue
		}
		if seen[f.Name] {
			b.fail(1, e.Name, fieldLoc(f), "declared twice")
		}
		seen[f.Name] = true
	}
	columns := map[string]*Field{}
	for _, f := range e.Fields {
		first, ok := columns[f.Column]
		if !ok {
			columns[f.Column] = f
			continue
		}
		if first.Name == f.Name && first.Implicit == "" && f.Implicit == "" {
			continue // rule 1
		}
		reason := fmt.Sprintf("column %q is also field %q's", f.Column, first.Name)
		if isDeclared(first) && f.Implicit != "" || isDeclared(f) && first.Implicit != "" {
			// The foreign key of an edge is declared but not bound to it.
			reason += "; bind it with edge.Field(...)"
		}
		b.fail(2, e.Name, fieldLoc(f), "%s", reason)
	}
	for _, f := range e.Fields {
		if !f.Tenant {
			continue
		}
		if e.Tenant == "" {
			e.Tenant = f.Name
		} else {
			b.fail(14, e.Name, fieldLoc(f), "a second tenant field; %q is the tenant", e.Tenant)
		}
		if f.Optional || f.Nillable {
			b.fail(15, e.Name, fieldLoc(f), "the tenant field must be required and not Nillable")
		}
		if len(f.Default) > 0 || f.DefaultFunc || f.UpdateDefault {
			b.fail(16, e.Name, fieldLoc(f), "the tenant field cannot have a default: the context's tenant fills it")
		}
		if f.Type == "json" || f.Type == "bytes" {
			b.fail(17, e.Name, fieldLoc(f), "the tenant field must be of a comparable type")
		}
	}
	for _, f := range e.Fields {
		// An insert fills a Default or DefaultFunc into a zero field, so on a
		// bool or number that is not a pointer it could never store false or
		// 0. The tenant's default is rule 16's.
		if (len(f.Default) > 0 || f.DefaultFunc) && !f.Nillable && !f.Tenant && (f.Type == "bool" || numeric[f.Type]) {
			b.fail(36, e.Name, fieldLoc(f), "a Default or DefaultFunc on a bool or number that is not Nillable cannot be told from unset; make the field Nillable or drop the Default")
		}
		// Unset, an Optional enum would write "", which is no value.
		if f.Type == "enum" && f.Optional && !f.Nillable && len(f.Default) == 0 {
			b.fail(37, e.Name, fieldLoc(f), "an Optional enum needs a Default or Nillable")
		}
		// Unset, an Optional bytes field is nil, which binds NULL.
		if f.Type == "bytes" && f.Optional && !f.Nillable && len(f.Default) == 0 && !f.DefaultFunc {
			b.fail(39, e.Name, fieldLoc(f), "an Optional bytes field needs a Default or Nillable")
		}
		// Unset, an Optional time is the zero time, which MySQL's strict
		// mode refuses.
		if f.Type == "time" && f.Optional && !f.Nillable && len(f.Default) == 0 && !f.DefaultFunc {
			b.fail(40, e.Name, fieldLoc(f), "an Optional time field needs a Default or Nillable")
		}
		switch f.Type {
		case "enum":
			b.validateEnum(e, f)
		case "other":
			if len(f.SchemaType) == 0 {
				b.fail(26, e.Name, fieldLoc(f), "an Other field needs SchemaType")
			}
		}
	}
	if id := e.Fields[0]; !id.AutoID {
		if id.Optional || id.Nillable || id.UpdateDefault {
			b.fail(21, e.Name, fieldLoc(id), "the ID must be required, not Nillable, and have no UpdateDefault")
		}
		if !idTypes[id.Type] {
			b.fail(22, e.Name, fieldLoc(id), "the ID cannot be of type %s", id.Type)
		}
	}
}

func (b *builder) validateEnum(e *Entity, f *Field) {
	if len(f.Enums) == 0 {
		b.fail(18, e.Name, fieldLoc(f), "an enum needs Values")
	}
	count := map[string]int{}
	for _, v := range f.Enums {
		if count[v.Value]++; count[v.Value] == 2 {
			b.fail(19, e.Name, fieldLoc(f), "value %q is given twice", v.Value)
		}
	}
	reported := map[string]bool{}
	for _, v := range f.Enums {
		if !ident(plugin.GoName(v.Name)) && !reported[v.Value] {
			b.fail(20, e.Name, fieldLoc(f), "value %q has no Go name; use NamedValues", v.Value)
			reported[v.Value] = true
		}
	}
}

func (b *builder) validateEdges(n *node) {
	e := n.ent
	seen := map[string]bool{}
	for _, ed := range e.Edges {
		if seen[ed.Name] {
			b.fail(3, e.Name, edgeLoc(ed), "declared twice")
		}
		seen[ed.Name] = true
		for _, f := range e.Fields {
			if f.GoName == ed.GoName {
				b.fail(4, e.Name, edgeLoc(ed), "its Go name %s is also field %q's", ed.GoName, f.Name)
				break
			}
		}
		if ed.Rel.Kind == "M2M" && !ed.Inverse {
			for _, other := range b.g.Entities {
				if other.Table == ed.Rel.Table {
					b.fail(27, e.Name, edgeLoc(ed), "join table %s is also the table of schema %s", ed.Rel.Table, other.Name)
					break
				}
			}
		}
	}
}
