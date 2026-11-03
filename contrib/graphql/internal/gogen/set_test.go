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
// CreateThingInput.slug, which the SDL has not. Create<T> takes a set func(*T)
// then, which it calls on the entity after the input's values and before the
// validators, and its errors name only the create input's fields. An entity with
// no such field keeps Create<T>(ctx, input).
func TestCreateSetsSkipped(t *testing.T) {
	files := render(t, setModel(t, true))
	_, funcs, expr := mutationDecls(t, files)
	if sig, want := expr(funcs["CreateThing"].Type), "func(ctx context.Context, input CreateThingInput, set func(*Thing)) (*Thing, error)"; sig != want {
		t.Errorf("CreateThing: %s, want %s", sig, want)
	}
	create := strings.Join(strings.Fields(funcSrc(t, files, "CreateThing")), " ")
	set := strings.Index(create, "if set != nil { set(&x) }")
	if set < 0 || set > strings.Index(create, "velox.Validate(") || set < strings.Index(create, "x.Name = input.Name") {
		t.Errorf("CreateThing does not call set after the input and before the validators:\n%s", create)
	}
	if !strings.Contains(create, `gqlimpl.WriteError(err, "CreateThingInput", "gen.Thing", thingCreateFieldGQL)`) {
		t.Errorf("CreateThing does not name its own input's fields:\n%s", create)
	}
	for fn, want := range map[string]bool{"thingCreateFieldGQL": false, "thingUpdateFieldGQL": true} {
		if got := strings.Contains(funcSrc(t, files, fn), `case "Slug":`); got != want {
			t.Errorf("%s names Slug: %v, want %v", fn, got, want)
		}
	}
	if !strings.Contains(strings.Join(strings.Fields(funcSrc(t, files, "UpdateThing")), " "), `"UpdateThingInput", "gen.Thing", thingUpdateFieldGQL)`) {
		t.Error("UpdateThing does not name its own input's fields")
	}
	if src := strings.Join(strings.Fields(strings.ReplaceAll(files["thing_gql.go"], "\n//", "")), " "); !strings.Contains(src, " set sets Slug, which CreateThingInput leaves out and an insert requires; nil leaves it to a velox.Hook. func CreateThing(") {
		t.Error("CreateThing's doc does not name slug and set")
	}

	_, funcs, expr = mutationDecls(t, render(t, setModel(t, false)))
	if sig, want := expr(funcs["CreateThing"].Type), "func(ctx context.Context, input CreateThingInput) (*Thing, error)"; sig != want {
		t.Errorf("with slug in the input, CreateThing: %s, want %s", sig, want)
	}
}
