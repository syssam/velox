package sdl

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/plugin"
)

// mutationsOf computes o's mutation inputs, CreateXInput and UpdateXInput,
// and adds its mutations to the root Mutation, for the kinds its Mutations
// annotation lists.
func (g *graph) mutationsOf(o *object) error {
	e := o.ent
	where := "schema " + e.Name
	var create, update, del bool
	for _, k := range o.ann.Mutations {
		switch k {
		case graphql.MutationCreate:
			create = true
		case graphql.MutationUpdate:
			update = true
		case graphql.MutationDelete:
			del = true
		default:
			return fmt.Errorf("%s: graphql.Mutations: %q is not a mutation; the mutations are create, update and delete", where, string(k))
		}
	}
	for _, c := range []struct {
		on   bool
		kind string
		skip graphql.SkipMode
		name string
	}{
		{create, "create", graphql.SkipMutationCreateInput, "SkipMutationCreateInput"},
		{update, "update", graphql.SkipMutationUpdateInput, "SkipMutationUpdateInput"},
	} {
		if c.on && o.ann.Skip&c.skip != 0 {
			return fmt.Errorf("%s: graphql.Mutations lists %s, and graphql.Skip(%s) leaves out its input", where, c.kind, c.name)
		}
	}
	for _, c := range []struct {
		on   bool
		kind string
	}{{update, "update"}, {del, "delete"}} {
		if c.on && !o.node {
			return fmt.Errorf("%s: graphql.Mutations lists %s, which takes an ID, and %s: it is not a Node", where, c.kind, notNode(e))
		}
	}

	if !create {
		for i, f := range e.Fields {
			if o.fanns[i].CreateDefault != nil {
				return fmt.Errorf("%s, field %s: graphql.CreateDefault, and graphql.Mutations does not list create", where, f.Name)
			}
		}
	}
	if create && o.node {
		pk := e.Fields[e.PrimaryKey[0]]
		if !pk.AutoID && !pk.HasDefault {
			return fmt.Errorf("%s: graphql.Mutations lists create, and its key %s is supplied by the caller (no auto ID, no default): v1 does not generate createX for such a schema, as the key is never an input", where, pk.Name)
		}
	}
	if create {
		in := "Create" + o.name + "Input"
		fs, gos, err := g.inputFields(o, in, true)
		if err != nil {
			return err
		}
		if len(fs) == 0 {
			return fmt.Errorf("%s: graphql.Mutations lists create, and its create input would have no fields", where)
		}
		if err := g.types.add(in, where); err != nil {
			return err
		}
		o.create, o.createGo = fs, gos
		g.mutation = append(g.mutation, gqlField{
			name: "create" + o.name, args: "(input: " + in + "!)", typ: o.name + "!",
			desc: "Creates one " + o.name + ".", where: where,
		})
	}
	if update {
		// An update input with no fields gets no type, and no updateX.
		in := "Update" + o.name + "Input"
		fs, gos, err := g.inputFields(o, in, false)
		if err != nil {
			return err
		}
		if len(fs) > 0 {
			if err := g.types.add(in, where); err != nil {
				return err
			}
			o.update, o.updateGo = fs, gos
			g.mutation = append(g.mutation, gqlField{
				name: "update" + o.name, args: "(id: ID!, input: " + in + "!)", typ: o.name + "!",
				desc: "Updates the " + o.name + " with this ID: only the fields given change.", where: where,
			})
		}
	}
	if del {
		o.del = true
		g.mutation = append(g.mutation, gqlField{
			name: "delete" + o.name, args: "(id: ID!)", typ: "ID!",
			desc: "Deletes the " + o.name + " with this ID, and returns the ID.", where: where,
		})
	}
	return nil
}

