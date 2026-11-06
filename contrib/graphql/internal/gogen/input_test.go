package gogen

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox/plugin"
)

// mutable is the graphql annotation of an entity with the mutations muts,
// each quoted: `"create"`.
func mutable(muts ...string) map[string]json.RawMessage {
	return map[string]json.RawMessage{"graphql": json.RawMessage(`{"mutations":[` + strings.Join(muts, ",") + `]}`)}
}

// petsModel is an Owner, whose key is of the application's Go type, with a
// one-to-one edge pet whose key the Pet holds as a plain int64, a Nillable
// field with a Default and a Nillable JSON field; and a Pet with a Required
// edge mom to its own type, whose key is NULL-able, and its inverse kits. Both
// have create and update inputs.
func petsModel() *plugin.Model {
	owner := &plugin.Entity{
		Name: "Owner", Label: "owner", Handles: "Owners", PrimaryKey: []int{0}, Annotations: mutable(`"create"`, `"update"`),
		Fields: []*plugin.Field{
			{Name: "id", GoName: "ID", Type: "int64", GoType: "types.OwnerID", ElemType: "types.OwnerID", PkgPath: "example.com/types", Kind: "int64", AutoID: true},
			{Name: "nick", GoName: "Nick", Type: "string", GoType: "*string", ElemType: "string", Pointer: true, Nullable: true, Optional: true, HasDefault: true, Default: json.RawMessage(`"x"`)},
			{Name: "meta", GoName: "Meta", Type: "json", GoType: "*map[string]any", ElemType: "map[string]any", Pointer: true, Nullable: true, Optional: true},
		},
		Edges: []*plugin.Edge{{Name: "pet", GoName: "Pet", Target: "Pet", Ref: "owner", Kind: plugin.O2O, Unique: true, Field: "owner_pet", FieldOn: "Pet"}},
	}
	pet := &plugin.Entity{
		Name: "Pet", Label: "pet", Handles: "Pets", PrimaryKey: []int{0}, Annotations: mutable(`"create"`, `"update"`),
		Fields: []*plugin.Field{
			{Name: "id", GoName: "ID", Type: "int64", GoType: "int64", ElemType: "int64", AutoID: true},
			{Name: "owner_pet", GoName: "OwnerPet", Type: "int64", GoType: "*int64", ElemType: "int64", Pointer: true, Nullable: true, Optional: true, Implicit: "pet"},
			{Name: "cat_kits", GoName: "CatKits", Type: "int64", GoType: "*int64", ElemType: "int64", Pointer: true, Nullable: true, Optional: true, Implicit: "kits"},
		},
		Edges: []*plugin.Edge{
			{Name: "owner", GoName: "Owner", Target: "Owner", Ref: "pet", Kind: plugin.O2O, Unique: true, Inverse: true, Field: "owner_pet", FieldOn: "Pet", OwnsKey: true},
			{Name: "kits", GoName: "Kits", Target: "Pet", Ref: "mom", Kind: plugin.O2M, Field: "cat_kits", FieldOn: "Pet"},
			{Name: "mom", GoName: "Mom", Target: "Pet", Ref: "kits", Kind: plugin.M2O, Unique: true, Inverse: true, Required: true, Field: "cat_kits", FieldOn: "Pet", OwnsKey: true},
		},
	}
	return &plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{owner, pet}}
}

