package sdl

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/contrib/graphql/internal/english"
	"github.com/syssam/velox/plugin"
)

// mutationsOf computes o's mutation inputs, CreateXInput and UpdateXInput,
// for the kinds its Mutations annotation lists. The application declares the
// mutations that take them in its own schema.
func (g *graph) mutationsOf(o *object) error {
	e := o.ent
	where := "schema " + e.Name
	var create, update bool
	for _, k := range o.ann.Mutations {
		switch k {
		case graphql.MutationCreate:
			create = true
		case graphql.MutationUpdate:
			update = true
		default:
			return fmt.Errorf("%s: graphql.Mutations: %q is not a mutation input; the inputs are create and update", where, string(k))
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
	if update && !o.hasID {
		return fmt.Errorf("%s: graphql.Mutations lists update, which takes an ID, and %s: it has no ID", where, noID(e))
	}

	if !create {
		for i, f := range e.Fields {
			if o.fanns[i].CreateDefault != nil {
				return fmt.Errorf("%s, field %s: graphql.CreateDefault, and graphql.Mutations does not list create", where, f.Name)
			}
		}
	}
	if create && o.hasID {
		pk := e.Fields[e.PrimaryKey[0]]
		if !pk.AutoID && !pk.HasDefault {
			return fmt.Errorf("%s: graphql.Mutations lists create, and its key %s is supplied by the caller (no auto ID, no default): velox-graphql writes no create input for such a schema, as the key is never an input", where, pk.Name)
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
	}
	if update {
		// An update input with no fields gets no type.
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
		}
	}
	return nil
}

// inputFields are the fields of o's create input, or its update input:
//   - neither has the primary key or a foreign key; the edges
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
//   - an edge to a type that has no ID is in neither;
//   - an edge's inputs follow its foreign key's flags, on whichever entity
//     holds it (see edgeInputs);
//   - a field's inputs carry its directives, and an edge's its directives,
//     as the where input's do: a directive that guards a value guards its
//     writes too.
//
// It returns the fields for the SDL and, in the same order, for the Go code.
func (g *graph) inputFields(o *object, typ string, create bool) ([]gqlField, []InputField, error) {
	e := o.ent
	skip := graphql.SkipMutationUpdateInput
	if create {
		skip = graphql.SkipMutationCreateInput
	}
	fs := newInputFields(typ)
	var gos []InputField
	for i := range e.Fields {
		add, err := g.fieldInputs(o, fs, i, create, skip)
		if err != nil {
			return nil, nil, err
		}
		gos = append(gos, add...)
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

// fieldInputs adds to fs the input fields of o's field i, on create or on
// update, and returns them for the Go code. skip is the Skip flag that
// leaves a field out of this input.
func (g *graph) fieldInputs(o *object, fs *inputFields, i int, create bool, skip graphql.SkipMode) ([]InputField, error) {
	e, f, a := o.ent, o.ent.Fields[i], o.fanns[i]
	if create && a.CreateDefault != nil && a.Skip&skip != 0 {
		return nil, fmt.Errorf("schema %s, field %s: graphql.CreateDefault on a field the create input leaves out", e.Name, f.Name)
	}
	switch {
	case o.hasID && i == e.PrimaryKey[0], f.Implicit != "", o.fks[f.Name], a.Skip&skip != 0, f.Version, !create && f.Immutable:
		return nil, nil
	}
	where := "schema " + e.Name + ", field " + f.Name
	if !inInput(f, a, create) {
		if create && a.CreateDefault != nil {
			return nil, fmt.Errorf("%s: graphql.CreateDefault on a field the create input leaves out", where)
		}
		return nil, nil
	}
	name, err := o.fieldName(i)
	if err != nil {
		return nil, err
	}
	t, def, err := g.inputType(o, i, create, where)
	if err != nil {
		return nil, err
	}
	dirs, err := directives(a.Directives, where)
	if err != nil {
		return nil, err
	}
	if o.veloxe {
		dirs = nil // veloxe put directives on the type's fields alone
	}
	if err := fs.add(gqlField{name: name, typ: t, def: def, desc: f.Comment, dirs: inputDirs(dirs, t), where: where}); err != nil {
		return nil, err
	}
	v := g.goValue(f, a)
	if a.Type != "" && strings.TrimSuffix(t, "!") == "JSON" && a.Type != "JSON" {
		v = g.goValue(f, graphql.Annotation{}) // inputType's JSON for an output type
	}
	if f.Type == "json" && !v.own {
		v.typ, v.pkg = v.getter, v.getterPkg
	}
	goName := goOf(name, f.GoName)
	gos := []InputField{{
		Name: name, GoName: goName, Type: t, Kind: InputValue, Field: i, Edge: -1,
		GoType: v.typ, GoPkg: v.pkg, Required: strings.HasSuffix(t, "!"),
	}}
	if create || !f.Nullable {
		return gos, nil
	}
	cl := gqlField{name: "clear" + upper(name), typ: "Boolean", desc: "Sets the field " + f.Name + " to NULL.", dirs: dirs, where: where}
	if err := fs.add(cl); err != nil {
		return nil, err
	}
	return append(gos, InputField{Name: cl.name, GoName: "Clear" + goName, Type: cl.typ, Kind: InputClear, Field: i, Edge: -1}), nil
}

// inInput reports whether the field f, annotated a, is in the create or
// the update input. CreateInput or UpdateInput decides when it is set.
//
// An UpdateDefault fills the field on update only: on create it is left
// out, unless nothing else fills it, which is a field that is not nullable
// and has no Default.
func inInput(f *plugin.Field, a graphql.Annotation, create bool) bool {
	explicit := a.UpdateInput
	if create {
		explicit = a.CreateInput
	}
	if explicit != nil {
		return *explicit
	}
	required := !f.Optional && !f.Nullable
	return !f.UpdateDefault || f.HasDefault || create && required
}

// inputType returns the GraphQL type of o's field i in the create or the
// update input, and its default, as " = true", or "" for none.
func (g *graph) inputType(o *object, i int, create bool, where string) (string, string, error) {
	f, a := o.ent.Fields[i], o.fanns[i]
	t, err := g.fieldType(o, f, a, where)
	if err != nil {
		return "", "", err
	}
	if a.Type != "" && f.Type == "json" && !g.inputable(t) {
		// A json field's object type, which no input takes: veloxe's JSON.
		// Any other field's column holds a scalar, which an input takes.
		t = "JSON"
	}
	if !create {
		return t, "", nil
	}
	var def string
	if a.CreateDefault != nil {
		if def, err = createDefault(f, a.CreateDefault, where); err != nil {
			return "", "", err
		}
		t += "!" // a value in Go: the default fills it
	}
	required := !f.Optional && !f.Nullable
	if required && !f.HasDefault && def == "" {
		t += "!"
	}
	return t, def, nil
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
//   - an edge to a type that has no ID has no input. When that
//     edge is required and holds its key on o's row, the create input could never
//     succeed: an error.
//
// Each input carries the edge's directives, after those of its key when the
// key is on o's row.
func (g *graph) edgeInputs(o *object, i int, create bool) ([]edgeInput, error) {
	ed := o.ent.Edges[i]
	t := g.byName[ed.Target]
	where := "schema " + o.ent.Name + ", edge " + ed.Name
	if t == nil || !t.hasID {
		return nil, unsettableEdge(o, ed, t, create, where)
	}
	name, err := o.edgeName(i)
	if err != nil {
		return nil, err
	}
	fk := g.keyField(ed)
	immutable := ed.Immutable || fk != nil && fk.Immutable
	nullable := fk == nil || fk.Nullable
	dirs, err := edgeInputDirs(o, i, where)
	if err != nil {
		return nil, err
	}
	if o.veloxe {
		dirs = nil // veloxe put directives on the type's fields alone
	}
	goEdge := goOf(name, ed.GoName)
	one, many, goOne := edgeInputNames(name, ed)
	var out []edgeInput
	add := func(name, typ, goName string, kind InputKind) {
		goType, goPkg := t.keyTyp, t.keyPkg
		if kind == InputClearEdge {
			goType, goPkg = "", ""
		}
		out = append(out, edgeInput{
			name: name, typ: typ, desc: edgeInputDesc(kind, ed, t), dirs: inputDirs(dirs, typ), where: where,
			goField: InputField{Name: name, GoName: goName, Type: typ, Kind: kind, Field: -1, Edge: i, Target: t.name, Required: strings.HasSuffix(typ, "!"),
				GoType: goType, GoPkg: goPkg},
		})
	}
	idName := name + "ID"
	if o.veloxe && ed.Field != "" && ed.FieldOn == o.ent.Name {
		// veloxe's: the input takes the key field the edge is bound to.
		idName = veloxeCamel(ed.Field)
	}
	edgeID := func(typ string) { add(idName, typ, goEdge+"ID", InputEdgeID) }
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

// unsettableEdge returns the error of a required edge ed of o whose key is
// on o's row and whose target t is skipped (nil) or has no ID, on create.
// It returns nil for any other such edge, which has no input.
func unsettableEdge(o *object, ed *plugin.Edge, t *object, create bool, where string) error {
	// A required key on o's own row that the create input could never
	// set. When the target's row holds the key, it writes none of its own.
	if !create || !ed.Unique || !ed.Required || !ed.OwnsKey {
		return nil
	}
	why := "is skipped"
	if t != nil {
		why = "has no ID"
	}
	return fmt.Errorf("%s: a required edge whose target %s %s: Create%sInput could never set it; skip the edge's create input with graphql.Skip(graphql.SkipMutationCreateInput), and set its key on the x the resolver gives Create%[4]sInput.Create, or in a hook", where, ed.Target, why, o.name)
}

// edgeInputDirs returns the directives the inputs of o's edge i carry: its
// key's, when the key is on o's row, then the edge's own.
func edgeInputDirs(o *object, i int, where string) ([]directive, error) {
	ed := o.ent.Edges[i]
	dirs, err := directives(o.eanns[i].Directives, where)
	if err != nil {
		return nil, err
	}
	// A key on o's row brings its own directives first, as to its where
	// predicates.
	if !ed.OwnsKey || ed.FieldOn != o.ent.Name {
		return dirs, nil
	}
	k := slices.IndexFunc(o.ent.Fields, func(f *plugin.Field) bool { return f.Name == ed.Field })
	if k < 0 {
		return dirs, nil
	}
	kds, err := directives(o.fanns[k].Directives, "schema "+o.ent.Name+", field "+ed.Field)
	if err != nil {
		return nil, err
	}
	return unite(kds, dirs), nil
}

// edgeInputNames returns the parts of the input names of the edge ed, whose
// GraphQL name is name: the singular for an add or remove input (one), for
// a create input (many), and for their Go names (goOne).
//
// The Go names follow the GraphQL names' parts: the edge's Go name, when
// the GraphQL name was made from it, and its singular. The singular is
// ent's (MutationAdd), of the lowercase name, never of a capitalised one:
// the edge name's for an edge the GraphQL name was made from, so children
// gives childIDs and ChildIDs; a renamed edge's GraphQL name's, so a
// rename to people gives addPersonIDs and AddPersonIDs.
func edgeInputNames(name string, ed *plugin.Edge) (one, many, goOne string) {
	one, many = upper(plugin.Singular(name)), plugin.Singular(name)
	goOne = one
	if strings.EqualFold(camel(ed.GoName), name) || veloxeCamel(ed.Name) == name {
		goOne = plugin.GoName(plugin.Singular(ed.Name))
		many = camel(goOne)
		one = upper(many)
	}
	return one, many, goOne
}

// edgeInputDesc returns the description of an input of the edge ed to t of
// the given kind. It says what the Go doc of the input's field says.
func edgeInputDesc(kind InputKind, ed *plugin.Edge, t *object) string {
	switch kind {
	case InputEdgeID:
		return fmt.Sprintf("The ID of the %s the edge %s leads to.", t.name, ed.Name)
	case InputEdgeIDs:
		return fmt.Sprintf("The IDs of the %s nodes the edge %s leads to.", t.name, ed.Name)
	case InputAddIDs:
		return fmt.Sprintf("The IDs of %s nodes to add to the edge %s.", t.name, ed.Name)
	case InputRemoveIDs:
		return fmt.Sprintf("The IDs of %s nodes to remove from the edge %s; an ID the edge does not lead to is ignored.", t.name, ed.Name)
	case InputClearEdge:
		if ed.Unique {
			return fmt.Sprintf("Unlinks the edge %s: it leads to no %s afterwards.", ed.Name, t.name)
		}
		return fmt.Sprintf("Removes every %s from the edge %s, before the IDs to remove and to add.", t.name, ed.Name)
	}
	return ""
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
// bool or number with no Default that is not nullable: the create input's
// default, where the schema's insert has none.
func createDefault(f *plugin.Field, d json.RawMessage, where string) (string, error) {
	switch {
	case !zeroIsUnset(f.Type):
		return "", fmt.Errorf("%s: graphql.CreateDefault on a field of type %s; it is for a bool or number: give this field a Default", where, f.Type)
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
		return "The input that creates " + english.Article(o.name) + " " + o.name + "."
	}
	return "The input that updates " + english.Article(o.name) + " " + o.name + ": a field not given is left as it is."
}

// SkipsCreate reports whether f's graphql annotation leaves it out of the
// create input explicitly, with Skip(SkipMutationCreateInput) or
// CreateInput(false): the application, or a hook, sets it. An annotation
// that does not decode, which Render refuses, skips nothing.
func SkipsCreate(f *plugin.Field) bool {
	a, err := decode(f.Annotations, "")
	if err != nil {
		return false
	}
	return a.Skip&graphql.SkipMutationCreateInput != 0 || a.CreateInput != nil && !*a.CreateInput
}
