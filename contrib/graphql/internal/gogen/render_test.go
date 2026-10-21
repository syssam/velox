package gogen

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	gqlast "github.com/vektah/gqlparser/v2/ast"

	"github.com/syssam/velox2/contrib/graphql"
	"github.com/syssam/velox2/contrib/graphql/internal/sdl"
	"github.com/syssam/velox2/plugin"
)

var update = flag.Bool("update", false, "rewrite the golden Go files")

const outPath = "example.com/app/gen"

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// testModel is plan 1's testdata model, whose annotations cover every
// annotation.
func testModel(t *testing.T) *plugin.Model {
	t.Helper()
	var m plugin.Model
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join("..", "sdl", "testdata", "model.json"))), &m); err != nil {
		t.Fatal(err)
	}
	return &m
}

func render(t *testing.T, m *plugin.Model) map[string]string {
	t.Helper()
	files, err := Render(m, outPath, "gen")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, f := range files {
		if _, dup := out[f.Path]; dup {
			t.Fatalf("two files %s", f.Path)
		}
		out[f.Path] = f.Content
	}
	return out
}

// TestGoGolden pins the Go of the testdata model. Run it with -update to
// rewrite testdata/golden.
func TestGoGolden(t *testing.T) {
	got := render(t, testModel(t))
	dir := filepath.Join("testdata", "golden")
	if *update {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, src := range got {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, e := range entries {
		want = append(want, e.Name())
	}
	names := make([]string, 0, len(got))
	for n := range got {
		names = append(names, n)
	}
	slices.Sort(names)
	if !slices.Equal(names, want) {
		t.Fatalf("files %v, want %v (run go test -update to accept)", names, want)
	}
	for _, n := range names {
		w := readFile(t, filepath.Join(dir, n))
		if got[n] == w {
			continue
		}
		gl, wl := strings.Split(got[n], "\n"), strings.Split(w, "\n")
		for i := range min(len(gl), len(wl)) {
			if gl[i] != wl[i] {
				t.Fatalf("%s line %d:\n got %q\nwant %q\n(run go test -update to accept)", n, i+1, gl[i], wl[i])
			}
		}
		t.Fatalf("%s: got %d lines, want %d (run go test -update to accept)", n, len(gl), len(wl))
	}
	if _, ok := got["gql_session.go"]; ok {
		t.Error("Session's type is skipped, and it has a Go file")
	}
	if _, ok := got["gql_user_group.go"]; ok {
		t.Error("a join entity has a Go file")
	}
}

// TestGoDeterministic renders the model twice.
func TestGoDeterministic(t *testing.T) {
	a, b := render(t, testModel(t)), render(t, testModel(t))
	for n := range a {
		if a[n] != b[n] {
			t.Errorf("%s differs between two runs", n)
		}
	}
}

// goDecls are the top-level names the files declare: types, with their kind
// and fields or methods, functions and constants.
type goDecls struct {
	types  map[string]*goType
	funcs  map[string]bool
	consts map[string]bool
}

type goType struct {
	alias   string   // the aliased type, for an alias
	kind    string   // "struct", "string"
	fields  []string // a struct's field names
	methods []string
}

func parseGo(t *testing.T, files map[string]string) goDecls {
	t.Helper()
	d := goDecls{types: map[string]*goType{}, funcs: map[string]bool{}, consts: map[string]bool{}}
	fset := token.NewFileSet()
	methods := map[string][]string{}
	for name, src := range files {
		f, err := parser.ParseFile(fset, name, src, parser.ParseComments)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, decl := range f.Decls {
			switch decl := decl.(type) {
			case *ast.FuncDecl:
				if decl.Recv == nil {
					d.funcs[decl.Name.Name] = true
					continue
				}
				rt := decl.Recv.List[0].Type
				if s, ok := rt.(*ast.StarExpr); ok {
					rt = s.X
				}
				recv := rt.(*ast.Ident).Name
				methods[recv] = append(methods[recv], decl.Name.Name)
			case *ast.GenDecl:
				for _, spec := range decl.Specs {
					switch spec := spec.(type) {
					case *ast.TypeSpec:
						gt := &goType{}
						switch typ := spec.Type.(type) {
						case *ast.StructType:
							gt.kind = "struct"
							for _, fl := range typ.Fields.List {
								for _, n := range fl.Names {
									gt.fields = append(gt.fields, n.Name)
								}
							}
						case *ast.Ident:
							gt.kind = typ.Name
						}
						if spec.Assign.IsValid() {
							gt.alias = spec.Type.(*ast.Ident).Name
						}
						if _, dup := d.types[spec.Name.Name]; dup {
							t.Errorf("the type %s is declared twice", spec.Name.Name)
						}
						d.types[spec.Name.Name] = gt
					case *ast.ValueSpec:
						for _, n := range spec.Names {
							d.consts[n.Name] = true
						}
					}
				}
			}
		}
	}
	for recv, ms := range methods {
		if gt := d.types[recv]; gt != nil {
			gt.methods = ms
		} else {
			d.types[recv] = &goType{kind: "velox-gen's", methods: ms}
		}
	}
	return d
}

// gqlName is how gqlgen matches a GraphQL field to a Go field: ignoring
// case and underscores.
func gqlName(s string) string { return strings.ToLower(strings.ReplaceAll(s, "_", "")) }

// TestNamesMatchSDL checks the Go against the SDL by name, as gqlgen's
// autobind will see it, on the testdata model and on the one of TestGoTypes.
// It does not check the Go types against the SDL's: Task 6's real gqlgen
// run does.
func TestNamesMatchSDL(t *testing.T) {
	t.Run("testdata", func(t *testing.T) {
		checkNames(t, testModel(t), readFile(t, filepath.Join("..", "sdl", "testdata", "app.graphqls")))
	})
	t.Run("types", func(t *testing.T) { checkNames(t, typesModel(t)) })
}

// checkNames checks, for m:
//   - every Go type the plugin writes is named after the SDL type it binds
//     to, and has one Go field (or method) for each of the SDL type's fields;
//   - every SDL object, input and enum without a Go type of its name, from
//     the plugin or velox-gen, is listed here with the reason;
//   - every @goField(name:) names a Go field or method of the bound type;
//   - the types that implement Node in the SDL are those whose Go type has
//     IsNode and IDGQL, and Node and Nodes are written.
func checkNames(t *testing.T, m *plugin.Model, others ...string) {
	t.Helper()
	sdlText, err := sdl.Render(m)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := sdl.Check(sdlText, others...)
	if err != nil {
		t.Fatal(err)
	}
	surf, err := sdl.Build(m)
	if err != nil {
		t.Fatal(err)
	}
	d := parseGo(t, render(t, m))

	// velox-gen's Go types in the generated package: the entities' structs and
	// the enum types; and the enum types that are the application's own.
	veloxGen, appEnums := map[string]bool{}, map[string]bool{}
	for _, e := range m.Entities {
		veloxGen[e.Name] = true
	}
	for _, en := range m.Enums {
		veloxGen[en.Type] = true
	}
	// The entity of each GraphQL type.
	entityOf := map[string]*plugin.Entity{}
	for _, ty := range surf.Types {
		entityOf[ty.Name] = ty.Entity
		for _, en := range ty.Enums {
			if en.Name != "" && !veloxGen[ty.Entity.Fields[en.Field].Enum] {
				appEnums[en.Name] = true
			}
		}
	}
	// The SDL types with no Go type of their name, and why.
	unbound := func(def *gqlast.Definition) string {
		switch {
		case def.Name == "Query" || def.Name == "Mutation":
			return "the root types: gqlgen generates their resolvers, which the application writes"
		case def.Name == "PageInfo" || def.Name == "OrderDirection":
			return "veloxgql's, mapped in gqlgen.yml"
		case strings.HasPrefix(def.Name, "Create") && strings.HasSuffix(def.Name, "Input"),
			strings.HasPrefix(def.Name, "Update") && strings.HasSuffix(def.Name, "Input"):
			return "the mutation inputs: plan 3 writes their Go types"
		case def.Kind == gqlast.Enum && appEnums[def.Name]:
			return "the application's own enum type, which a later task binds with @goModel"
		}
		return ""
	}
	methodsOf := func(name string) []string {
		if gt := d.types[name]; gt != nil {
			return gt.methods
		}
		return nil
	}

	sdlTypes := map[string]*gqlast.Definition{}
	for name, def := range schema.Types {
		if def.BuiltIn || strings.HasPrefix(name, "__") || def.Position.Src.Name != "velox.graphqls" {
			continue // gqlparser's, or the application's own
		}
		switch def.Kind {
		case gqlast.Object, gqlast.InputObject, gqlast.Enum:
			sdlTypes[name] = def
		}
	}
	for name, def := range sdlTypes {
		if e := entityOf[name]; e != nil {
			// The @goField targets on the entity's Go type.
			var has []string
			for _, f := range e.Fields {
				has = append(has, f.GoName)
			}
			for _, ed := range e.Edges {
				has = append(has, ed.GoName)
			}
			has = append(has, methodsOf(e.Name)...)
			for _, f := range def.Fields {
				dir := f.Directives.ForName("goField")
				if dir == nil {
					continue
				}
				target := dir.Arguments.ForName("name").Value.Raw
				if !slices.Contains(has, target) {
					t.Errorf("%s.%s binds to %s, which %s does not have", name, f.Name, target, e.Name)
				}
			}
			// Node in the SDL is a veloxgql.Noder in Go, both ways.
			ms := methodsOf(e.Name)
			goNode := slices.Contains(ms, "IsNode") && slices.Contains(ms, "IDGQL")
			if sdlNode := slices.Contains(def.Interfaces, "Node"); sdlNode != goNode {
				t.Errorf("%s implements Node in the SDL: %v; %s has IsNode and IDGQL: %v", name, sdlNode, e.Name, goNode)
			}
		}
		gt := d.types[name]
		switch {
		case gt != nil && gt.kind != "velox-gen's":
		case veloxGen[name] && def.Kind == gqlast.Enum:
			// velox-gen's enum type: the plugin gives it the marshalers that
			// turn the GraphQL values into the stored ones.
			if gt == nil || !slices.Contains(gt.methods, "MarshalGQL") || !slices.Contains(gt.methods, "UnmarshalGQL") {
				t.Errorf("the enum %s is velox-gen's Go type, and the plugin writes no MarshalGQL and UnmarshalGQL for it", name)
			}
			continue
		case veloxGen[name]:
			continue
		case unbound(def) != "":
			continue
		default:
			t.Errorf("the SDL %s %s has no Go type and no reason", strings.ToLower(string(def.Kind)), name)
			continue
		}
		if gt.alias != "" {
			if !veloxGen[gt.alias] || entityOf[name] == nil || entityOf[name].Name != gt.alias {
				t.Errorf("%s is an alias of %s, which is not its entity's Go type", name, gt.alias)
			}
			continue
		}
		switch def.Kind {
		case gqlast.Enum:
			if gt.kind != "string" || !slices.Contains(gt.methods, "MarshalGQL") || !slices.Contains(gt.methods, "UnmarshalGQL") {
				t.Errorf("the enum %s: Go type %+v, want a string type with MarshalGQL and UnmarshalGQL", name, gt)
			}
		case gqlast.InputObject:
			var want []string
			for _, f := range def.Fields {
				want = append(want, gqlName(f.Name))
			}
			var have []string
			for _, f := range gt.fields {
				have = append(have, gqlName(f))
			}
			if !slices.Equal(have, want) {
				t.Errorf("the input %s: Go fields %v, want one for each of %v", name, gt.fields, want)
			}
		case gqlast.Object:
			for _, f := range def.Fields {
				n := 0
				for _, g := range append(slices.Clone(gt.fields), gt.methods...) {
					if gqlName(g) == gqlName(f.Name) {
						n++
					}
				}
				if n != 1 {
					t.Errorf("the object %s's field %s: %d Go fields or methods match, want 1", name, f.Name, n)
				}
			}
		}
	}
	// Every Go type the plugin writes binds to the SDL type of its name.
	for name, gt := range d.types {
		if gt.kind == "velox-gen's" {
			if !veloxGen[name] {
				t.Errorf("methods on %s, which neither the plugin nor velox-gen declares", name)
			}
			continue
		}
		if sdlTypes[name] == nil {
			t.Errorf("the Go type %s binds to no SDL type", name)
		}
	}
	if !d.funcs["Node"] || !d.funcs["Nodes"] {
		t.Errorf("funcs %v: want Node and Nodes", d.funcs)
	}
}

// typesModel has what the testdata model has not: a UUID key; a key of the
// application's own type, from a package named ids, as one of the generated
// code's locals is; the application's own enum type and string type; a JSON
// field; and predicates on the scalars veloxgql binds.
func typesModel(t *testing.T) *plugin.Model {
	t.Helper()
	ann := func(a graphql.Annotation) map[string]json.RawMessage {
		b, err := json.Marshal(a)
		if err != nil {
			t.Fatal(err)
		}
		return map[string]json.RawMessage{"graphql": b}
	}
	where := func(ops graphql.WhereOp) map[string]json.RawMessage { return ann(graphql.Annotation{WhereOps: ops}) }
	return &plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{
		{
			Name: "Event", Label: "event", Handles: "Events", Tenant: -1, PrimaryKey: []int{0},
			Edges: []*plugin.Edge{{Name: "owner", GoName: "Owner", Target: "Owner", Kind: plugin.M2O, Unique: true, Field: "owner_id", FieldOn: "Event", OwnsKey: true, Annotations: ann(graphql.Annotation{WhereEdge: true})}},
			Fields: []*plugin.Field{
				{Name: "id", GoName: "ID", Type: "uuid", GoType: "uuid.UUID", ElemType: "uuid.UUID", PkgPath: "github.com/google/uuid", Kind: "array", Annotations: where(graphql.OpsDefault)},
				{Name: "level", GoName: "Level", Type: "enum", GoType: "levels.Level", ElemType: "levels.Level", PkgPath: "example.com/app/types/v2", Kind: "string", Enum: "Level", EnumValues: []string{"low", "high"}, Annotations: ann(graphql.Annotation{WhereOps: graphql.OpEQ, OrderField: "LEVEL"})},
				{Name: "size", GoName: "Size", Type: "uint64", GoType: "uint64", ElemType: "uint64", Annotations: where(graphql.OpGT)},
				{Name: "rank", GoName: "Rank", Type: "int8", GoType: "int8", ElemType: "int8", Annotations: where(graphql.OpIn)},
				{Name: "at", GoName: "At", Type: "time", GoType: "*time.Time", ElemType: "time.Time", PkgPath: "time", Nullable: true, Pointer: true, Annotations: where(graphql.OpLT | graphql.OpIsNil)},
				{Name: "ref", GoName: "Ref", Type: "uuid", GoType: "uuid.UUID", ElemType: "uuid.UUID", PkgPath: "github.com/google/uuid", Kind: "array", Annotations: where(graphql.OpNEQ)},
				{Name: "tags", GoName: "Tags", Type: "json", GoType: "[]string", ElemType: "[]string", Kind: "slice"},
				// A foreign key of the application's own type, to Owner.
				{Name: "owner_id", GoName: "OwnerID", Type: "int64", GoType: "*ids.UserID", ElemType: "ids.UserID", PkgPath: "example.com/app/ids", Kind: "int64", Pointer: true, Nullable: true, Annotations: where(graphql.OpsDefault)},
			},
		},
		{
			Name: "Owner", Label: "owner", Handles: "Owners", Tenant: -1, PrimaryKey: []int{0}, Edges: []*plugin.Edge{},
			Fields: []*plugin.Field{
				{Name: "id", GoName: "ID", Type: "int64", GoType: "ids.UserID", ElemType: "ids.UserID", PkgPath: "example.com/app/ids", Kind: "int64", Annotations: where(graphql.OpEQ)},
				{Name: "email", GoName: "Email", Type: "string", GoType: "*ids.Email", ElemType: "ids.Email", PkgPath: "example.com/app/ids", Kind: "string", Pointer: true, Nullable: true, Annotations: where(graphql.OpEQ)},
				{Name: "code", GoName: "Code", Type: "string", GoType: "ids.Code", ElemType: "ids.Code", PkgPath: "example.com/app/ids", Kind: "string"},
				{Name: "rank", GoName: "Rank", Type: "int32", GoType: "ids.Rank", ElemType: "ids.Rank", PkgPath: "example.com/app/ids", Kind: "int32", Annotations: ann(graphql.Annotation{OrderField: "RANK", WhereOps: graphql.OpIn})},
				{Name: "ref", GoName: "Ref", Type: "uuid", GoType: "ids.Ref", ElemType: "ids.Ref", PkgPath: "example.com/app/ids", Kind: "array", Annotations: where(graphql.OpEQ)},
				{Name: "ratio", GoName: "Ratio", Type: "float64", GoType: "ids.Ratio", ElemType: "ids.Ratio", PkgPath: "example.com/app/ids", Kind: "float64", Annotations: where(graphql.OpGT)},
				{Name: "on", GoName: "On", Type: "bool", GoType: "ids.Flag", ElemType: "ids.Flag", PkgPath: "example.com/app/ids", Kind: "bool", Annotations: where(graphql.OpEQ)},
			},
		},
	}}
}

// TestGoTypes pins the Go types of the where predicates, keys, getters and
// enums of typesModel.
func TestGoTypes(t *testing.T) {
	files := render(t, typesModel(t))
	// Compared with the spaces squashed, as gofmt aligns them.
	squash := func(s string) string { return " " + strings.Join(strings.Fields(s), " ") + " " }
	ev, owner, node := squash(files["gql_event.go"]), squash(files["gql_owner.go"]), squash(files["gql_node.go"])
	for _, want := range []string{
		`func (x *Event) IDGQL() string { return veloxgql.EncodeID("Event", [16]byte(x.ID)) }`,
		// The columns' own Go types, which bind through veloxgql's function
		// marshalers and gqlgen's.
		" ID *string ", " IDNEQ *string ", " IDIn []string ",
		" Level *levels.Level ",
		" SizeGT *uint64 ",
		" RankIn []int8 ",
		" AtLT *time.Time ", " AtIsNil *bool ",
		" RefNEQ *uuid.UUID ",
		` levels "example.com/app/types/v2" `, ` "time" `, ` "github.com/google/uuid" `,
		// The key's order term, and an order field of the application's enum.
		"func(x *Event) [16]byte { return [16]byte(x.ID) }, func(x *Event, v [16]byte) { x.ID = uuid.UUID(v) }),",
		"func(x *Event) string { return string(x.Level) }, func(x *Event, v string) { x.Level = levels.Level(v) }), nil",
		// A JSON field binds through a getter.
		"func (x *Event) TagsGQL() (veloxgql.JSON, error) { b, err := json.Marshal(x.Tags) return veloxgql.JSON(b), err }",
		// Pred: one helper call for each predicate, converting the input's
		// values to the column's Go type where they differ.
		` f := gqlimpl.NewFilter[Event](ctx, "EventWhereInput") `,
		` gqlimpl.IDAs(f, "id", w.ID, "Event", gqlimpl.AsUUID[uuid.UUID, [16]byte], Events.ID.EQ) `,
		` gqlimpl.IDListAs(f, "idIn", w.IDIn, "Event", gqlimpl.AsUUID[uuid.UUID, [16]byte], Events.ID.In) `,
		` gqlimpl.Value(f, "level", w.Level, Events.Level.EQ) `,
		` gqlimpl.Value(f, "sizeGT", w.SizeGT, Events.Size.GT) `,
		` gqlimpl.List(f, "rankIn", w.RankIn, Events.Rank.In) `,
		` gqlimpl.Value(f, "atLT", w.AtLT, Events.At.LT) `,
		` gqlimpl.Is(f, "atIsNil", w.AtIsNil, Events.At.IsNull) `,
		` gqlimpl.Value(f, "refNEQ", w.RefNEQ, Events.Ref.NEQ) `,
		` gqlimpl.IDAs(f, "ownerID", w.OwnerID, "Owner", gqlimpl.AsInt[idspkg.UserID, int64], Events.OwnerID.EQ) `,
		` gqlimpl.IDListAs(f, "ownerIDNotIn", w.OwnerIDNotIn, "Owner", gqlimpl.AsInt[idspkg.UserID, int64], Events.OwnerID.NotIn) `,
		` gqlimpl.Is(f, "hasOwner", w.HasOwner, Events.Owner.Has) `,
		` gqlimpl.HasWith(f, "hasOwnerWith", w.HasOwnerWith, Events.Owner.HasWhere) `,
	} {
		if !strings.Contains(ev, want) {
			t.Errorf("no %q in\n%s", want, ev)
		}
	}
	if strings.Contains(ev, "veloxgql.Time") || strings.Contains(ev, "veloxgql.UUID") || strings.Contains(ev, "veloxgql.Uint64") {
		t.Errorf("a where input holds a veloxgql scalar type:\n%s", ev)
	}
	if strings.Contains(ev, "Level) MarshalGQL") {
		t.Errorf("a marshaler on the application's own enum type:\n%s", ev)
	}
	for _, want := range []string{
		`func (x *Owner) IDGQL() string { return veloxgql.EncodeID("Owner", int64(x.ID)) }`,
		// A field of the application's own Go type binds through a getter.
		"func (x *Owner) CodeGQL() string { return string(x.Code) }",
		"func (x *Owner) EmailGQL() *string { if x.Email == nil { return nil } v := string(*x.Email) return &v }",
		// The key's order term converts the key to and from its base type,
		// and an order field held in a pointer to and from its value.
		"func(x *Owner) int64 { return int64(x.ID) }, func(x *Owner, v int64) { x.ID = idspkg.UserID(v) }),",
		"func(x *Owner) int32 { return int32(x.Rank) }, func(x *Owner, v int32) { x.Rank = idspkg.Rank(v) }), nil",
		// Its where predicates take the scalar's Go type, and Pred converts.
		" Email *string ", " RankIn []int32 ", " Ref *uuid.UUID ", " RatioGT *float64 ", " On *bool ",
		` gqlimpl.IDAs(f, "id", w.ID, "Owner", gqlimpl.AsInt[idspkg.UserID, int64], Owners.ID.EQ) `,
		` gqlimpl.ValueAs(f, "email", w.Email, gqlimpl.AsString[idspkg.Email, string], Owners.Email.EQ) `,
		` gqlimpl.ListAs(f, "rankIn", w.RankIn, gqlimpl.AsInt[idspkg.Rank, int32], Owners.Rank.In) `,
		` gqlimpl.ValueAs(f, "ref", w.Ref, gqlimpl.AsUUID[idspkg.Ref, uuid.UUID], Owners.Ref.EQ) `,
		` gqlimpl.ValueAs(f, "ratioGT", w.RatioGT, gqlimpl.AsFloat[idspkg.Ratio, float64], Owners.Ratio.GT) `,
		` gqlimpl.ValueAs(f, "on", w.On, gqlimpl.AsBool[idspkg.Flag, bool], Owners.On.EQ) `,
	} {
		if !strings.Contains(owner, want) {
			t.Errorf("no %q in\n%s", want, owner)
		}
	}
	for _, want := range []string{
		"k, err := veloxgql.DecodeID[[16]byte](id, \"Event\")",
		"Events.ID.EQ(uuid.UUID(k))",
		"keysEvent = append(keysEvent, uuid.UUID(k))",
		"by := make(map[uuid.UUID]*Event, len(rows))",
		` "github.com/google/uuid" `,
		` idspkg "example.com/app/ids" `,
		"k, err := veloxgql.DecodeID[int64](id, \"Owner\")",
		"Owners.ID.EQ(idspkg.UserID(k))",
		"keysOwner []idspkg.UserID",
		"by := make(map[idspkg.UserID]*Owner, len(rows))",
	} {
		if !strings.Contains(node, want) {
			t.Errorf("no %q in\n%s", want, node)
		}
	}
}

// TestNoNodes renders a model whose only type is not a Node: Node and Nodes
// still exist, for the root query, and refuse every ID.
func TestNoNodes(t *testing.T) {
	m := &plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{{
		Name: "Pair", Label: "pair", Handles: "Pairs", Tenant: -1, PrimaryKey: []int{0, 1}, Edges: []*plugin.Edge{},
		Fields: []*plugin.Field{
			{Name: "a", GoName: "A", Type: "int64", GoType: "int64", ElemType: "int64"},
			{Name: "b", GoName: "B", Type: "int64", GoType: "int64", ElemType: "int64"},
		},
	}}}
	node := render(t, m)["gql_node.go"]
	if !strings.Contains(node, "func Node(_ context.Context, id string) (veloxgql.Noder, error) {") || strings.Contains(node, "DBFrom(ctx)") {
		t.Errorf("gql_node.go:\n%s", node)
	}
}