// inputFields are the fields of o's create input, or its update input:
//   - neither has the primary key, the tenant or a foreign key; the edges
//     stand for the foreign keys;
//   - neither has a field whose Skip leaves out the input, or that
//     CreateInput(false) or UpdateInput(false) leaves out;
//   - a field with an UpdateDefault and no Default is not in the update input
//     (the UpdateDefault fills it), nor in the create input, unless it is
//     required, when nothing else fills it on insert: it is in the create
//     input, and required; CreateInput(true) or UpdateInput(true) brings
//     any such field into that input;
//   - the update input has no immutable field or edge;
//   - on create, a field is required unless it is Optional, nullable or has
//     a Default (or DefaultFunc), so an UpdateDefault alone does not make it
//     optional;
//     a unique edge's ID is required when the edge is;
//   - on update, every field is optional, and a nullable field, whose column
//     holds NULL, can be cleared;
//   - an edge to a type that is not a Node has no ID and is in neither;
//   - an edge's inputs follow its foreign key's flags, on whichever entity
//     holds it (see edgeInputs);
//   - a field's inputs carry its directives, and an edge's its directives,
//     as the where input's do: a directive that guards a value guards its
//     writes too.
//
// It returns the fields for the SDL and, in the same order, for the Go code.
func (g *graph) inputFields(o *object, typ string, create bool) ([]gqlField, []InputField, error) {
	e := o.ent
	skip, explicit := graphql.SkipMutationUpdateInput, func(a graphql.Annotation) *bool { return a.UpdateInput }
	if create {
		skip, explicit = graphql.SkipMutationCreateInput, func(a graphql.Annotation) *bool { return a.CreateInput }
	}
	fs := newInputFields(typ)
	var gos []InputField
	for i, f := range e.Fields {
		a := o.fanns[i]
		if create && a.CreateDefault != nil && a.Skip&skip != 0 {
			return nil, nil, fmt.Errorf("schema %s, field %s: graphql.CreateDefault on a field the create input leaves out", e.Name, f.Name)
		}
		switch {
		case o.node && i == e.PrimaryKey[0], i == e.Tenant, f.Implicit != "", o.fks[f.Name], a.Skip&skip != 0, !create && f.Immutable:
			continue
		}
		// An UpdateDefault fills the field on update only: on create it is
		// left out, unless nothing else fills it, which is a field that is
		// not nullable and has no Default.
		required := !f.Optional && !f.Nullable
		in := !f.UpdateDefault || f.HasDefault || create && required
		if p := explicit(a); p != nil {
			in = *p
		}
		where := "schema " + e.Name + ", field " + f.Name
		if !in {
			if create && a.CreateDefault != nil {
				return nil, nil, fmt.Errorf("%s: graphql.CreateDefault on a field the create input leaves out", where)
			}
			continue
		}
		name, err := o.fieldName(i)
		if err != nil {
			return nil, nil, err
		}
		t, err := g.fieldType(o, f, a, where)
		if err != nil {
			return nil, nil, err
		}
		var def string
		if create && a.CreateDefault != nil {
			if def, err = createDefault(f, a.CreateDefault, where); err != nil {
				return nil, nil, err
			}
			t += "!" // a value in Go: the default fills it
		}
		if create && required && !f.HasDefault && def == "" {
			t += "!"
		}
		dirs, err := directives(a.Directives, where)
		if err != nil {
			return nil, nil, err
		}
		if err := fs.add(gqlField{name: name, typ: t, def: def, desc: f.Comment, dirs: inputDirs(dirs, t), where: where}); err != nil {
			return nil, nil, err
		}
		v := g.goValue(f, a)
		if f.Type == "json" && !v.own {
			v.typ, v.pkg = v.getter, v.getterPkg
		}
		goName := goOf(name, f.GoName)
		gos = append(gos, InputField{
			Name: name, GoName: goName, Type: t, Kind: InputValue, Field: i, Edge: -1,
			GoType: v.typ, GoPkg: v.pkg, Required: strings.HasSuffix(t, "!"),
		})
		if !create && f.Nullable {
			cl := gqlField{name: "clear" + upper(name), typ: "Boolean", dirs: dirs, where: where}
			if err := fs.add(cl); err != nil {
				return nil, nil, err
			}
			gos = append(gos, InputField{Name: cl.name, GoName: "Clear" + goName, Type: cl.typ, Kind: InputClear, Field: i, Edge: -1})
		}
	}
	for i := range e.Edges {
		if o.eanns[i].Skip&skip != 0 {
			continue
		}
		add, err := g.edgeInputs(o, i, create)
		if err != nil {
			return nil, nil, err
		}
		for _, f := range add {
			if err := fs.add(f.gqlField); err != nil {
				return nil, nil, err
			}
			gos = append(gos, f.goField)
		}
	}
	return fs.list, gos, nil
}

