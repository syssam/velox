package gogen

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/plugin"
)

// setModel is Thing, with createX and updateX: name, in both inputs; slug,
// required with no Default and left out of the create input, which entgql users
// set in the resolver; and note, Optional and left out of it too, which unset
// leaves "" as the schema says.
func setModel(t *testing.T, skipSlug bool) *plugin.Model {
	t.Helper()
	ann := func(a graphql.Annotation) map[string]json.RawMessage {
		b, err := json.Marshal(a)
		if err != nil {
			t.Fatal(err)
		}
		return map[string]json.RawMessage{"graphql": b}
	}
	str := func(name string) *plugin.Field {
		return &plugin.Field{Name: name, GoName: plugin.GoName(name), Type: "string", GoType: "string", ElemType: "string"}
	}
	slug, note := str("slug"), str("note")
	if skipSlug {
		slug.Annotations = ann(graphql.Skip(graphql.SkipMutationCreateInput))
	}
	note.Optional = true
	note.Annotations = ann(graphql.Skip(graphql.SkipMutationCreateInput))
	return &plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{{
		Name: "Thing", Label: "thing", Handles: "Things", PrimaryKey: []int{0}, Edges: []*plugin.Edge{},
		Annotations: ann(graphql.Mutations(graphql.MutationCreate, graphql.MutationUpdate)),
		Fields: []*plugin.Field{
			{Name: "id", GoName: "ID", Type: "int64", GoType: "int64", ElemType: "int64", AutoID: true},
			str("name"), slug, note,
		},
	}}}
}

// TestCreateSetsSkipped: a required field with no Default that the create input
// leaves out stored its zero value silently, and a validator's error on it named
// CreateThingInput.slug, which the SDL has not. Setters's setters go to the
// resolver's insert of x, whose other fields the resolver sets, and its doc
// names the field; its errors name only the create input's fields, by the Conn's
// CreateInputs, apart from the update input's UpdateInputs, which names slug. Its doc
// does not name slug: a field the schema leaves out of the create input
// explicitly is the application's or a hook's to set, as a tenancy mixin's
// hook sets its column.
func TestCreateSetsSkipped(t *testing.T) {
	files := render(t, setModel(t, true))
	_, funcs, expr := mutationDecls(t, files)
	if sig, want := expr(funcs["CreateThingInput.Setters"].Type), "func() ([]velox.Setter[Thing], error)"; sig != want {
		t.Errorf("CreateThingInput.Setters: %s, want %s", sig, want)
	}
	create := funcSrc(t, files, "CreateThingInput.Setters")
	if !strings.Contains(create, `return gqlimpl.Checked("CreateThingInput", &thingConnGQL, set)`) {
		t.Errorf("Setters does not take thingConnGQL:\n%s", create)
	}
	lit := connLit(t, files, "thingConnGQL")
	for field, want := range map[string]bool{"CreateInputs": false, "UpdateInputs": true} {
		if got := slices.Contains(lit[field], `thingColSlug: "slug"`); got != want {
			t.Errorf("thingConnGQL's %s names slug: %v, want %v (%v)", field, got, want, lit)
		}
	}
	if !strings.Contains(funcSrc(t, files, "UpdateThingInput.Setters"), `return gqlimpl.Checked("UpdateThingInput", &thingConnGQL, set)`) {
		t.Error("UpdateThingInput.Setters does not take thingConnGQL")
	}
	if doc := funcs["CreateThingInput.Setters"].Doc.Text(); strings.Contains(doc, "leaves out") {
		t.Errorf("Setters's doc names slug, which the schema skips explicitly: %q", doc)
	}

	files = render(t, setModel(t, false))
	_, funcs, _ = mutationDecls(t, files)
	if doc := funcs["CreateThingInput.Setters"].Doc.Text(); strings.Contains(doc, "leaves out") {
		t.Errorf("with slug in the input, Setters's doc names a field it leaves out: %q", doc)
	}
}

// TestCreateDocLeftOutKey: Create's doc, now Setters's, said a key the create input leaves
// out "is stored as the zero value", but the zero key names no row, and the
// database's foreign key refuses the insert. The doc names the edge and the
// error.
func TestCreateDocLeftOutKey(t *testing.T) {
	m := setModel(t, false)
	thing := m.Entities[0]
	thing.Fields = append(thing.Fields, &plugin.Field{Name: "owner_id", GoName: "OwnerID", Type: "int64", GoType: "int64", ElemType: "int64"})
	skip, err := json.Marshal(graphql.Skip(graphql.SkipMutationCreateInput))
	if err != nil {
		t.Fatal(err)
	}
	thing.Edges = append(thing.Edges, &plugin.Edge{
		Name: "owner", GoName: "Owner", Target: "User", Ref: "things", Kind: plugin.M2O, Unique: true, Required: true,
		Inverse: true, Field: "owner_id", FieldOn: "Thing", OwnsKey: true,
		Annotations: map[string]json.RawMessage{"graphql": skip},
	})
	m.Entities = append(m.Entities, &plugin.Entity{
		Name: "User", Label: "user", Handles: "Users", PrimaryKey: []int{0},
		Fields: []*plugin.Field{{Name: "id", GoName: "ID", Type: "int64", GoType: "int64", ElemType: "int64", AutoID: true}},
		Edges:  []*plugin.Edge{{Name: "things", GoName: "Things", Target: "Thing", Ref: "owner", Kind: plugin.O2M, Field: "owner_id", FieldOn: "Thing"}},
	})
	_, funcs, _ := mutationDecls(t, render(t, m))
	doc := strings.Join(strings.Fields(funcs["CreateThingInput.Setters"].Doc.Text()), " ")
	for _, want := range []string{
		"leaves out OwnerID, the key of the edge owner: set it on x, or in a velox.Hook; a zero key names no row, and the database's foreign key refuses the insert with a velox.ConstraintError.",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("Setters's doc does not say %q:\n%s", want, doc)
		}
	}
}