func TestRenderErrors(t *testing.T) {
	entity := func(name, keyType string) *plugin.Entity {
		return &plugin.Entity{
			Name: name, Label: strings.ToLower(name), Handles: name + "s", Tenant: -1, PrimaryKey: []int{0}, Edges: []*plugin.Edge{},
			Fields: []*plugin.Field{
				{Name: "id", GoName: "ID", Type: keyType, GoType: keyType, ElemType: keyType},
				{Name: "x", GoName: "X", Type: "string", GoType: "string", ElemType: "string"},
			},
		}
	}
	for name, c := range map[string]struct {
		m    *plugin.Model
		want string
	}{
		"node file": {&plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{func() *plugin.Entity {
			e := entity("Node", "int64")
			e.Annotations = map[string]json.RawMessage{"graphql": json.RawMessage(`{"type":"Vertex"}`)}
			return e
		}()}}, "schema Node: its Go file would be gql_node.go"},
		"sdl": {&plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{entity("Query", "int64")}}, "Query"},
		"many-to-many with no inverse": {&plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{func() *plugin.Entity {
			e := entity("Author", "int64")
			e.Edges = []*plugin.Edge{{Name: "books", GoName: "Books", Target: "Book", Kind: plugin.M2M, Join: "AuthorBook"}}
			return e
		}(), entity("Book", "int64")}}, "schema Author, edge books: velox-graphql finds the nodes of a many-to-many edge through its inverse edge, and Book has none"},
		"an edge to a key of two columns": {&plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{func() *plugin.Entity {
			e := entity("Author", "int64")
			e.Edges = []*plugin.Edge{{Name: "pair", GoName: "Pair", Target: "Pair", Kind: plugin.M2O, Unique: true, Field: "pair_a", FieldOn: "Author", OwnsKey: true}}
			e.Fields = append(e.Fields, &plugin.Field{Name: "pair_a", GoName: "PairA", Type: "int64", GoType: "int64", ElemType: "int64", Implicit: "pair"})
			return e
		}(), func() *plugin.Entity {
			e := entity("Pair", "int64")
			e.PrimaryKey = []int{0, 1}
			e.Fields[1] = &plugin.Field{Name: "b", GoName: "B", Type: "int64", GoType: "int64", ElemType: "int64"}
			return e
		}()}}, "schema Author, edge pair: velox-graphql finds its nodes by the key of Pair, which is 2 columns"},
		"an edge from a key of two columns": {&plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{func() *plugin.Entity {
			e := entity("Pair", "int64")
			e.PrimaryKey = []int{0, 1}
			e.Fields[1] = &plugin.Field{Name: "b", GoName: "B", Type: "int64", GoType: "int64", ElemType: "int64"}
			e.Edges = []*plugin.Edge{{Name: "notes", GoName: "Notes", Target: "Note", Kind: plugin.O2M, Field: "pair_notes", FieldOn: "Note"}}
			return e
		}(), withField(entity("Note", "int64"), &plugin.Field{Name: "pair_notes", GoName: "PairNotes", Type: "int64", GoType: "*int64", ElemType: "int64", Pointer: true, Nullable: true, Implicit: "notes"})}},
			"schema Pair, edge notes: velox-graphql finds its nodes by the key of Pair, which is 2 columns"},
		// The kind of the application's Go type tells what converts.
		"a struct for a time": {&plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{withField(entity("Thing", "int64"),
			&plugin.Field{Name: "at", GoName: "At", Type: "time", GoType: "types.Stamp", ElemType: "types.Stamp", PkgPath: "example.com/types", Kind: "struct"})}},
			"schema Thing, field at: velox-graphql converts a time field only from time.Time, and its Go type is types.Stamp"},
		"a struct for a UUID key": {&plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{func() *plugin.Entity {
			e := entity("Thing", "uuid")
			e.Fields[0].ElemType, e.Fields[0].PkgPath, e.Fields[0].Kind = "types.ID", "example.com/types", "struct"
			return e
		}()}}, "schema Thing, field id: velox-graphql converts its Go type types.ID to [16]byte, and a struct does not convert"},
		"a string for an integer key": {&plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{func() *plugin.Entity {
			e := entity("Thing", "int64")
			e.Fields[0].ElemType, e.Fields[0].PkgPath, e.Fields[0].Kind = "types.ID", "example.com/types", "string"
			return e
		}()}}, "schema Thing, field id: velox-graphql converts its Go type types.ID to int64, and a string does not convert"},
		"a where on a time of another Go type": {&plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{withField(entity("Thing", "int64"),
			&plugin.Field{Name: "at", GoName: "At", Type: "time", GoType: "types.Stamp", ElemType: "types.Stamp", PkgPath: "example.com/types", Annotations: map[string]json.RawMessage{"graphql": json.RawMessage(`{"where_ops":1}`)}})}},
			"schema Thing, field at: velox-graphql converts a time field only from time.Time, and its Go type is types.Stamp"},
		// Only a Nillable field is a pointer, and keyset paging needs an
		// order column without NULLs.
		"a pointer order field": {&plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{withField(entity("Thing", "int64"),
			&plugin.Field{Name: "n", GoName: "N", Type: "int64", GoType: "*int64", ElemType: "int64", Pointer: true, Annotations: map[string]json.RawMessage{"graphql": json.RawMessage(`{"order_field":"N"}`)}})}},
			"schema Thing, field n: a connection orders by it, and its Go field is a pointer, which only a Nillable field is"},
		"a where field named Pred": {&plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{withField(entity("Thing", "int64"),
			&plugin.Field{Name: "pred", GoName: "Pred", Type: "string", GoType: "string", ElemType: "string", Annotations: map[string]json.RawMessage{"graphql": json.RawMessage(`{"where_ops":1}`)}})}},
			"schema Thing: the field pred of ThingWhereInput is the Go field Pred"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Render(c.m, outPath, "gen"); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err %v, want one with %q", err, c.want)
			}
		})
	}
}