// edgeInput is an input field of an edge, for the SDL and for the Go code.
type edgeInput struct {
	gqlField
	goField InputField
}

// edgeInputs are the input fields of o's edge i, on create or on update.
// An edge stands for a foreign key, and its inputs only offer what a write
// of that key may do:
//   - a unique edge whose key is on o's row (OwnsKey) sets it: <edge>ID, and
//     on update clear<Edge> when the key is nullable;
//   - an edge whose key is on the target's rows (O2M, and an O2O whose key
//     the other entity holds) re-points those rows: <edge>IDs, or
//     add/remove/clear on update; an O2O sets <edge>ID, and clears it on
//     update. Only a nullable key that can move gets them: a required
//     (non-null) key always holds an owner, from whom a write would only
//     take the row, and an immutable one cannot move, so the edge has no
//     input at all, on create too; the child is linked from its side;
//   - when that key is a requiredKey, NULL-able but a Required edge's (a
//     Required self edge's), the target's row may hold no owner, but velox
//     refuses to unlink it: on update, an O2M has add<Edge>IDs only, and an
//     O2O nothing; create keeps its inputs, which only link;
//   - an immutable key on o's row is set on create only;
//   - an M2M edge writes join rows: <edge>IDs, add/remove/clear;
//   - a key on o's row that is the tenant has no input: the tenant scope fills
//     it, as it does a tenant with no edge;
//   - an edge to a type that is not a Node has no ID and no input. When that
//     edge is required and holds its key on o's row, createX could never
//     succeed: an error.
//
// Each input carries the edge's directives, after those of its key when the
// key is on o's row.
func (g *graph) edgeInputs(o *object, i int, create bool) ([]edgeInput, error) {
	ed := o.ent.Edges[i]
	t := g.byName[ed.Target]
	where := "schema " + o.ent.Name + ", edge " + ed.Name
	if t == nil || !t.node {
		// A required key on o's own row that createX could never set. When
		// the target's row holds the key, createX writes none of its own.
		if create && ed.Unique && ed.Required && ed.OwnsKey {
			why := "is skipped"
			if t != nil {
				why = "is not a Node"
			}
			return nil, fmt.Errorf("%s: a required edge whose target %s %s: create%s could never set it; skip the edge's create input with graphql.Skip(graphql.SkipMutationCreateInput), and set its key in the resolver through Create%s's set, or in a hook", where, ed.Target, why, o.name, o.name)
		}
		return nil, nil
	}
	name, err := o.edgeName(i)
	if err != nil {
		return nil, err
	}
	fk := g.keyField(ed)
	if ed.OwnsKey && ed.FieldOn == o.ent.Name && o.ent.Tenant >= 0 && fk == o.ent.Fields[o.ent.Tenant] {
		return nil, nil // the tenant scope fills the key
	}
	immutable := ed.Immutable || fk != nil && fk.Immutable
	nullable := fk == nil || fk.Nullable
	dirs, err := directives(o.eanns[i].Directives, where)
	if err != nil {
		return nil, err
	}
	// A key on o's row brings its own directives first, as to its where
	// predicates.
	if ed.OwnsKey && ed.FieldOn == o.ent.Name {
		if k := slices.IndexFunc(o.ent.Fields, func(f *plugin.Field) bool { return f.Name == ed.Field }); k >= 0 {
			kds, err := directives(o.fanns[k].Directives, "schema "+o.ent.Name+", field "+ed.Field)
			if err != nil {
				return nil, err
			}
			dirs = unite(kds, dirs)
		}
	}
	// The Go names follow the GraphQL names' parts: the edge's Go name, when
	// the GraphQL name was made from it, and its singular. The singular is
	// ent's (MutationAdd), of the lowercase name, never of a capitalised one:
	// the edge name's for an edge the GraphQL name was made from, so children
	// gives childIDs and ChildIDs; a renamed edge's GraphQL name's, so a
	// rename to people gives addPersonIDs and AddPersonIDs.
	goEdge := goOf(name, ed.GoName)
	one, many := upper(plugin.Singular(name)), plugin.Singular(name)
	goOne := one
	if camel(ed.GoName) == name {
		goOne = plugin.GoName(plugin.Singular(ed.Name))
		many = camel(goOne)
		one = upper(many)
	}
	var out []edgeInput
	add := func(name, typ, goName string, kind InputKind) {
		out = append(out, edgeInput{
			name: name, typ: typ, dirs: inputDirs(dirs, typ), where: where,
			goField: InputField{Name: name, GoName: goName, Type: typ, Kind: kind, Field: -1, Edge: i, Target: t.name, Required: strings.HasSuffix(typ, "!")},
		})
	}
	edgeID := func(typ string) { add(name+"ID", typ, goEdge+"ID", InputEdgeID) }
	clearEdge := func() { add("clear"+upper(name), "Boolean", "Clear"+goEdge, InputClearEdge) }
	switch {
	case ed.Kind == plugin.M2M:
		if immutable && !create {
			break
		}
		if create {
			add(many+"IDs", "[ID!]", goOne+"IDs", InputEdgeIDs)
		} else {
			add("add"+one+"IDs", "[ID!]", "Add"+goOne+"IDs", InputAddIDs)
			add("remove"+one+"IDs", "[ID!]", "Remove"+goOne+"IDs", InputRemoveIDs)
			clearEdge()
		}
	case ed.Unique && ed.OwnsKey:
		switch {
		case create && ed.Required:
			edgeID("ID!")
		case create:
			edgeID("ID")
		case immutable:
		default:
			edgeID("ID")
			if !ed.Required && nullable {
				clearEdge()
			}
		}
	case immutable || !nullable:
		// The key is on the target's rows. An immutable one cannot move; a
		// required one always holds an owner, whom a write could only take
		// the row from, so the input could never succeed: the child is
		// linked by its own <edge>ID.
	case !create && requiredKey(g.ents[ed.FieldOn], fk):
		// The key is on the target's rows and is a Required edge's, NULL-able
		// (a Required self edge's). Unlinking a child would leave its key
		// NULL, which velox refuses: an O2O's re-point unlinks first, and so
		// do remove and clear. Only an O2M's add, which sets a NULL key,
		// can succeed; a child moves by its own <edge>ID.
		if !ed.Unique {
			add("add"+one+"IDs", "[ID!]", "Add"+goOne+"IDs", InputAddIDs)
		}
	case ed.Unique:
		typ := "ID"
		if create && ed.Required {
			typ = "ID!"
		}
		edgeID(typ)
		if !create && !ed.Required {
			clearEdge()
		}
	case create:
		add(many+"IDs", "[ID!]", goOne+"IDs", InputEdgeIDs)
	default:
		add("add"+one+"IDs", "[ID!]", "Add"+goOne+"IDs", InputAddIDs)
		add("remove"+one+"IDs", "[ID!]", "Remove"+goOne+"IDs", InputRemoveIDs)
		clearEdge()
	}
	return out, nil
}

