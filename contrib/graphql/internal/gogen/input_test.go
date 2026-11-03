package gogen

import (
	"encoding/json"
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
// a plain assignment, is written with its gqlimpl helper, converting a key
// between the owner's Go type and the type the target's rows hold it in.
func TestInputEdgeKinds(t *testing.T) {
	files := render(t, petsModel())
	for name, wants := range map[string][]string{
		"CreateOwnerInput.Create": {
			// A null for a Nillable field with a Default is refused, as the
			// insert would store the Default.
			`gqlimpl.NotNull(c, "nick", in.Nick != nil) if in.Nick != nil {`,
			// A Nillable JSON field decodes, and the row takes its address.
			`metaJSON := gqlimpl.DecodeJSON[map[string]any](c, "meta", *in.Meta) x.Meta = &metaJSON c.Given(ownerColMeta)`,
			// A one-to-one edge whose key the target holds re-points one child.
			`if in.PetID != nil { petIDKey := gqlimpl.Key[int64](c, "petID", *in.PetID, "Pet") gqlimpl.Repoint(c, "petID", "Pet", ownerPetEdgeGQL(), []int64{petIDKey}) }`,
		},
		"UpdateOwnerInput.Update": {
			`u, key := gqlimpl.NewUpdateAs[int64, types.OwnerID](ctx, "UpdateOwnerInput", "Owner", "gen.Owner", velox.TableOf[Owner](), ownerColID, id, ownerInputGQL, gqlimpl.AsInt[types.OwnerID, int64])`,
			// The Pet's rows hold the owner's key as an int64.
			`gqlimpl.Child(u, "petID", "Pet", clearPet, petIDKey, int64(key), ownerPetEdgeGQL())`,
		},
		"ownerPetEdgeGQL": {
			`gqlimpl.Owned{Owner: velox.TableOf[Owner](), Target: velox.TableOf[Pet](), OwnerKey: ownerColID, TargetKey: petColID, FK: petColOwnerPet}`,
		},
		"CreatePetInput.Create": {
			`ownerIDVal := int64(ownerIDKey) x.OwnerPet = &ownerIDVal`,
			`momIDKey := gqlimpl.Key[int64](c, "momID", in.MomID, "Pet")`,
		},
		"UpdatePetInput.Update": {
			`u.SetKey(petColOwnerPet, int64(ownerIDKey))`,
			// A Required edge's NULL-able key can only be added to: no remove
			// or clear, which would leave a child with none.
			`addKitIDsKeys := gqlimpl.Keys[int64](u, "addKitIDs", in.AddKitIDs, "Pet")`,
			`gqlimpl.Children(u, "addKitIDs", "Pet", gqlimpl.Edit{Add: addKitIDsKeys}, key, petKitsEdgeGQL())`,
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
	if src := funcSrc(t, files, "UpdatePetInput.Update"); strings.Contains(src, "RemoveKitIDs") || strings.Contains(src, "ClearKits") {
		t.Errorf("UpdatePetInput.Update unlinks a kit:\n%s", src)
	}
}

// TestNonNodeOwnerM2M: a create input of a type that is no Node (its key is
// a float64) writes its many-to-many edge with a function literal for its
// key, of the join entity's two key types.
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
	for name, want := range map[string]string{
		"thingTagsEdgeGQL": `gqlimpl.M2M{Owner: velox.TableOf[Thing](), Target: velox.TableOf[Tag](), Join: velox.TableOf[ThingTag](), ` +
			`OwnerKey: thingColID, TargetKey: tagColID, JoinOwner: thingTagColThingID, JoinTarget: thingTagColTagID}`,
		"CreateThingInput.Create": `gqlimpl.Join(c, "tagIDs", "Tag", thingTagsEdgeGQL(), tagIDsKeys)`,
	} {
		if src := funcSrc(t, files, name); !strings.Contains(src, want) {
			t.Errorf("%s has no %s:\n%s", name, want, src)
		}
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
	handles := func(e *plugin.Entity, h string) *plugin.Entity { e.Handles = h; return e }
	// immutable is a field the create input sets and the update input does
	// not, so that the two name their fields apart.
	immutable := &plugin.Field{Name: "im", GoName: "Im", Type: "string", GoType: "string", ElemType: "string", Immutable: true}
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
		"the create input's name function is taken": {[]*plugin.Entity{withField(ent("Thing", `"create"`, `"update"`), immutable), handles(ent("Other"), "thingCreateInputGQL")},
			"schema Thing: its Go name thingCreateInputGQL is also velox-gen's handles of schema Other"},
		"the update input's name function is taken": {[]*plugin.Entity{withField(ent("Thing", `"create"`, `"update"`), immutable), handles(ent("Other"), "thingUpdateInputGQL")},
			"schema Thing: its Go name thingUpdateInputGQL is also velox-gen's handles of schema Other"},
		"an edge's function is taken": {withKids(ent("Thing", `"create"`), handles(ent("Kid"), "thingKidsEdgeGQL"), kidsKey("*int64", "int64", "", "")),
			"schema Thing, edge kids: its Go name thingKidsEdgeGQL is also velox-gen's handles of schema Kid"},
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
		"CreateThingInput.Create": "x.B = types.Blob(in.B)",
		"UpdateThingInput.Update": "u.Set(thingColB, types.Blob(*in.B))",
	} {
		if src := funcSrc(t, files, name); !strings.Contains(src, want) {
			t.Errorf("%s has no %s:\n%s", name, want, src)
		}
	}
}