// withField is e with fd added.
func withField(e *plugin.Entity, fd *plugin.Field) *plugin.Entity {
	e.Fields = append(e.Fields, fd)
	return e
}

// TestGeneratedFilesCarryTheGuard: every file carries the version guard once,
// as the core's files carry theirs, whatever else it uses of veloxgql.
func TestGeneratedFilesCarryTheGuard(t *testing.T) {
	for label, m := range map[string]*plugin.Model{"testdata": testModel(t), "types": typesModel(t)} {
		files := render(t, m)
		if len(files) == 0 {
			t.Fatalf("%s: no files", label)
		}
		for name, src := range files {
			if n := strings.Count(src, "const _ = veloxgql.GeneratedCodeIsVersion1\n"); n != 1 {
				t.Errorf("%s: %s has the guard %d times, want once", label, name, n)
			}
			if !strings.Contains(src, `"github.com/syssam/velox2/contrib/graphql/veloxgql"`) {
				t.Errorf("%s: %s does not import veloxgql", label, name)
			}
		}
	}
}

// TestEnumErrorPercent: a stored enum value with a % in it is text in the
// generated error, never a format verb of gqlimpl.InputErrorf.
func TestEnumErrorPercent(t *testing.T) {
	m := testModel(t)
	for _, e := range m.Entities {
		for _, f := range e.Fields {
			if f.Enum == "GroupVisibility" {
				f.EnumValues = []string{"public", "50% off"}
			}
		}
	}
	for _, en := range m.Enums {
		if en.Type == "GroupVisibility" {
			en.Values = []string{"public", "50% off"}
		}
	}
	src := render(t, m)["gql_group.go"]
	_, rest, ok := strings.Cut(src, "gqlimpl.InputErrorf(")
	if !ok {
		t.Fatalf("gql_group.go calls no gqlimpl.InputErrorf:\n%s", src)
	}
	lit, _, _ := strings.Cut(rest, ")\n")
	format, err := strconv.Unquote(lit)
	if err != nil {
		t.Fatalf("the format %s: %v", lit, err)
	}
	// As InputErrorf formats it, with no arguments.
	if got := fmt.Sprintf(format, []any{}...); got != "GroupVisibility must be public or 50% off" {
		t.Errorf("the error reads %q", got)
	}
}