// requiredKey reports whether a Required edge of t that owns its key holds
// the field fk, and fk is NULL-able: the key of any Required edge whose key
// is NULL-able, such as a self edge's. velox refuses such a key nil
// (velox.ColumnDescriptor.RequiredEdge), and the database's ON DELETE SET NULL can
// still clear it. velox-gen marks the same columns: a Nillable field held by
// a Required edge that owns it (internal/codegen/lower.go, OutField.RequiredEdge),
// which the plugin model gives as Required, OwnsKey and Field
// (internal/codegen/plugin.go), with the field Nullable.
func requiredKey(t *plugin.Entity, fk *plugin.Field) bool {
	if t == nil || fk == nil || !fk.Nullable {
		return false
	}
	return slices.ContainsFunc(t.Edges, func(ed *plugin.Edge) bool {
		return ed.Required && ed.OwnsKey && ed.Field == fk.Name
	})
}

// keyField is the foreign-key field ed stands for, on whichever entity holds
// it; nil for an M2M edge, or a field the model does not have.
func (g *graph) keyField(ed *plugin.Edge) *plugin.Field {
	if ed.Field == "" {
		return nil
	}
	h := g.ents[ed.FieldOn]
	if h == nil {
		return nil
	}
	for _, f := range h.Fields {
		if f.Name == ed.Field {
			return f
		}
	}
	return nil
}