// TestInputEdgeKinds: each kind of edge, and each field that needs more than
// a plain setter, becomes its setter: the key on the row its column's Set,
// converted between the owner's Go type and the type the row holds it in;
// an edge whose key the target holds its relation handle's Add, Remove or
// Clear. A clear field wins over its field.
func TestInputEdgeKinds(t *testing.T) {
	files := render(t, petsModel())
	for name, wants := range map[string][]string{
		"CreateOwnerInput.Setters": {
			// A Nillable field with a Default is set when given; a null is a
			// field not given.
			`if v := in.Nick; v != nil { set = append(set, Owners.Nick.Set(*v)) }`,
			// A Nillable JSON field decodes, and its handle takes the value.
			`meta, err := gqlimpl.FromJSON[map[string]any]("CreateOwnerInput.meta", *v) if err != nil { return nil, err } set = append(set, Owners.Meta.Set(meta))`,
			// A one-to-one edge whose key the target holds is the edge's Add.
			`petID, err := gqlimpl.DecodeID[int64]("CreateOwnerInput.petID", *v, "Pet") if err != nil { return nil, err } set = append(set, Owners.Pet.Add(petID))`,
			`return gqlimpl.Checked("CreateOwnerInput", &ownerConnGQL, set)`,
		},
		"UpdateOwnerInput.Setters": {
			`if in.ClearNick { set = append(set, Owners.Nick.SetNull()) } else if v := in.Nick; v != nil {`,
			`if in.ClearPet { set = append(set, Owners.Pet.Clear()) } else if v := in.PetID; v != nil {`,
		},
		"CreatePetInput.Setters": {
			`ownerID, err := gqlimpl.DecodeIDAs[int64]("CreatePetInput.ownerID", *v, "Owner", gqlimpl.AsInt[types.OwnerID, int64])`,
			// The row holds the owner's key as an int64.
			`set = append(set, Pets.OwnerPet.Set(int64(ownerID)))`,
			`set = append(set, Pets.Kits.Add(kitIDs...))`,
			// A required ID is always decoded.
			`momID, err := gqlimpl.DecodeID[int64]("CreatePetInput.momID", in.MomID, "Pet") if err != nil { return nil, err } set = append(set, Pets.CatKits.Set(momID))`,
		},
		"UpdatePetInput.Setters": {
			`if in.ClearOwner { set = append(set, Pets.OwnerPet.SetNull()) } else if v := in.OwnerID; v != nil {`,
			// A Required edge's NULL-able key can only be added to: no remove
			// or clear, which would leave a child with none.
			`addKitIDs, err := gqlimpl.DecodeIDs[int64]("UpdatePetInput.addKitIDs", in.AddKitIDs, "Pet") if err != nil { return nil, err } set = append(set, Pets.Kits.Add(addKitIDs...))`,
		},
	} {
		src := funcSrc(t, files, name)
		if src == "" {
			t.Errorf("no %s", name)
			continue
		}
		for _, want := range wants {
			if !strings.Contains(src, want) {
				t.Errorf("%s has no %s:\n%s", name, want, src)
			}
		}
	}
	if src := funcSrc(t, files, "UpdatePetInput.Setters"); strings.Contains(src, "RemoveKitIDs") || strings.Contains(src, "ClearKits") {
		t.Errorf("UpdatePetInput.Setters unlinks a kit:\n%s", src)
	}
}

// TestNonNodeOwnerM2M: a create input of a type that is no Node (its key is
// a float64) writes its many-to-many edge with the edge's Add, which the
// write runs with the row's key.
func TestNonNodeOwnerM2M(t *testing.T) {
	id := func(typ string) *plugin.Field {
		return &plugin.Field{Name: "id", GoName: "ID", Type: typ, GoType: typ, ElemType: typ}
	}
	m := &plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{
		{
			Name: "Thing", Label: "thing", Handles: "Things", PrimaryKey: []int{0}, Annotations: mutable(`"create"`),
			Fields: []*plugin.Field{id("float64")},
			Edges:  []*plugin.Edge{{Name: "tags", GoName: "Tags", Target: "Tag", Kind: plugin.M2M, Join: "ThingTag"}},
		},
		{Name: "Tag", Label: "tag", Handles: "Tags", PrimaryKey: []int{0}, Fields: []*plugin.Field{id("int64")}, Edges: []*plugin.Edge{}},
		{
			Name: "ThingTag", Label: "thing_tag", Handles: "ThingTags", PrimaryKey: []int{0, 1}, Edges: []*plugin.Edge{},
			Fields: []*plugin.Field{
				{Name: "thing_id", GoName: "ThingID", Type: "float64", GoType: "float64", ElemType: "float64"},
				{Name: "tag_id", GoName: "TagID", Type: "int64", GoType: "int64", ElemType: "int64"},
			},
		},
	}}
	files := render(t, m)
	want := `tagIDs, err := gqlimpl.DecodeIDs[int64]("CreateThingInput.tagIDs", in.TagIDs, "Tag") if err != nil { return nil, err } set = append(set, Things.Tags.Add(tagIDs...))`
	if src := funcSrc(t, files, "CreateThingInput.Setters"); !strings.Contains(src, want) {
		t.Errorf("CreateThingInput.Setters has no %s:\n%s", want, src)
	}
}

