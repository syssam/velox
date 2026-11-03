package gogen

import (
	"encoding/json"
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
// CreateThingInput.slug, which the SDL has not. Create takes the row x then, whose
// other fields the resolver sets before the validators run, and its doc names the
// field; its errors name only the create input's fields, by thingCreateInputGQL,
// apart from the update input's thingUpdateInputGQL, which names slug.
func TestCreateSetsSkipped(t *testing.T) {
	files := render(t, setModel(t, true))
	_, funcs, expr := mutationDecls(t, files)
	if sig, want := expr(funcs["CreateThingInput.Create"].Type), "func(ctx context.Context, x *Thing) (*Thing, error)"; sig != want {
		t.Errorf("CreateThingInput.Create: %s, want %s", sig, want)
	}
	create := funcSrc(t, files, "CreateThingInput.Create")
	if !strings.Contains(create, `gqlimpl.NewCreate(ctx, "CreateThingInput", "gen.Thing", x, thingCreateInputGQL, Things.Name)`) {
		t.Errorf("Create does not name its own input's fields:\n%s", create)
	}
	for fn, want := range map[string]bool{"thingCreateInputGQL": false, "thingUpdateInputGQL": true} {
		if got := strings.Contains(funcSrc(t, files, fn), `case "Slug":`); got != want {
			t.Errorf("%s names Slug: %v, want %v", fn, got, want)
		}
	}
	if !strings.Contains(funcSrc(t, files, "UpdateThingInput.Update"), `"UpdateThingInput", "gen.Thing", id, thingUpdateInputGQL)`) {
		t.Error("Update does not name its own input's fields")
	}
	if doc := strings.Join(strings.Fields(funcs["CreateThingInput.Create"].Doc.Text()), " "); !strings.Contains(doc, "CreateThingInput leaves out Slug, which an insert requires: set it on x, or in a velox.Hook") {
		t.Errorf("Create's doc does not name slug: %q", doc)
	}

	files = render(t, setModel(t, false))
	_, funcs, _ = mutationDecls(t, files)
	if doc := funcs["CreateThingInput.Create"].Doc.Text(); strings.Contains(doc, "leaves out") {
		t.Errorf("with slug in the input, Create's doc names a field it leaves out: %q", doc)
	}
}