// createDefault is the SDL of f's graphql.CreateDefault d, as " = true": a
// bool, or a number of f's type, a decimal string for a Uint64. It is for a
// bool or number with no Default that is not nullable, whose Default velox-gen
// refuses.
func createDefault(f *plugin.Field, d json.RawMessage, where string) (string, error) {
	switch {
	case !zeroIsUnset(f.Type):
		return "", fmt.Errorf("%s: graphql.CreateDefault on a field of type %s; it is for a bool or number, whose non-zero Default velox-gen refuses unless it is Nillable: give this field a Default", where, f.Type)
	case f.Nullable:
		return "", fmt.Errorf("%s: graphql.CreateDefault on a nullable field, which may have a Default of its own", where)
	case f.HasDefault:
		return "", fmt.Errorf("%s: graphql.CreateDefault on a field with a Default, which the create input applies already", where)
	}
	s := string(d)
	if f.Type == "bool" {
		if s != "true" && s != "false" {
			return "", fmt.Errorf("%s: graphql.CreateDefault(%s) on a bool field", where, s)
		}
		return " = " + s, nil
	}
	bits := 64
	if n := strings.TrimLeft(f.Type, "uinfloat"); n != "" {
		bits, _ = strconv.Atoi(n)
	}
	var err error
	switch {
	case strings.HasPrefix(f.Type, "float"):
		var v float64
		if v, err = strconv.ParseFloat(s, bits); err == nil && (math.IsInf(v, 0) || math.IsNaN(v)) {
			err = strconv.ErrRange
		}
	case strings.HasPrefix(f.Type, "uint"):
		var v uint64
		if v, err = strconv.ParseUint(s, 10, bits); err == nil && v > math.MaxInt64 {
			return "", fmt.Errorf("%s: graphql.CreateDefault(%s) is over 9223372036854775807: database/sql cannot send a uint64 with the high bit set", where, s)
		}
	default:
		_, err = strconv.ParseInt(s, 10, bits)
	}
	if err != nil {
		return "", fmt.Errorf("%s: graphql.CreateDefault(%s) is not a value of the %s field", where, s, f.Type)
	}
	if f.Type == "uint" || f.Type == "uint64" {
		return ` = "` + s + `"`, nil // the Uint64 scalar is a string
	}
	return " = " + s, nil
}

// inputDirs are the directives ds of an input field of the type typ: all of
// them, except @deprecated on a required field, which GraphQL forbids, since
// a client cannot stop sending it. The output field keeps it.
func inputDirs(ds []directive, typ string) []directive {
	if !strings.HasSuffix(typ, "!") {
		return ds
	}
	return slices.DeleteFunc(slices.Clone(ds), func(d directive) bool { return d.name == "deprecated" })
}

// inputDesc is the description of o's create or update input.
func inputDesc(o *object, create bool) string {
	if create {
		return "The input of create" + o.name + "."
	}
	return "The input of update" + o.name + ": a field not given is left as it is."
}