// TestInputNamesApart: when the create input sets a column the update input
// does not, an immutable one, the Conn names each input's fields apart, in
// CreateInputs and UpdateInputs; when the two set the same, in Inputs.
func TestInputNamesApart(t *testing.T) {
	thing := func(fs ...*plugin.Field) *plugin.Model {
		return &plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{{
			Name: "Thing", Label: "thing", Handles: "Things", PrimaryKey: []int{0}, Edges: []*plugin.Edge{}, Annotations: mutable(`"create"`, `"update"`),
			Fields: append([]*plugin.Field{
				{Name: "id", GoName: "ID", Type: "int64", GoType: "int64", ElemType: "int64", AutoID: true},
				{Name: "x", GoName: "X", Type: "string", GoType: "string", ElemType: "string"},
			}, fs...),
		}}}
	}
	lit := connLit(t, render(t, thing(&plugin.Field{Name: "im", GoName: "Im", Type: "string", GoType: "string", ElemType: "string", Immutable: true})), "thingConnGQL")
	if want := []string{`thingColX: "x"`, `thingColIm: "im"`}; !slices.Equal(lit["CreateInputs"], want) {
		t.Errorf("CreateInputs %v, want %v", lit["CreateInputs"], want)
	}
	if want := []string{`thingColX: "x"`}; !slices.Equal(lit["UpdateInputs"], want) || lit["Inputs"] != nil {
		t.Errorf("UpdateInputs %v and Inputs %v, want %v and none", lit["UpdateInputs"], lit["Inputs"], want)
	}
	lit = connLit(t, render(t, thing()), "thingConnGQL")
	if want := []string{`thingColX: "x"`}; !slices.Equal(lit["Inputs"], want) || lit["CreateInputs"] != nil || lit["UpdateInputs"] != nil {
		t.Errorf("Inputs %v, CreateInputs %v, UpdateInputs %v; want %v alone", lit["Inputs"], lit["CreateInputs"], lit["UpdateInputs"], want)
	}
}

// TestInputRenderErrors: a mutation input the generated package cannot
// declare, or whose field or key cannot be converted, fails the render,
// naming the schema and the field or the Go name.
func TestInputRenderErrors(t *testing.T) {
	ent := func(name string, muts ...string) *plugin.Entity {
		return &plugin.Entity{
			Name: name, Label: strings.ToLower(name), Handles: name + "s", PrimaryKey: []int{0}, Edges: []*plugin.Edge{}, Annotations: mutable(muts...),
			Fields: []*plugin.Field{
				{Name: "id", GoName: "ID", Type: "int64", GoType: "int64", ElemType: "int64", AutoID: true},
				{Name: "x", GoName: "X", Type: "string", GoType: "string", ElemType: "string"},
			},
		}
	}
	// withKids is e with the edge kids, whose key thing_kids, fk, a Kid holds.
	withKids := func(e *plugin.Entity, kid *plugin.Entity, fk *plugin.Field) []*plugin.Entity {
		e.Edges = []*plugin.Edge{{Name: "kids", GoName: "Kids", Target: "Kid", Kind: plugin.O2M, Field: "thing_kids", FieldOn: "Kid"}}
		return []*plugin.Entity{e, withField(kid, fk)}
	}
	kidsKey := func(goType, elem, pkg, kind string) *plugin.Field {
		return &plugin.Field{Name: "thing_kids", GoName: "ThingKids", Type: "int64", GoType: goType, ElemType: elem, PkgPath: pkg, Kind: kind, Pointer: true, Nullable: true, Optional: true, Implicit: "kids"}
	}
	for name, c := range map[string]struct {
		es   []*plugin.Entity
		want string
	}{
		"a child's key that does not convert": {withKids(ent("Thing", `"create"`), ent("Kid"), kidsKey("*types.Tag", "types.Tag", "example.com/types", "string")),
			"schema Kid, field thing_kids: velox-graphql converts its Go type types.Tag to int64, and a string does not convert"},
		"a JSON field of no Go type, on create": {[]*plugin.Entity{withField(ent("Thing", `"create"`), &plugin.Field{Name: "j", GoName: "J", Type: "json"})},
			"schema Thing, field j: the model gives no Go type for the JSON field"},
		"a JSON field of no Go type, on update": {[]*plugin.Entity{withField(ent("Thing", `"update"`), &plugin.Field{Name: "j", GoName: "J", Type: "json"})},
			"schema Thing, field j: the model gives no Go type for the JSON field"},
		"a time of another Go type, of no kind": {[]*plugin.Entity{withField(ent("Thing", `"create"`),
			&plugin.Field{Name: "at", GoName: "At", Type: "time", GoType: "types.Stamp", ElemType: "types.Stamp", PkgPath: "example.com/types"})},
			"schema Thing, field at: velox-graphql converts a time field only from time.Time, and its Go type is types.Stamp; give the field time.Time"},
		"a string of an int Go type, on create": {[]*plugin.Entity{withField(ent("Thing", `"create"`),
			&plugin.Field{Name: "s", GoName: "S", Type: "string", GoType: "types.S", ElemType: "types.S", PkgPath: "example.com/types", Kind: "int"})},
			"schema Thing, field s: velox-graphql converts its Go type types.S to string, and an int does not convert"},
		"a string of an int Go type, on update": {[]*plugin.Entity{withField(ent("Thing", `"update"`),
			&plugin.Field{Name: "s", GoName: "S", Type: "string", GoType: "types.S", ElemType: "types.S", PkgPath: "example.com/types", Kind: "int"})},
			"schema Thing, field s: velox-graphql converts its Go type types.S to string, and an int does not convert"},
		"a nullable column whose Go type holds no nil": {[]*plugin.Entity{withField(ent("Thing", `"update"`),
			&plugin.Field{Name: "n", GoName: "N", Type: "int64", GoType: "int64", ElemType: "int64", Kind: "int64", Nullable: true, Optional: true})},
			"schema Thing, field n: the model says its column holds NULL, and its Go type int64 holds no nil; make the field Nillable"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Render(&plugin.Model{Version: plugin.ModelVersion, Entities: c.es}, outPath, "gen"); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err %v, want one with %q", err, c.want)
			}
		})
	}
}

