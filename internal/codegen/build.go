package codegen

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/codegen/loadjson"
	"github.com/syssam/velox/internal/ddl"
	"github.com/syssam/velox/plugin"
)

// Build resolves m into a Graph and checks every rule of the model. It returns
// all the rules m breaks, as *Error values joined with errors.Join, ordered by
// schema name and then by rule.
//
// Build does not change m. The Graph shares m's annotations, checks and other
// reference values.
func Build(m *loadjson.Model) (*Graph, error) {
	b := &builder{
		pkg: m.Package, g: &Graph{Package: m.Package, Entities: []*Entity{}},
		refused: map[*Index]bool{}, implicits: map[*Field]*edgeState{}, keyEdges: map[*Field]*edgeState{},
	}
	b.entities(m)
	b.resolveEdges()
	b.foreignKeys()
	b.onDeletes()
	b.indexes()
	b.names()
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
	pkg       string
	g         *Graph
	nodes     []*node // in the order of g.Entities
	errs      []ruleError
	refused   map[*Index]bool       // the indexes an index rule refuses, whose names are not compared
	implicits map[*Field]*edgeState // the edge each implicit foreign key was added for
	keyEdges  map[*Field]*edgeState // the edge each declared foreign key is bound to
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

// newField resolves a field of the model.
func newField(lf *loadjson.Field) *Field {
	return &Field{Field: *lf, Pos: lf.Pos, GoName: plugin.GoName(lf.Name), Column: cmp.Or(lf.StorageKey, lf.Name), Pointer: lf.Nillable}
}

// entities makes one Entity per schema, with its table, its fields and its ID
// (steps 1 and 2).
func (b *builder) entities(m *loadjson.Model) {
	schemas := slices.Clone(m.Schemas)
	slices.SortStableFunc(schemas, func(x, y *loadjson.Schema) int { return strings.Compare(x.Name, y.Name) })
	for _, s := range schemas {
		e := &Entity{Name: s.Name, Label: plugin.Snake(s.Name), Fields: []*Field{}, Edges: []*Edge{}, Annotations: s.Annotations}
		e.Table = tableName(e.Name)
		if raw, ok := s.Annotations["Table"]; ok {
			var t struct {
				Schema string `json:"schema"`
				Table  string `json:"table"`
			}
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
			auto := &Field{Name: "id", Type: "int64", GoName: "ID", Column: "id", AutoID: true}
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

// assocNames are the names of n's assoc edges, in declaration order.
func (n *node) assocNames() []string {
	var names []string
	for _, s := range n.edges {
		if !s.le.Inverse {
			names = append(names, s.ed.Name)
		}
	}
	return names
}

// edgeNames are the names of n's edges, in declaration order.
func (n *node) edgeNames() []string {
	names := make([]string, len(n.edges))
	for i, s := range n.edges {
		names[i] = s.ed.Name
	}
	return names
}

// fieldNames are the names of e's fields, in e.Fields' order.
func fieldNames(e *Entity) []string {
	names := make([]string, len(e.Fields))
	for i, f := range e.Fields {
		names[i] = f.Name
	}
	return names
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
		b.fail(7, n.ent.Name, edgeLoc(ed), "%s has no edge %q to refer to%s", t.ent.Name, le.Ref, suggest(le.Ref, t.assocNames()))
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
		if le.Required {
			// The core clears a partner's key whenever its partner re-points,
			// which a Required edge's key may never be.
			b.fail(53, n.ent.Name, edgeLoc(ed), "a bidirectional one-to-one edge cannot be Required: its key is cleared whenever its partner re-points; make it optional, or give it an inverse edge")
		}
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
			ft, it := typeName(&f.Field), typeName(&id.Field)
			if ft == it {
				// One printed name, two packages: say which.
				ft += " of " + strings.Join(goImports(&f.Field), ", ")
				it += " of " + strings.Join(goImports(&id.Field), ", ")
			}
			reason := fmt.Sprintf("field %q is %s; %s's ID is %s", f.Name, ft, s.target.ent.Name, it)
			if id.AutoID && f.GoType == nil && integer(f.Type) && integer(id.Type) {
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
		if ed.Rel.Kind == "O2O" {
			f.Unique = true // every one-to-one key is unique, as ent's fkColumn
		}
		if other := b.keyEdges[f]; other != nil && other != s && other != s.pair {
			b.fail(55, n.ent.Name, edgeLoc(ed), "binds field %q, which holds edge %q's foreign key: a key holds one row's ID; declare a field for each edge", f.Name, other.ed.Name)
		}
		b.keyEdges[f] = s
		b.bind(s, f.Name, f.Column)
		return
	}
	switch {
	case own && (s.pair == nil || s.pair.le.Field == ""):
		// A key to its own table is NULL-able even when the edge is Required,
		// as in ent: the first row has none to reference.
		b.implicit(n, s.target, s, le.Required && s.target != n, ed.Rel.Kind == "O2O", le.Immutable)
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
	f := &Field{Field: lf, GoName: plugin.GoName(name), Column: name, Pointer: !required, Implicit: s.ed.Name}
	holder.ent.Fields = append(holder.ent.Fields, f)
	b.implicits[f] = s
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
// type id: the field types and the Go types must both be equal, so that a
// named Go type, such as type Cents int64, holds only an ID of that type. Two
// Go types are equal when they print alike and name the same packages, so
// that types.ID of one package is not types.ID of another.
func sameType(f, id *loadjson.Field) bool {
	return f.Type == id.Type && typeName(f) == typeName(id) && slices.Equal(goImports(f), goImports(id))
}

// goImports is the package paths f's Go type names, or nil.
func goImports(f *loadjson.Field) []string {
	if f.GoType == nil {
		return nil
	}
	return f.GoType.Imports
}

// indexes resolves the indexes of every entity (step 5), and records those an
// index rule refuses: their names are rule 43's or partial, so step 5b does
// not compare them.
func (b *builder) indexes() {
	fks := foreignKeys(b.g)
	for _, n := range b.nodes {
		e := n.ent
		var ok []bool // the indexes no earlier rule refuses
		for i, li := range n.schema.Indexes {
			ix := &Index{Columns: []string{}, Unique: li.Unique, Where: li.Where, Annotations: li.Annotations, Pos: li.Pos}
			good := len(li.Fields)+len(li.Edges) > 0
			if !good {
				b.fail(25, e.Name, indexLoc(i, ix), "names no fields or edges")
			}
			for _, name := range li.Fields {
				f := e.Field(name)
				if f == nil {
					b.fail(23, e.Name, indexLoc(i, ix), "names unknown field %q%s", name, suggest(name, fieldNames(e)))
					good = false
					continue
				}
				if f.Type == "json" {
					b.fail(42, e.Name, indexLoc(i, ix), "field %q is JSON, which no index can cover", name)
					good = false
				}
				ix.Columns = append(ix.Columns, f.Column)
			}
			for _, name := range li.Edges {
				s := n.edge(name)
				switch {
				case s == nil:
					b.fail(23, e.Name, indexLoc(i, ix), "names unknown edge %q%s", name, suggest(name, n.edgeNames()))
					good = false
				case s.ed.Rel.Kind == "":
					good = false // unresolved: an error is recorded
				case !ownFK(s.ed):
					b.fail(24, e.Name, indexLoc(i, ix), "edge %q has no column in table %s", name, e.Table)
					good = false
				default:
					ix.Columns = append(ix.Columns, s.ed.Rel.Columns[0])
				}
			}
			ix.Name, ix.Derived = cmp.Or(li.StorageKey, indexName(e.Name, ix.Columns)), li.StorageKey == ""
			e.Indexes = append(e.Indexes, ix)
			ok = append(ok, good)
			if b.repeats(e, i, ok) || !good {
				b.refused[ix] = true
			}
		}
		// A partial index with no StorageKey whose name another index has,
		// or velox's foreign-key index of the table on PostgreSQL and SQLite,
		// takes the hash of its Where; a full index never does, and two of
		// one name are rule 44's.
		fkIndexes := fkIndexNames(e, fks, b.refused)
		var hashed []*Index
		for _, ix := range e.Indexes {
			if ix.Derived && ix.Where != "" && (slices.ContainsFunc(e.Indexes, func(o *Index) bool { return o != ix && o.Name == ix.Name }) || slices.Contains(fkIndexes, ix.Name)) {
				hashed = append(hashed, ix)
			}
		}
		for _, ix := range hashed {
			ix.Name = partialIndexName(e.Name, ix.Columns, ix.Where)
		}
	}
}

// names checks the names the graph puts in a database (step 5b): each one
// the schema gives, or velox derives without a cap, is at most ddl.MaxName
// bytes (rule 50); no two objects share a name in one namespace of one
// database schema, compared case-insensitively (rule 44); and enum fields
// that share a type have its values (rule 52).
func (b *builder) names() {
	b.nameLengths()
	b.namespaces()
}

// nameLengths checks rule 50 on every table, column, join table and join
// column name, explicit or derived, every index StorageKey, every Symbol and
// every enum type a SchemaType names. A derived name's error says which
// option names it.
func (b *builder) nameLengths() {
	long := func(schema string, at loc, what, name, hint string) {
		if len(name) > ddl.MaxName {
			b.fail(50, schema, at, "%s %q is %d bytes; a database name has at most %d%s", what, name, len(name), ddl.MaxName, hint)
		}
	}
	for _, n := range b.nodes {
		e := n.ent
		hint := ""
		if e.Table == tableName(e.Name) {
			hint = "; name it with schema.Table"
		}
		long(e.Name, tableLoc(e), "table name", e.Table, hint)
		for _, f := range e.Fields {
			switch s := b.implicits[f]; {
			case s != nil:
				hint := ""
				if s.column == "" {
					hint = "; name it with edge.Column"
				}
				long(s.owner.ent.Name, edgeLoc(s.ed), "column name", f.Column, hint)
			case isDeclared(f):
				hint := ""
				if f.StorageKey == "" {
					hint = "; name it with StorageKey"
				}
				long(e.Name, fieldLoc(f), "column name", f.Column, hint)
				if st := f.SchemaType[velox.Postgres.String()]; f.Type == "enum" && st != "" && !ddl.StringType(velox.Postgres, st) {
					long(e.Name, fieldLoc(f), "enum type name", st, "")
				}
			}
		}
		for i, li := range n.schema.Indexes {
			long(e.Name, indexLoc(i, e.Indexes[i]), "index name", li.StorageKey, "")
		}
		// A bad annotation is declaredChecks's error.
		declared, _ := checksOf(e)
		for _, c := range declared {
			long(e.Name, tableLoc(e), "check name", c.Name, "")
		}
		for _, s := range n.edges {
			for _, sym := range s.le.Symbols {
				long(e.Name, edgeLoc(s.ed), "symbol", sym, "")
			}
			if s.ed.Rel.Kind != "M2M" || s.le.Inverse {
				continue
			}
			// The join table's names are the assoc edge's, unless its
			// inverse gives them.
			by, hint := s, "; name it with edge.Table"
			if p := s.pair; s.le.Table == "" && p != nil && p.le.Table != "" {
				by = p
			}
			if by.le.Table != "" {
				hint = ""
			}
			long(by.owner.ent.Name, edgeLoc(by.ed), "join table name", s.ed.Rel.Table, hint)
			by, hint = s, "; name them with edge.Columns"
			if p := s.pair; len(s.le.Columns) == 0 && p != nil && len(p.le.Columns) > 0 {
				by = p
			}
			if len(by.le.Columns) > 0 {
				hint = ""
			}
			for i, c := range s.ed.Rel.Columns {
				if i == 0 || c != s.ed.Rel.Columns[0] { // rule 35
					long(by.owner.ent.Name, edgeLoc(by.ed), "join table column name", c, hint)
				}
			}
		}
	}
}

// namespaces checks rules 44 and 52 over the objects of the graph. The
// second object with a name reports the first. Two tables are compared once,
// as relations, and the other objects of tables that share a name are not
// compared. Enum types of one name are one type when their values are equal
// and in order; rule 52 reports them otherwise. A join table with an entity's
// table's name is rule 27's.
func (b *builder) namespaces() {
	type key struct {
		space        nameSpace
		schema, name string
	}
	first := map[key]object{}
	clashed := map[[2]string]bool{} // the tables that share a name, lower case, by database schema
	for _, o := range objects(b.g, b.refused) {
		if !o.isTable() && clashed[[2]string{strings.ToLower(o.schema), strings.ToLower(o.table)}] {
			continue
		}
		k := key{o.space, strings.ToLower(o.schema), strings.ToLower(o.name)}
		p, dup := first[k]
		if !dup {
			first[k] = o
			continue
		}
		switch {
		case o.space == spaceTypes && o.isTable() && p.isTable():
			continue
		case o.values != nil && p.values != nil:
			if !slices.Equal(o.values, p.values) {
				other := "this schema"
				if p.owner != o.owner {
					other = "schema " + p.owner
				}
				if p.at.pos != "" {
					other += " (" + p.at.pos + ")"
				}
				b.fail(52, o.owner, o.at, "enum type %s is also %s of %s, with other values", o.name, strings.TrimPrefix(p.what, "enum type of "), other)
			}
			continue
		case o.isTable() && p.isTable():
			clashed[[2]string{k.schema, strings.ToLower(o.table)}] = true
			if o.what != p.what && o.name == p.name {
				continue // rule 27
			}
		}
		other := "this schema's "
		if p.owner != o.owner {
			other = "schema " + p.owner + "'s "
		}
		hint := ""
		if strings.HasPrefix(o.what, "unique index of ") && strings.HasPrefix(p.what, "index ") {
			hint = "; give " + p.what + " another StorageKey" // a declared index of the field's index's name over other columns
		}
		b.fail(44, o.owner, o.at, "name %q is also %s%s%s", o.name, other, p.what, hint)
	}
}

// repeats reports whether the index i of e repeats an earlier one (rule 43),
// and reports it: the same columns in order, the same Unique and the same
// Where, among the declared indexes, mixins' included. A field's Unique is not
// compared. ok tells the indexes no earlier rule refuses; the others are
// skipped.
func (b *builder) repeats(e *Entity, i int, ok []bool) bool {
	ix := e.Indexes[i]
	if !ok[i] {
		return false
	}
	for j, prev := range e.Indexes[:i] {
		if ok[j] && ix.Unique == prev.Unique && ix.Where == prev.Where && slices.Equal(ix.Columns, prev.Columns) {
			b.fail(43, e.Name, indexLoc(i, ix), "repeats index %d", j)
			return true
		}
	}
	return false
}

// validate checks the rules the earlier steps do not (step 6).
func (b *builder) validate() {
	for _, n := range b.nodes {
		b.validateFields(n.ent)
		b.validateEdges(n)
		b.validateNames(n.ent)
	}
	b.validateScopes()
}

// scopeKey matches the key of a Scoped annotation: it makes the Go name of the
// key's variable, <Key>Key.
var scopeKey = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// scoped returns the key of f's Scoped annotation, and false when it has none
// or the annotation is not one. A key that cannot be read is reported by
// validateScopes through its empty value.
func scoped(f *Field) (key string, ok bool) {
	raw, ok := f.Annotations["Scoped"]
	if !ok {
		return "", false
	}
	var a struct {
		Key string `json:"key"`
	}
	_ = json.Unmarshal(raw, &a)
	return a.Key, true
}

// scopeType is the Go type name an error shows for the scoped field f.
func scopeType(f *Field) string {
	if f.GoType != nil {
		return f.GoType.Ident
	}
	return f.Type
}

// validateScopes checks the Scoped fields (rules 57 to 59): each is Immutable,
// neither Optional nor Nillable, of a type that has an equality condition and a
// zero value for "unset"; an entity has at most one; a key has one type; and a
// key makes a Go name.
func (b *builder) validateScopes() {
	type first struct{ entity, typ string }
	types := map[string]first{}
	for _, n := range b.nodes {
		e := n.ent
		var prev *Field
		for _, f := range e.Fields {
			key, ok := scoped(f)
			if !ok {
				continue
			}
			switch {
			case !f.Immutable:
				b.fail(57, e.Name, fieldLoc(f), "a Scoped field must be Immutable, so that an update cannot move a row to another scope")
			case f.Optional || f.Nillable:
				b.fail(57, e.Name, fieldLoc(f), "a Scoped field cannot be Optional or Nillable: a missing value would mean every scope")
			case !integer(f.Type) && f.Type != "string" && f.Type != "uuid":
				b.fail(57, e.Name, fieldLoc(f), "a Scoped field must be an integer, string or UUID, not %s", f.Type)
			}
			if !scopeKey.MatchString(key) {
				b.fail(58, e.Name, fieldLoc(f), "the Scoped key %q has no Go name: use letters, digits and underscores, starting with a lower-case letter", key)
				continue
			}
			if prev != nil {
				b.fail(58, e.Name, fieldLoc(f), "an entity has one Scoped field, and %q is already", prev.Name)
				continue
			}
			prev = f
			if old, ok := types[key]; ok && old.typ != scopeType(f) {
				b.fail(59, e.Name, fieldLoc(f), "key %q is %s in %s and %s here: a key has one type", key, old.typ, old.entity, scopeType(f))
			} else if !ok {
				types[key] = first{e.Name, scopeType(f)}
			}
		}
	}
}

// shown is a Go name as an error shows it: "" when it is empty.
func shown(goName string) string {
	if goName == "" {
		return `""`
	}
	return goName
}

// ascii reports whether s is ASCII. plugin.Snake and plugin.Plural work on
// bytes, as ent's do, and their outputs never change (rule 56).
func ascii(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool { return r >= utf8.RuneSelf }) < 0
}

// validateNames checks the Go names the generator generates from (rule 30): each
// field's and edge's must be an identifier, unique among the fields and among
// the edges, and each enum value's unique within its field. An edge with a
// field's Go name is rule 4; elements of one name are rules 1, 2 and 3.
func (b *builder) validateNames(e *Entity) {
	if !ascii(e.Name) {
		b.fail(56, e.Name, loc{}, "its name is not ASCII, and velox derives table, column and Go names from it: rename the schema")
	}
	for _, f := range e.Fields {
		if !ascii(f.Name) && isDeclared(f) {
			b.fail(56, e.Name, fieldLoc(f), "its name is not ASCII, and velox derives column and Go names from it: rename it, and give the column its name with StorageKey")
		}
	}
	for _, ed := range e.Edges {
		if !ascii(ed.Name) {
			b.fail(56, e.Name, edgeLoc(ed), "its name is not ASCII, and velox derives column and Go names from it: rename it")
		}
	}
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

// unencodable names, by kind, the Go types encoding/json cannot write (rule
// 49); complexKind marks those a MarshalJSON or MarshalText makes writable.
var (
	unencodable = map[string]string{
		"interface": "an interface", "func": "a func", "chan": "a chan", "unsafe.Pointer": "an unsafe.Pointer",
		"complex64": "a complex64", "complex128": "a complex128",
	}
	complexKind = map[string]bool{"complex64": true, "complex128": true}
)

// optionalNeeds lists the field types whose unset value cannot be stored, so
// an Optional field of the type that is not Nillable needs a Default.
var optionalNeeds = []struct {
	typ, noun string
	rule      int
	funcOK    bool // a DefaultFunc counts as a Default
}{
	{"enum", "enum", 37, false},        // unset, an enum is "", which is no value
	{"bytes", "bytes field", 39, true}, // unset, a bytes field is nil, which binds NULL
	{"time", "time field", 40, true},   // unset, a time is the zero time, which MySQL's strict mode refuses
}

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
		// An insert fills a Default or DefaultFunc into a zero field, so on a
		// bool or number that is not a pointer it could never store false or
		if (len(f.Default) > 0 || f.DefaultFunc) && !f.Nillable && (f.Type == "bool" || numeric[f.Type]) {
			b.fail(36, e.Name, fieldLoc(f), "a Default or DefaultFunc on a bool or number that is not Nillable cannot be told from unset; make the field Nillable or drop the Default")
		}
		if b.keyEdges[f] == nil && uniqueUnset(e, f) { // a bound key is rule 38's
			b.fail(54, e.Name, fieldLoc(f), "an Optional field that is Unique holds one value on every unset row, so the second row without it fails; make it Nillable, whose NULLs do not collide, or give it a DefaultFunc that a value a hook fills can stand in for, as velox-gen cannot see a hook")
		}
		for _, r := range optionalNeeds {
			if f.Type == r.typ && f.Optional && !f.Nillable && len(f.Default) == 0 && (!r.funcOK || !f.DefaultFunc) {
				b.fail(r.rule, e.Name, fieldLoc(f), "an Optional %s needs a Default or Nillable", r.noun)
			}
		}
		// encoding/json cannot write an interface, func, chan or unsafe
		// pointer, nor a complex number without its own encoding. A func or
		// chan with a MarshalJSON stays refused: its nil is no pointer, slice
		// or map, so the core could not take it as NULL.
		unwritable := false
		if f.Type == "json" && f.GoType != nil {
			if noun, ok := unencodable[f.GoType.Kind]; ok && (!f.GoType.Marshaler || !complexKind[f.GoType.Kind]) {
				unwritable = true
				hint := ""
				if complexKind[f.GoType.Kind] {
					hint = "; give the type a MarshalJSON or MarshalText method"
				}
				b.fail(49, e.Name, fieldLoc(f), "its Go type %s is %s, which a JSON column cannot hold%s", f.GoType.Ident, noun, hint)
			}
		}
		// An Optional JSON field's column holds NULL, so its Go field must
		// hold nil: a slice, a map or a pointer, or a pointer that Nillable
		// makes. A type no codec can write gets only the error above: adding
		// Nillable would not help it.
		if f.Type == "json" && f.Optional && !f.Nillable && f.GoType != nil && !unwritable && !nilKinds[f.GoType.Kind] {
			b.fail(48, e.Name, fieldLoc(f), "an Optional JSON field of a non-nil-able type needs Nillable")
		}
		// No index covers a JSON column (rule 42 is a declared index's).
		if f.Type == "json" && f.Unique {
			b.fail(41, e.Name, fieldLoc(f), "a JSON field cannot be Unique")
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
		if integer(id.Type) && len(id.SchemaDefault) > 0 {
			b.fail(21, e.Name, fieldLoc(id), "an integer ID is assigned by the database and cannot have a SchemaDefault")
		}
		if !idTypes[id.Type] {
			b.fail(22, e.Name, fieldLoc(id), "the ID cannot be of type %s", id.Type)
		}
	}
}

// uniqueUnset reports whether every unset row of e holds one value of f, and a
// unique index of f alone refuses the second: f is Optional, not Nillable, and
// nothing gives each row its own value (a DefaultFunc, or the database's
// SchemaDefault, may). A partial index need not cover the unset rows, and a
// JSON field cannot be Unique (rule 41).
func uniqueUnset(e *Entity, f *Field) bool {
	if !f.Optional || f.Nillable || f.DefaultFunc || len(f.SchemaDefault) > 0 || f.Type == "json" {
		return false
	}
	return f.Unique || slices.ContainsFunc(e.Indexes, func(ix *Index) bool {
		return ix.Unique && ix.Where == "" && len(ix.Columns) == 1 && ix.Columns[0] == f.Column
	})
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
	for _, s := range n.edges {
		b.validateSymbols(s)
	}
}

// validateSymbols checks the foreign-key names of the resolved edge s (rule
// 51): one for a relation that is not many-to-many, two for one that is, and
// on an inverse edge the same as its pair's when both give them.
func (b *builder) validateSymbols(s *edgeState) {
	le, ed := s.le, s.ed
	if ed.Rel.Kind == "" {
		return // unresolved: an error is recorded
	}
	want := 1
	if ed.Rel.Kind == "M2M" {
		want = 2
	}
	switch n := len(le.Symbols); {
	case n == 0:
		return
	case want == 1 && n > 1:
		b.fail(51, s.owner.ent.Name, edgeLoc(ed), "a relation that is not many-to-many has one foreign key; use edge.Symbol")
		return
	case want == 2 && n == 1:
		b.fail(51, s.owner.ent.Name, edgeLoc(ed), "a many-to-many relation has two foreign keys; name them with edge.Symbols")
		return
	}
	if p := s.pair; le.Inverse && p != nil && len(p.le.Symbols) == want && !slices.Equal(le.Symbols, p.le.Symbols) {
		b.fail(51, s.owner.ent.Name, edgeLoc(ed), "Symbols %q differ from edge %q's %q on %s", le.Symbols, p.le.Name, p.le.Symbols, p.owner.ent.Name)
	}
}

// onDeletes checks the actions of the foreign keys (rules 45-47) and gives both
// edges of a relation its action: the one that is set, since a pair that sets
// two must agree. With none set, a NULL-able key is SET NULL and a NOT NULL one
// has none (NO ACTION), as ent's deleteAction.
func (b *builder) onDeletes() {
	for _, n := range b.nodes {
		for _, s := range n.edges {
			le, ed := s.le, s.ed
			if ed.Rel.Kind == "" {
				continue // unresolved: an error is recorded
			}
			act := le.OnDelete
			if p := s.pair; p != nil && p.le.OnDelete != "" {
				switch {
				case act == "":
					act = p.le.OnDelete
				case act != p.le.OnDelete && le.Inverse:
					b.fail(47, n.ent.Name, edgeLoc(ed), "OnDelete %s differs from edge %q's %s on %s", act, p.le.Name, p.le.OnDelete, p.owner.ent.Name)
				}
			}
			if act == "" && ed.Rel.Kind != "M2M" && !b.notNullable(s) {
				act = "SET NULL"
			}
			ed.Rel.OnDelete = act
			if le.OnDelete == "" {
				continue
			}
			if ed.Rel.Kind == "M2M" {
				b.fail(45, n.ent.Name, edgeLoc(ed), "OnDelete has no foreign key to act on in a many-to-many edge; join rows are deleted with either end")
				continue
			}
			if le.OnDelete == "SET NULL" && b.notNullable(s) {
				b.fail(46, n.ent.Name, edgeLoc(ed), "SET NULL needs a NULL-able foreign key")
			}
		}
	}
}

// notNullable reports whether the foreign key of the relation of s cannot be
// NULL: either edge is Required, unless the relation is to its own schema,
// whose implicit key is NULL-able, or the field that holds the key is not
// Nillable.
func (b *builder) notNullable(s *edgeState) bool {
	if s.target != s.owner && (s.le.Required || (s.pair != nil && s.pair.le.Required)) {
		return true
	}
	holder := s.owner
	if !ownFK(s.ed) && s.target != nil {
		holder = s.target
	}
	for _, f := range holder.ent.Fields {
		if f.Name == s.ed.Field {
			return !f.Nillable
		}
	}
	return false
}