// TestBytesOfANamedType: a bytes field of the application's Go type converts
// from the input's []byte, on create and on update.
func TestBytesOfANamedType(t *testing.T) {
	m := &plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{{
		Name: "Thing", Label: "thing", Handles: "Things", PrimaryKey: []int{0}, Edges: []*plugin.Edge{}, Annotations: mutable(`"create"`, `"update"`),
		Fields: []*plugin.Field{
			{Name: "id", GoName: "ID", Type: "int64", GoType: "int64", ElemType: "int64", AutoID: true},
			{Name: "b", GoName: "B", Type: "bytes", GoType: "types.Blob", ElemType: "types.Blob", PkgPath: "example.com/types", Kind: "slice"},
		},
	}}}
	files := render(t, m)
	for name, want := range map[string]string{
		"CreateThingInput.Setters": "set = append(set, Things.B.Set(types.Blob(in.B)))",
		"UpdateThingInput.Setters": "set = append(set, Things.B.Set(types.Blob(*v)))",
	} {
		if src := funcSrc(t, files, name); !strings.Contains(src, want) {
			t.Errorf("%s has no %s:\n%s", name, want, src)
		}
	}
}

// TestSetterLocals: a decoded field's local is named after the field, unless
// that is a keyword, a predeclared name or a package the file imports, when
// it ends in Val.
func TestSetterLocals(t *testing.T) {
	m := &plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{{
		Name: "Thing", Label: "thing", Handles: "Things", PrimaryKey: []int{0}, Edges: []*plugin.Edge{}, Annotations: mutable(`"create"`),
		Fields: []*plugin.Field{
			{Name: "id", GoName: "ID", Type: "int64", GoType: "int64", ElemType: "int64", AutoID: true},
			{Name: "type", GoName: "Type", Type: "json", GoType: "map[string]any", ElemType: "map[string]any", Kind: "map"},
			{Name: "len", GoName: "Len", Type: "json", GoType: "map[string]any", ElemType: "map[string]any", Kind: "map"},
			{Name: "types", GoName: "Types", Type: "json", GoType: "types.Bag", ElemType: "types.Bag", PkgPath: "example.com/types", Kind: "map"},
			{Name: "meta", GoName: "Meta", Type: "json", GoType: "map[string]any", ElemType: "map[string]any", Kind: "map"},
		},
	}}}
	src := funcSrc(t, render(t, m), "CreateThingInput.Setters")
	for _, want := range []string{"typeVal, err :=", "lenVal, err :=", "typesVal, err :=", "meta, err :="} {
		if !strings.Contains(src, want) {
			t.Errorf("CreateThingInput.Setters has no %s:\n%s", want, src)
		}
	}
}
