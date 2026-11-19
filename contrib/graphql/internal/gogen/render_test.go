package gogen

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/printer"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	gqlast "github.com/vektah/gqlparser/v2/ast"

	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/contrib/graphql/internal/modeltest"
	"github.com/syssam/velox/contrib/graphql/internal/sdl"
	"github.com/syssam/velox/plugin"
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
	got := render(t, modeltest.Model(t))
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
	if _, ok := got["session_gql.go"]; ok {
		t.Error("Session's type is skipped, and it has a Go file")
	}
	if _, ok := got["user_group_gql.go"]; ok {
		t.Error("a join entity has a Go file")
	}
}

// TestGoFileNameBuildsEverywhere: a schema may be named like a system, an
// architecture or a test, and its Go file is still in every build.
func TestGoFileNameBuildsEverywhere(t *testing.T) {
	for _, name := range []string{"Android", "Windows", "Linux", "Arm", "Wasm", "Test"} {
		t.Run(name, func(t *testing.T) {
			m := modeltest.Model(t)
			u := m.Entities[slices.IndexFunc(m.Entities, func(e *plugin.Entity) bool { return e.Name == "User" })]
			u.Label = strings.ToLower(name)
			files := render(t, m)
			base := u.Label + "_gql.go"
			src, ok := files[base]
			if !ok {
				t.Fatalf("no %s", base)
			}
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, base), []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, goos := range []string{"linux", "darwin", "plan9"} {
				ctx := build.Context{GOOS: goos, GOARCH: "riscv64", Compiler: "gc"}
				if ok, err := ctx.MatchFile(dir, base); err != nil || !ok {
					t.Errorf("%s/riscv64 leaves out %s: %v", goos, base, err)
				}
			}
			if strings.HasSuffix(base, "_test.go") {
				t.Errorf("%s is a test file", base)
			}
		})
	}
}

// TestGoDeterministic renders the model twice.
func TestGoDeterministic(t *testing.T) {
	a, b := render(t, modeltest.Model(t)), render(t, modeltest.Model(t))
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
		checkNames(t, modeltest.Model(t), readFile(t, filepath.Join("..", "sdl", "testdata", "app.graphql")))
	})
	t.Run("types", func(t *testing.T) { checkNames(t, typesModel(t)) })
}

// checkNames checks, for m:
//   - every Go type the plugin writes is named after the SDL type it binds
//     to, and has one Go field (or method) for each of the SDL type's fields;
//   - every SDL object, input and enum without a Go type of its name, from
//     the plugin or velox-gen, is listed here with the reason;
//   - every @goField(name:) names a Go field or method of the bound type;
//   - no type implements an interface, no Go type has IsNode or GlobalID,
//     and neither Node nor Nodes is written.
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
		case def.Kind == gqlast.Enum && appEnums[def.Name]:
			// Bound by @goModel to the application's own type, which the
			// plugin does not write.
			if def.Directives.ForName("goModel") == nil {
				t.Errorf("the application's enum %s has no @goModel", def.Name)
			}
			return "the application's own enum type, which @goModel binds"
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
		if def.BuiltIn || strings.HasPrefix(name, "__") || def.Position.Src.Name != "velox.graphql" {
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
			// velox has no Node interface: an ID is the key.
			ms := methodsOf(e.Name)
			if len(def.Interfaces) != 0 || slices.Contains(ms, "IsNode") || slices.Contains(ms, "GlobalID") {
				t.Errorf("%s implements %v in the SDL; %s has methods %v", name, def.Interfaces, e.Name, ms)
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
		if o, ok := strings.CutSuffix(name, "PaginateOption"); ok && sdlTypes[o] != nil {
			continue // Paginate's options, which only Go reads
		}
		if sdlTypes[name] == nil {
			t.Errorf("the Go type %s binds to no SDL type", name)
		}
	}
	if d.funcs["Node"] || d.funcs["Nodes"] {
		t.Errorf("funcs %v: want no Node and Nodes", d.funcs)
	}
}

// typesModel has what the testdata model has not: keys of the application's
// own type, from a package named ids, as one of the generated code's locals
// is, one Go type for every key; the application's own enum type and string type; a JSON
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
			Name: "Event", Label: "event", Handles: "Events", PrimaryKey: []int{0},
			Edges: []*plugin.Edge{{Name: "owner", GoName: "Owner", Target: "Owner", Kind: plugin.M2O, Unique: true, Field: "owner_id", FieldOn: "Event", OwnsKey: true, Annotations: ann(graphql.Annotation{WhereEdge: true})}},
			Fields: []*plugin.Field{
				{Name: "id", GoName: "ID", Type: "int64", GoType: "ids.UserID", ElemType: "ids.UserID", PkgPath: "example.com/app/ids", Kind: "int64", Annotations: where(graphql.OpsDefault)},
				{Name: "level", GoName: "Level", Type: "enum", GoType: "levels.Level", ElemType: "levels.Level", PkgPath: "example.com/app/types/v2", Kind: "string", Enum: "Level", EnumValues: []string{"low", "high"}, Annotations: ann(graphql.Annotation{WhereOps: graphql.OpEQ, OrderField: "LEVEL"})},
				{Name: "size", GoName: "Size", Type: "uint64", GoType: "uint64", ElemType: "uint64", Annotations: where(graphql.OpGT)},
				{Name: "rank", GoName: "Rank", Type: "int8", GoType: "int8", ElemType: "int8", Annotations: where(graphql.OpIn)},
				{Name: "at", GoName: "At", Type: "time", GoType: "*time.Time", ElemType: "time.Time", PkgPath: "time", Nullable: true, Pointer: true, Annotations: where(graphql.OpLT | graphql.OpIsNil)},
				{Name: "ref", GoName: "Ref", Type: "uuid", GoType: "uuid.UUID", ElemType: "uuid.UUID", PkgPath: "github.com/google/uuid", Kind: "array", Annotations: where(graphql.OpNEQ)},
				{Name: "tags", GoName: "Tags", Type: "json", GoType: "[]int32", ElemType: "[]int32", Kind: "slice"},
				// A foreign key of the application's own type, to Owner.
				{Name: "owner_id", GoName: "OwnerID", Type: "int64", GoType: "*ids.UserID", ElemType: "ids.UserID", PkgPath: "example.com/app/ids", Kind: "int64", Pointer: true, Nullable: true, Annotations: where(graphql.OpsDefault)},
			},
		},
		{
			Name: "Owner", Label: "owner", Handles: "Owners", PrimaryKey: []int{0}, Edges: []*plugin.Edge{},
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
	ev, owner := squash(files["event_gql.go"]), squash(files["owner_gql.go"])
	if _, ok := files["node_gql.go"]; ok {
		t.Error("velox-graphql wrote node_gql.go")
	}
	for _, want := range []string{
		// The columns' own Go types, which bind through veloxgql's function
		// marshalers and gqlgen's; the IDs are the keys, in their Go type.
		" ID *idspkg.UserID ", " IDNEQ *idspkg.UserID ", " IDIn []idspkg.UserID ", " OwnerIDIn []idspkg.UserID ",
		" Level *levels.Level ",
		" SizeGT *uint64 ",
		" RankIn []int8 ",
		" AtLT *time.Time ", " AtIsNil *bool ",
		" RefNEQ *uuid.UUID ",
		` levels "example.com/app/types/v2" `, ` "time" `, ` "github.com/google/uuid" `,
		// The key's order term, and an order field of the application's enum.
		// The connection's columns: gqlgraph converts their values to and from
		// their base types.
		"Key: []gqlgraph.Column{{Name: \"id\", Index: eventColID}},",
		"{Name: \"LEVEL\", Index: eventColLevel},",
		// A JSON field binds through a getter.
		"func (e *Event) TagsGQL() (veloxgql.JSON, error) { b, err := json.Marshal(e.Tags) return veloxgql.JSON(b), err }",
		// Pred: one helper call for each field's predicates, by column
		// index, with the input's values in a gqlgraph.Ops of the Go type
		// they hold; gqlgraph converts them to the column's Go type where
		// they differ.
		` f := gqlgraph.NewFilter(ctx, &eventType) `,
		` gqlgraph.IDField(f, "id", eventColID, gqlgraph.Ops[idspkg.UserID]{ EQ: w.ID, NEQ: w.IDNEQ, In: w.IDIn, NotIn: w.IDNotIn, GT: w.IDGT, GTE: w.IDGTE, LT: w.IDLT, LTE: w.IDLTE, }) `,
		` gqlgraph.Field(f, "level", eventColLevel, gqlgraph.Ops[levels.Level]{EQ: w.Level}) `,
		` gqlgraph.Field(f, "size", eventColSize, gqlgraph.Ops[uint64]{GT: w.SizeGT}) `,
		` gqlgraph.Field(f, "rank", eventColRank, gqlgraph.Ops[int8]{In: w.RankIn}) `,
		` gqlgraph.Field(f, "at", eventColAt, gqlgraph.Ops[time.Time]{LT: w.AtLT, IsNil: w.AtIsNil}) `,
		` gqlgraph.Field(f, "ref", eventColRef, gqlgraph.Ops[uuid.UUID]{NEQ: w.RefNEQ}) `,
		` gqlgraph.EdgeIDField(f, "ownerID", eventColOwnerID, eventEdgeOwner, &ownerType, gqlgraph.Ops[idspkg.UserID]{ EQ: w.OwnerID, NEQ: w.OwnerIDNEQ, In: w.OwnerIDIn, NotIn: w.OwnerIDNotIn, IsNil: w.OwnerIDIsNil, NotNil: w.OwnerIDNotNil, }) `,
		` gqlgraph.Has(f, "hasOwner", w.HasOwner, eventEdgeOwner, untyped.TableOf[Owner]()) `,
		` gqlgraph.HasWith(f, "hasOwnerWith", w.HasOwnerWith, (*OwnerWhereInput).predicate, eventEdgeOwner, untyped.TableOf[Owner]()) `,
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
		// A field of the application's own Go type binds through a getter.
		"func (o *Owner) CodeGQL() string { return string(o.Code) }",
		"func (o *Owner) EmailGQL() *string { if o.Email == nil { return nil } v := string(*o.Email) return &v }",
		// The connection's columns: gqlgraph converts the key and an order
		// field of the application's Go types to and from their base types.
		"Key: []gqlgraph.Column{{Name: \"id\", Index: ownerColID}},",
		"{Name: \"RANK\", Index: ownerColRank},",
		// Its where predicates take the scalar's Go type, and Pred converts.
		" Email *string ", " RankIn []int32 ", " Ref *uuid.UUID ", " RatioGT *float64 ", " On *bool ",
		` gqlgraph.IDField(f, "id", ownerColID, gqlgraph.Ops[idspkg.UserID]{EQ: w.ID}) `,
		` gqlgraph.Field(f, "email", ownerColEmail, gqlgraph.Ops[string]{EQ: w.Email}) `,
		` gqlgraph.Field(f, "rank", ownerColRank, gqlgraph.Ops[int32]{In: w.RankIn}) `,
		` gqlgraph.Field(f, "ref", ownerColRef, gqlgraph.Ops[uuid.UUID]{EQ: w.Ref}) `,
		` gqlgraph.Field(f, "ratio", ownerColRatio, gqlgraph.Ops[float64]{GT: w.RatioGT}) `,
		` gqlgraph.Field(f, "on", ownerColOn, gqlgraph.Ops[bool]{EQ: w.On}) `,
	} {
		if !strings.Contains(owner, want) {
			t.Errorf("no %q in\n%s", want, owner)
		}
	}
	for _, want := range []string{
		` "github.com/google/uuid" `,
		" Name: \"Event\", Table: untyped.TableOf[Event], ",
		" AlwaysRead: []int{eventColID, eventColOwnerID}, ",
	} {
		if !strings.Contains(ev, want) {
			t.Errorf("no %q in\n%s", want, ev)
		}
	}
	for _, not := range []string{"GlobalID", "IsNode", "EncodeID", "DecodeID"} {
		if strings.Contains(ev+owner, not) {
			t.Errorf("%s in the generated code:\n%s%s", not, ev, owner)
		}
	}
}

// TestUUIDKeyIDs: the IDs of a UUID key are uuid.UUID, the key's Go type,
// in the where input and in an input's edge IDs.
func TestUUIDKeyIDs(t *testing.T) {
	key := func() *plugin.Field {
		return &plugin.Field{Name: "id", GoName: "ID", Type: "uuid", GoType: "uuid.UUID", ElemType: "uuid.UUID", PkgPath: "github.com/google/uuid", Kind: "array", AutoID: true}
	}
	m := &plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{
		{
			Name: "Doc", Label: "doc", Handles: "Docs", PrimaryKey: []int{0},
			Annotations: map[string]json.RawMessage{"graphql": json.RawMessage(`{"mutations":["create","update"]}`)},
			Edges:       []*plugin.Edge{{Name: "tags", GoName: "Tags", Target: "Tag", Kind: plugin.M2M}},
			Fields: []*plugin.Field{key(), {Name: "title", GoName: "Title", Type: "string", GoType: "string", ElemType: "string",
				Annotations: map[string]json.RawMessage{"graphql": json.RawMessage(`{"where_ops":1}`)}}},
		},
		{Name: "Tag", Label: "tag", Handles: "Tags", PrimaryKey: []int{0}, Edges: []*plugin.Edge{}, Fields: []*plugin.Field{key(), {Name: "name", GoName: "Name", Type: "string", GoType: "string", ElemType: "string"}}},
	}}
	doc := strings.Join(strings.Fields(render(t, m)["doc_gql.go"]), " ")
	for _, want := range []string{
		"ID *uuid.UUID", "IDIn []uuid.UUID",
		`gqlgraph.IDField(f, "id", docColID, gqlgraph.Ops[uuid.UUID]{`,
		"TagIDs []uuid.UUID", "AddTagIDs []uuid.UUID",
		`tagIDs, err := gqlgraph.IDs("CreateDocInput.tagIDs", in.TagIDs)`,
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("no %q in\n%s", want, doc)
		}
	}
}

// TestNoIDs renders a model whose only type has no ID: it writes the type's
// file alone.
func TestNoIDs(t *testing.T) {
	m := &plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{{
		Name: "Pair", Label: "pair", Handles: "Pairs", PrimaryKey: []int{0, 1}, Edges: []*plugin.Edge{},
		Fields: []*plugin.Field{
			{Name: "a", GoName: "A", Type: "int64", GoType: "int64", ElemType: "int64"},
			{Name: "b", GoName: "B", Type: "int64", GoType: "int64", ElemType: "int64"},
		},
	}}}
	files := render(t, m)
	if len(files) != 1 || files["pair_gql.go"] == "" {
		t.Errorf("files %v, want pair_gql.go alone", slices.Collect(maps.Keys(files)))
	}
}

func TestRenderErrors(t *testing.T) {
	entity := func(name, keyType string) *plugin.Entity {
		return &plugin.Entity{
			Name: name, Label: strings.ToLower(name), Handles: name + "s", PrimaryKey: []int{0}, Edges: []*plugin.Edge{},
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
		"sdl": {&plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{entity("Query", "int64")}}, "Query"},
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
		// clear<Field> sets NULL: a nullable field whose Go field holds no
		// nil cannot be cleared (velox-gen's rule 48 refuses such a model).
		"a nullable JSON struct that is no pointer": {&plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{func() *plugin.Entity {
			e := withField(entity("Thing", "int64"), &plugin.Field{Name: "shape", GoName: "Shape", Type: "json", GoType: "types.Shape", ElemType: "types.Shape", PkgPath: "example.com/types", Kind: "struct", Optional: true, Nullable: true})
			e.Annotations = map[string]json.RawMessage{"graphql": json.RawMessage(`{"mutations":["update"]}`)}
			return e
		}()}}, "schema Thing, field shape: the model says its column holds NULL, and its Go type types.Shape holds no nil; make the field Nillable"},
		// The kind of the application's Go type tells what converts.
		"a struct for a time": {&plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{withField(entity("Thing", "int64"),
			&plugin.Field{Name: "at", GoName: "At", Type: "time", GoType: "types.Stamp", ElemType: "types.Stamp", PkgPath: "example.com/types", Kind: "struct"})}},
			"schema Thing, field at: velox-graphql converts a time field only from time.Time, and its Go type is types.Stamp"},
		// A type writes thingType, beside the names velox-gen declares.
		"a handles variable named as a type's connection variable": {&plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{func() *plugin.Entity {
			e := entity("Thing", "int64")
			e.Handles = "thingType"
			return e
		}()}}, "its Go name thingType is also velox-gen's handles of schema Thing"},
		"a column constant named as a type's variable": {&plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{
			withField(entity("Thing", "int64"), &plugin.Field{Name: "type", GoName: "Type", Type: "string", GoType: "string", ElemType: "string"}),
			func() *plugin.Entity {
				e := entity("ThingCol", "int64")
				e.Label, e.Handles = "thing_col", "ThingCols"
				return e
			}(),
		}}, "its Go name thingColType is also velox-gen's column constant of field type of schema Thing"},
		"a key of a Go type it cannot name": {&plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{func() *plugin.Entity {
			e := entity("Thing", "int64")
			e.Fields[0].ElemType, e.Fields[0].PkgPath, e.Fields[0].Kind = "[]types.ID", "example.com/types", "int64"
			e.Fields[0].Annotations = map[string]json.RawMessage{"graphql": json.RawMessage(`{"where_ops":1}`)}
			return e
		}()}}, "schema Thing, field id: velox-graphql cannot name the Go type []types.ID"},
		"a key of a Go type with no package": {&plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{func() *plugin.Entity {
			e := entity("Thing", "int64")
			e.Fields[0].ElemType, e.Fields[0].Kind = "types.ID", "int64"
			e.Fields[0].Annotations = map[string]json.RawMessage{"graphql": json.RawMessage(`{"where_ops":1}`)}
			return e
		}()}}, "schema Thing, field id: the model gives no package for the Go type types.ID"},
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
		"an int for a UUID key": {&plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{func() *plugin.Entity {
			e := entity("Thing", "uuid")
			e.Fields[0].ElemType, e.Fields[0].PkgPath, e.Fields[0].Kind = "types.ID", "example.com/types", "int"
			return e
		}()}}, "schema Thing, field id: velox-graphql converts its Go type types.ID to [16]byte, and an int does not convert"},
		"a where on a time of another Go type": {&plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{withField(entity("Thing", "int64"),
			&plugin.Field{Name: "at", GoName: "At", Type: "time", GoType: "types.Stamp", ElemType: "types.Stamp", PkgPath: "example.com/types", Annotations: map[string]json.RawMessage{"graphql": json.RawMessage(`{"where_ops":1}`)}})}},
			"schema Thing, field at: velox-graphql converts a time field only from time.Time, and its Go type is types.Stamp"},
		"a create-input field named Setters": {&plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{creatable(withField(entity("Thing", "int64"),
			&plugin.Field{Name: "text", GoName: "Text", Type: "string", GoType: "string", ElemType: "string", Annotations: map[string]json.RawMessage{"graphql": json.RawMessage(`{"field_name":"setters"}`)}}))}},
			"schema Thing: the field setters of CreateThingInput is the Go field Setters, which is the input's method"},
		"a create-input field named String beside a sensitive one": {&plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{creatable(withField(entity("Thing", "int64"),
			&plugin.Field{Name: "secret", GoName: "Secret", Type: "string", GoType: "string", ElemType: "string", Sensitive: true},
			&plugin.Field{Name: "text", GoName: "Text", Type: "string", GoType: "string", ElemType: "string", Annotations: map[string]json.RawMessage{"graphql": json.RawMessage(`{"field_name":"string"}`)}}))}},
			"schema Thing: the field string of CreateThingInput is the Go field String, which is the input's method"},
		"create-input fields one to gqlgen": {&plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{creatable(withField(entity("Thing", "int64"),
			&plugin.Field{Name: "ab", GoName: "Ab", Type: "string", GoType: "string", ElemType: "string"},
			&plugin.Field{Name: "other", GoName: "Other", Type: "string", GoType: "string", ElemType: "string", Annotations: map[string]json.RawMessage{"graphql": json.RawMessage(`{"field_name":"aB"}`)}}))}},
			"schema Thing: the fields Ab and AB of CreateThingInput are one Go field to gqlgen"},
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

// creatable is e with a create input, and an auto ID.
func creatable(e *plugin.Entity) *plugin.Entity {
	e.Fields[0].AutoID = true
	e.Annotations = map[string]json.RawMessage{"graphql": json.RawMessage(`{"mutations":["create"]}`)}
	return e
}

// withField is e with fds added.
func withField(e *plugin.Entity, fds ...*plugin.Field) *plugin.Entity {
	e.Fields = append(e.Fields, fds...)
	return e
}

// TestGeneratedFilesCarryTheGuard: every file carries the version guard once,
// as the core's files carry theirs, whatever else it uses of veloxgql.
func TestGeneratedFilesCarryTheGuard(t *testing.T) {
	for label, m := range map[string]*plugin.Model{"testdata": modeltest.Model(t), "types": typesModel(t)} {
		files := render(t, m)
		if len(files) == 0 {
			t.Fatalf("%s: no files", label)
		}
		for name, src := range files {
			if n := strings.Count(src, "const _ = veloxgql.GeneratedCodeIsVersion25\n"); n != 1 {
				t.Errorf("%s: %s has the guard %d times, want once", label, name, n)
			}
			if !strings.Contains(src, `"github.com/syssam/velox/contrib/graphql/veloxgql"`) {
				t.Errorf("%s: %s does not import veloxgql", label, name)
			}
		}
	}
}

// TestEnumErrorPercent: a stored enum value with a % in it is text in the
// generated error, never a format verb of gqlgraph.InputErrorf, whose one
// verb is the value given.
func TestEnumErrorPercent(t *testing.T) {
	m := modeltest.Model(t)
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
	src := render(t, m)["group_gql.go"]
	_, rest, ok := strings.Cut(src, "gqlgraph.InputErrorf(")
	if !ok {
		t.Fatalf("group_gql.go calls no gqlgraph.InputErrorf:\n%s", src)
	}
	lit, _, _ := strings.Cut(rest, ", v)\n")
	format, err := strconv.Unquote(lit)
	if err != nil {
		t.Fatalf("the format %s: %v", lit, err)
	}
	// As InputErrorf formats it, with the value given.
	if got := fmt.Sprintf(format, "x"); got != "GroupVisibility must be public or 50% off, not x" {
		t.Errorf("the error reads %q", got)
	}
}

// TestEnumWithoutConsts: a model from a velox-gen that writes no enum
// constants fails with an error that says velox-gen is too old.
func TestEnumWithoutConsts(t *testing.T) {
	m := modeltest.Model(t)
	for _, en := range m.Enums {
		if en.Type == "GroupVisibility" {
			en.Consts = nil
		}
	}
	_, err := Render(m, outPath, "gen")
	if err == nil || !strings.Contains(err.Error(), "velox-gen is too old") {
		t.Errorf("Render: %v, want velox-gen is too old", err)
	}
}

// TestPaginateQueryEveryConnection: every connection's file declares the
// entity's PaginateQuery, which takes the query, and no file declares an
// exported name that ends in GQL but the methods gqlgen binds to.
func TestPaginateQueryEveryConnection(t *testing.T) {
	const decl = ") PaginateQuery(\n\tctx context.Context, q velox.Query["
	for label, m := range map[string]*plugin.Model{"testdata": modeltest.Model(t), "types": typesModel(t)} {
		files := render(t, m)
		total := 0
		for name, src := range files {
			n, of := strings.Count(src, "Connection struct {"), strings.Count(src, decl)
			total += of
			if n != of {
				t.Errorf("%s: %s has %d connections and %d PaginateQuery methods", label, name, n, of)
			}
			for l := range strings.SplitSeq(src, "\n") {
				if fn, ok := exportedGQL(l); ok && !strings.HasSuffix(fn, "arshalGQL") && !strings.HasSuffix(fn, "GQL()") {
					t.Errorf("%s: %s declares %s", label, name, l)
				}
			}
		}
		if total == 0 {
			t.Errorf("%s: no PaginateQuery at all", label)
		}
	}
}

// exportedGQL is the name a line declares when it is a function or a method
// whose exported name ends in GQL: its name, and "()" after a getter's,
// which gqlgen binds a field to.
func exportedGQL(line string) (string, bool) {
	rest, ok := strings.CutPrefix(line, "func ")
	if !ok {
		return "", false
	}
	if strings.HasPrefix(rest, "(") {
		_, rest, _ = strings.Cut(rest, ") ")
	}
	name, args, _ := strings.Cut(rest, "(")
	if !strings.HasSuffix(name, "GQL") || name[0] < 'A' || name[0] > 'Z' {
		return "", false
	}
	if strings.HasPrefix(args, ")") {
		return name + "()", true
	}
	return name, true
}

// mutationsModel is typesModel with createX on both types: Event's key is a
// UUID with a default, and Owner's an auto ID of the application's own Go
// type, whose other fields are of its own Go types too.
func mutationsModel(t *testing.T) *plugin.Model {
	t.Helper()
	m := typesModel(t)
	b, err := json.Marshal(graphql.Mutations(graphql.MutationCreate))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range m.Entities {
		e.Annotations = map[string]json.RawMessage{"graphql": b}
		switch e.Name {
		case "Event", "Owner":
			e.Fields[0].AutoID = true
		}
	}
	return m
}

// TestCreateInputs: each type with createX has Create<T>Input, with one Go
// field for each of the Surface's InputFields, in order, a pointer exactly
// when not Required (an ID a string, an ID list a []string), and
// Create<T>, which takes it and returns the entity.
func TestCreateInputs(t *testing.T) {
	for label, m := range map[string]*plugin.Model{"testdata": modeltest.Model(t), "mutations": mutationsModel(t)} {
		t.Run(label, func(t *testing.T) {
			surf, err := sdl.Build(m)
			if err != nil {
				t.Fatal(err)
			}
			structs, funcs, expr := mutationDecls(t, render(t, m))
			n := 0
			for _, ty := range surf.Types {
				in := "Create" + ty.Name + "Input"
				st := structs[in]
				if ty.Create == nil {
					if st != nil || funcs[in+".Setters"] != nil {
						t.Errorf("%s has no create input, and %s or its Setters is declared", ty.Name, in)
					}
					continue
				}
				n++
				if st == nil {
					t.Errorf("no type %s struct", in)
					continue
				}
				var got []string
				for _, fl := range st.Fields.List {
					for _, name := range fl.Names {
						got = append(got, name.Name+" "+expr(fl.Type))
					}
				}
				var want []string
				for _, f := range ty.Create {
					switch f.Kind {
					case sdl.InputEdgeIDs:
						want = append(want, f.GoName+" []"+idsPkg(f.GoType))
					default:
						// An ID is its target's key, in the key's Go type. No
						// GoType of these models names a package imported under
						// another name, or the generated one, but ids, the
						// package of typesModel's keys.
						want = append(want, f.GoName+" "+ptrUnless(f.Required, idsPkg(f.GoType)))
					}
				}
				if !slices.Equal(got, want) {
					t.Errorf("%s: fields\n %v\nwant\n %v", in, got, want)
				}
				fn := funcs[in+".Setters"]
				if fn == nil {
					t.Errorf("no method %s.Setters", in)
					continue
				}
				if want := "func() ([]velox.Setter[" + ty.Entity.Name + "], error)"; expr(fn.Type) != want {
					t.Errorf("%s.Setters: %s, want %s", in, expr(fn.Type), want)
				}
				// The fields the input leaves out and an insert requires are
				// named in its doc: the resolver sets them on x.
				for _, fd := range leftOut(ty) {
					if !strings.Contains(fn.Doc.Text(), fd.GoName) {
						t.Errorf("%s.Setters's doc does not name %s, which it leaves out", in, fd.GoName)
					}
				}
			}
			if n == 0 {
				t.Error("no type has a create input")
			}
		})
	}
}

// mutationDecls are files' struct types and funcs (a method as
// "Recv.Name"), by name, and how to
// print an expression of them.
func mutationDecls(t *testing.T, files map[string]string) (map[string]*ast.StructType, map[string]*ast.FuncDecl, func(ast.Expr) string) {
	t.Helper()
	fset := token.NewFileSet()
	structs, funcs := map[string]*ast.StructType{}, map[string]*ast.FuncDecl{}
	for name, src := range files {
		f, err := parser.ParseFile(fset, name, src, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			switch decl := decl.(type) {
			case *ast.FuncDecl:
				name := decl.Name.Name
				if decl.Recv != nil {
					rt := decl.Recv.List[0].Type
					if s, ok := rt.(*ast.StarExpr); ok {
						rt = s.X
					}
					name = rt.(*ast.Ident).Name + "." + name
				}
				funcs[name] = decl
			case *ast.GenDecl:
				for _, spec := range decl.Specs {
					if ts, ok := spec.(*ast.TypeSpec); ok {
						if st, ok := ts.Type.(*ast.StructType); ok {
							structs[ts.Name.Name] = st
						}
					}
				}
			}
		}
	}
	expr := func(e ast.Expr) string {
		var b strings.Builder
		if err := printer.Fprint(&b, fset, e); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	return structs, funcs, expr
}

// updateOnlyModel is typesModel with an update input and no create input on both types:
// the update input names its fields' validation errors too.
func updateOnlyModel(t *testing.T) *plugin.Model {
	t.Helper()
	m := typesModel(t)
	b, err := json.Marshal(graphql.Mutations(graphql.MutationUpdate))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range m.Entities {
		e.Annotations = map[string]json.RawMessage{"graphql": b}
	}
	return m
}

// TestUpdateInputs: each type with an update input has Update<T>Input, with
// one Go field for each of the Surface's InputFields, in order: a pointer for
// a value or a unique edge's ID, a []string for an ID list and a bool for a
// clear; and its method Setters, which returns the entity's setters through
// gqlgraph.Checked with the type's Conn, which names the input's fields.
func TestUpdateInputs(t *testing.T) {
	for label, m := range map[string]*plugin.Model{"testdata": modeltest.Model(t), "update-only": updateOnlyModel(t)} {
		t.Run(label, func(t *testing.T) {
			surf, err := sdl.Build(m)
			if err != nil {
				t.Fatal(err)
			}
			files := render(t, m)
			structs, funcs, expr := mutationDecls(t, files)
			n := 0
			for _, ty := range surf.Types {
				in := "Update" + ty.Name + "Input"
				fn := in + ".Setters"
				st := structs[in]
				if ty.Update == nil {
					if st != nil || funcs[fn] != nil {
						t.Errorf("%s has no update input, and %s or %s is declared", ty.Name, in, fn)
					}
					continue
				}
				n++
				if st == nil {
					t.Errorf("no type %s struct", in)
					continue
				}
				var got []string
				for _, fl := range st.Fields.List {
					for _, name := range fl.Names {
						got = append(got, name.Name+" "+expr(fl.Type))
					}
				}
				var want []string
				for _, f := range ty.Update {
					switch f.Kind {
					case sdl.InputAddIDs, sdl.InputRemoveIDs:
						want = append(want, f.GoName+" []"+idsPkg(f.GoType))
					case sdl.InputClear, sdl.InputClearEdge:
						want = append(want, f.GoName+" bool")
					case sdl.InputEdgeID:
						want = append(want, f.GoName+" *"+idsPkg(f.GoType))
					case sdl.InputValue:
						if f.Required {
							t.Errorf("%s.%s is required", in, f.Name)
						}
						want = append(want, f.GoName+" *"+f.GoType)
					default:
						t.Errorf("%s.%s is of kind %d", in, f.Name, f.Kind)
					}
				}
				if !slices.Equal(got, want) {
					t.Errorf("%s: fields\n %v\nwant\n %v", in, got, want)
				}
				d := funcs[fn]
				if d == nil {
					t.Errorf("no func %s", fn)
					continue
				}
				if sig, want := expr(d.Type), "func() ([]velox.Setter["+ty.Entity.Name+"], error)"; sig != want {
					t.Errorf("%s: %s, want %s", fn, sig, want)
				}
				base := strings.ToLower(ty.Entity.Name[:1]) + ty.Entity.Name[1:]
				if src := funcSrc(t, files, fn); !strings.Contains(src, "return gqlgraph.Validate(\""+in+"\", &"+base+"Type, set)") {
					t.Errorf("%s does not take %sType:\n%s", fn, base, src)
				}
				lit := connLit(t, files, base+"Type")
				if len(lit["Inputs"]) == 0 && len(lit["UpdateInputs"]) == 0 {
					t.Errorf("%sType names no field of %s", base, in)
				}
			}
			if n == 0 {
				t.Error("no type has an update input")
			}
		})
	}
}

// ptrUnless is typ, as a pointer unless required.
func ptrUnless(required bool, typ string) string {
	if required {
		return typ
	}
	return "*" + typ
}

// idsPkg is typ as the generated code names it: the package ids, a local's
// name, is imported as idspkg.
func idsPkg(typ string) string { return strings.Replace(typ, "ids.", "idspkg.", 1) }

// TestCreateConversions pins how Setters turns its input into setters for
// mutationsModel's Go types: IDs that are keys of the application's key type,
// which gqlgen decoded, JSON decoded before any statement, the
// application's own Go types converted from the scalar's.
func TestCreateConversions(t *testing.T) {
	files := render(t, mutationsModel(t))
	squash := func(s string) string { return " " + strings.Join(strings.Fields(s), " ") + " " }
	ev, owner := squash(files["event_gql.go"]), squash(files["owner_gql.go"])
	for _, want := range []string{
		` OwnerID *idspkg.UserID `,
		` Tags veloxgql.JSON `,
		`if v := in.OwnerID; v != nil { set = append(set, Events.Owner.Set(*v)) }`,
		`tags, err := gqlgraph.FromJSON[[]int32]("CreateEventInput.tags", in.Tags)`,
		`return gqlgraph.Validate("CreateEventInput", &eventType, set)`,
		`eventColTags: "tags"`,
	} {
		if !strings.Contains(ev, want) {
			t.Errorf("no %q in\n%s", want, ev)
		}
	}
	for _, want := range []string{
		` Email *string `, ` Code string `, ` Rank int32 `,
		`set = append(set, Owners.Code.Set(idspkg.Code(in.Code)))`,
		`if v := in.Email; v != nil { set = append(set, Owners.Email.Set(idspkg.Email(*v))) }`,
		`set = append(set, Owners.Rank.Set(idspkg.Rank(in.Rank)))`,
		`set = append(set, Owners.Ref.Set(idspkg.Ref(in.Ref)))`,
	} {
		if !strings.Contains(owner, want) {
			t.Errorf("no %q in\n%s", want, owner)
		}
	}
}

// TestInputStringHidesSensitive: an input's String, and so GoString and
// fmt's %v and %#v, leaves out its sensitive fields.
func TestInputStringHidesSensitive(t *testing.T) {
	m := modeltest.Model(t)
	surf, err := sdl.Build(m)
	if err != nil {
		t.Fatal(err)
	}
	files := render(t, m)
	n := 0
	for _, ty := range surf.Types {
		for _, f := range ty.Create {
			if f.Field < 0 || !ty.Entity.Fields[f.Field].Sensitive {
				continue
			}
			n++
			src := files[ty.Entity.Label+"_gql.go"]
			sig := "func (in Create" + ty.Name + "Input) String() string {"
			_, body, ok := strings.Cut(src, sig)
			if !ok {
				t.Fatalf("no %q in\n%s", sig, src)
			}
			body, _, _ = strings.Cut(body, "\n}\n")
			if strings.Contains(body, f.GoName) {
				t.Errorf("Create%sInput's String names the sensitive field %s:\n%s", ty.Name, f.GoName, body)
			}
			if !strings.Contains(src, "func (in Create"+ty.Name+"Input) GoString() string { return in.String() }") {
				t.Errorf("Create%sInput has no GoString that is String", ty.Name)
			}
		}
	}
	if n == 0 {
		t.Fatal("no create input has a sensitive field")
	}
	// An input with no sensitive field has no String: fmt prints its fields.
	for _, ty := range surf.Types {
		for _, in := range []struct {
			name string
			fs   []sdl.InputField
		}{{"Create" + ty.Name + "Input", ty.Create}, {"Update" + ty.Name + "Input", ty.Update}} {
			if in.fs == nil || slices.ContainsFunc(in.fs, func(f sdl.InputField) bool { return f.Field >= 0 && ty.Entity.Fields[f.Field].Sensitive }) {
				continue
			}
			if strings.Contains(files[ty.Entity.Label+"_gql.go"], "func (in "+in.name+") String() string {") {
				t.Errorf("%s has no sensitive field, and a String", in.name)
			}
		}
	}
}

// setMutations sets the mutations e's graphql annotation lists.
func setMutations(t *testing.T, e *plugin.Entity, kinds ...string) {
	t.Helper()
	var a map[string]any
	if err := json.Unmarshal(e.Annotations["graphql"], &a); err != nil {
		t.Fatal(err)
	}
	a["mutations"] = kinds
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	e.Annotations["graphql"] = b
}

// TestUpdateEdgeToRenamedType: Setters writes the edges of an M2M edge
// whose target's GraphQL type is renamed (Group.users to User, the type
// Account), finding the target by its entity, not by its type's name.
func TestUpdateEdgeToRenamedType(t *testing.T) {
	m := modeltest.Model(t)
	for _, e := range m.Entities {
		if e.Name == "Group" {
			setMutations(t, e, "update")
		}
	}
	src := flat(render(t, m)["group_gql.go"])
	for _, w := range []string{
		`func (in UpdateGroupInput) Setters() ([]velox.Setter[Group], error) {`,
		`if in.ClearUsers { set = append(set, Groups.Users.Clear()) }`,
		`removeUserIDs, err := gqlgraph.IDs("UpdateGroupInput.removeUserIDs", in.RemoveUserIDs)`,
		`set = append(set, Groups.Users.Remove(removeUserIDs...))`,
		`addUserIDs, err := gqlgraph.IDs("UpdateGroupInput.addUserIDs", in.AddUserIDs)`,
		`set = append(set, Groups.Users.Add(addUserIDs...))`,
	} {
		if !strings.Contains(src, w) {
			t.Errorf("group_gql.go: no %q in\n%s", w, src)
		}
	}
}

// connLit is the fields of the Conn literal of the variable name in files:
// each field's elements, printed, by the field's name ("Read": ["postColID",
// …], "Fields": ["postColID: \"id\"", …]); nil when files have no such
// variable.
func connLit(t *testing.T, files map[string]string, name string) map[string][]string {
	t.Helper()
	fset := token.NewFileSet()
	for fname, src := range files {
		f, err := parser.ParseFile(fset, fname, src, 0)
		if err != nil {
			t.Fatalf("%s: %v", fname, err)
		}
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, sp := range gd.Specs {
				vs := sp.(*ast.ValueSpec)
				if len(vs.Names) != 1 || vs.Names[0].Name != name || len(vs.Values) != 1 {
					continue
				}
				out := map[string][]string{}
				for _, el := range vs.Values[0].(*ast.CompositeLit).Elts {
					kv := el.(*ast.KeyValueExpr)
					field := kv.Key.(*ast.Ident).Name
					lit, ok := kv.Value.(*ast.CompositeLit)
					if !ok {
						out[field] = nil
						continue
					}
					var elems []string
					for _, x := range lit.Elts {
						var b strings.Builder
						if err := printer.Fprint(&b, fset, x); err != nil {
							t.Fatal(err)
						}
						elems = append(elems, b.String())
					}
					out[field] = elems
				}
				return out
			}
		}
	}
	return nil
}

// funcSrc is the source of the function or method name in files (a method
// as "Recv.Name"), with its whitespace collapsed; "" when none declares it.
func funcSrc(t *testing.T, files map[string]string, name string) string {
	t.Helper()
	fset := token.NewFileSet()
	for fname, src := range files {
		f, err := parser.ParseFile(fset, fname, src, 0)
		if err != nil {
			t.Fatalf("%s: %v", fname, err)
		}
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			n := fd.Name.Name
			if fd.Recv != nil {
				rt := fd.Recv.List[0].Type
				if s, ok := rt.(*ast.StarExpr); ok {
					rt = s.X
				}
				n = rt.(*ast.Ident).Name + "." + n
			}
			if n == name {
				var b strings.Builder
				if err := printer.Fprint(&b, fset, fd); err != nil {
					t.Fatal(err)
				}
				return flat(b.String())
			}
		}
	}
	return ""
}

// unwrap are the line breaks and alignment that items writes in a long
// list, which flat takes out.
var unwrap = []struct {
	re *regexp.Regexp
	to string
}{
	{regexp.MustCompile(`,\s*\n\s*([)}])`), "$1"},
	{regexp.MustCompile(`(\(|[\w\]]\{)\s*\n\s*`), "$1"}, // a call's or a literal's, not a block's
	{regexp.MustCompile(`,\s*\n\s*`), ", "},
	{regexp.MustCompile(`(\w):\s{2,}`), "$1: "},
}

// flat is src on one line, as if each list were: its fields joined by single
// spaces, a wrapped list unwrapped and its alignment dropped.
func flat(src string) string {
	for _, u := range unwrap {
		src = u.re.ReplaceAllString(src, u.to)
	}
	return strings.Join(strings.Fields(src), " ")
}

// TestNoPointerCasts: the generated code imports no unsafe and converts no
// pointer: gqlgraph takes and gives its rows as *E in an any.
func TestNoPointerCasts(t *testing.T) {
	cast := regexp.MustCompile(`\(\*[A-Z]\w*\)\(\w+\)`)
	for label, m := range map[string]*plugin.Model{"testdata": modeltest.Model(t), "types": typesModel(t)} {
		for name, src := range render(t, m) {
			if strings.Contains(src, "unsafe") {
				t.Errorf("%s: %s names unsafe", label, name)
			}
			if c := cast.FindString(src); c != "" {
				t.Errorf("%s: %s converts a pointer: %s", label, name, c)
			}
		}
	}
}

// TestEdgesBatch: every edge method reads its edge through gqlgraph's
// batching (EdgeOne, EdgeList or the target's paginate, by its kind, of
// the owner type's Edge of the row and the relation's index), none reads
// the DB itself, and none reads the relation's field, which an
// application's With may have loaded filtered or limited.
func TestEdgesBatch(t *testing.T) {
	for label, m := range map[string]*plugin.Model{"testdata": modeltest.Model(t), "types": typesModel(t)} {
		surf, err := sdl.Build(m)
		if err != nil {
			t.Fatal(err)
		}
		files := render(t, m)
		n := 0
		for _, ty := range surf.Types {
			e := ty.Entity
			for _, ed := range ty.Edges {
				n++
				pe := e.Edges[ed.Edge]
				src := funcSrc(t, files, e.Name+"."+ed.Method)
				// The edge: the owner's type, whose Edges name its
				// field (TestConnSelection), the receiver and the relation.
				edge := typeVar(ty) + ".Edge(" + recv(e) + ", " + edgeConst(e, pe) + ")"
				// The target's type, which reads only the columns its
				// selection needs, and the typed result; a connection is
				// the target's paginate of the edge, with its arguments.
				tt := ed.Target
				want := map[sdl.EdgeKind]string{
					sdl.EdgeConnection: "return " + newConnImpl(tt) + "(ctx, " + edge + ", " + connArgs(tt) + ")",
					sdl.EdgeUnique:     "v, err := gqlgraph.EdgeOne(ctx, &" + typeVar(tt) + ", " + edge + ") n, _ := v.(*" + tt.Entity.Name + ") return n, err",
					sdl.EdgeList:       "v, err := gqlgraph.EdgeList(ctx, &" + typeVar(tt) + ", " + edge + ", 0) n, _ := v.([]*" + tt.Entity.Name + ") return n, err",
				}[ed.Kind]
				if strings.Contains(src, "DBFromContext") {
					t.Errorf("%s: %s.%s reads the DB itself:\n%s", label, e.Name, ed.Method, src)
				}
				if !strings.Contains(src, want) {
					t.Errorf("%s: %s.%s: no %q in\n%s", label, e.Name, ed.Method, want, src)
				}
				if strings.Contains(src, ".Get()") || strings.Contains(src, "LoadedConnection") {
					t.Errorf("%s: %s.%s reads the relation's field:\n%s", label, e.Name, ed.Method, src)
				}
				if strings.Contains(src, "unsafe") {
					t.Errorf("%s: %s.%s converts a pointer:\n%s", label, e.Name, ed.Method, src)
				}
			}
		}
		if n == 0 {
			t.Errorf("%s: no edge", label)
		}
	}
	// A key held in a pointer on the owner's row: a NULL sends no statement.
	src := funcSrc(t, render(t, typesModel(t)), "Event.Owner")
	if i, j := strings.Index(src, "if e.OwnerID == nil { return nil, nil }"), strings.Index(src, "gqlgraph.EdgeOne("); i < 0 || j < i {
		t.Errorf("Event.Owner does not return nil for a NULL key before EdgeOne:\n%s", src)
	}
}

// TestConnSelection: each type's Conn names, in Fields and Edges, every
// field of its GraphQL type, getters and edges included, and reads, in AlwaysRead
// and Fields, no sensitive field's column; AlwaysRead is the key and then the
// foreign keys.
func TestConnSelection(t *testing.T) {
	for label, m := range map[string]*plugin.Model{"testdata": modeltest.Model(t), "types": typesModel(t)} {
		sdlText, err := sdl.Render(m)
		if err != nil {
			t.Fatal(err)
		}
		var others []string
		if label == "testdata" {
			others = append(others, readFile(t, filepath.Join("..", "sdl", "testdata", "app.graphql")))
		}
		schema, err := sdl.Check(sdlText, others...)
		if err != nil {
			t.Fatal(err)
		}
		surf, err := sdl.Build(m)
		if err != nil {
			t.Fatal(err)
		}
		files := render(t, m)
		sensitive := 0
		for _, ty := range surf.Types {
			e := ty.Entity
			lit := connLit(t, files, typeVar(ty))
			if lit == nil {
				t.Errorf("%s: %s: no %s", label, ty.Name, typeVar(ty))
				continue
			}
			var names []string
			cols := slices.Clone(lit["AlwaysRead"])
			for _, kv := range lit["Fields"] {
				col, q, _ := strings.Cut(kv, ": ")
				v, err := strconv.Unquote(q)
				if err != nil {
					t.Fatalf("%s: %s: a field %q", label, ty.Name, kv)
				}
				names = append(names, v)
				cols = append(cols, col)
			}
			// Edges is by relation: an edge's field, which batches it.
			byRel := map[string]string{}
			for _, ed := range ty.Edges {
				byRel[edgeConst(e, e.Edges[ed.Edge])] = ed.Name
			}
			for _, kv := range lit["Edges"] {
				rel, q, _ := strings.Cut(kv, ": ")
				v, err := strconv.Unquote(q)
				if err != nil || byRel[rel] != v {
					t.Fatalf("%s: %s: an edge %q, want one of %v", label, ty.Name, kv, byRel)
				}
				names = append(names, v)
			}
			var want []string
			for _, f := range schema.Types[ty.Name].Fields {
				want = append(want, f.Name)
			}
			slices.Sort(names)
			slices.Sort(want)
			if !slices.Equal(names, want) {
				t.Errorf("%s: %s names %v, want the fields of %s %v", label, typeVar(ty), names, ty.Name, want)
			}
			for _, k := range e.PrimaryKey {
				if !slices.Contains(lit["AlwaysRead"], colConst(e, e.Fields[k])) {
					t.Errorf("%s: %s: AlwaysRead %v lacks the key %s", label, typeVar(ty), lit["AlwaysRead"], e.Fields[k].Name)
				}
			}
			for _, fd := range e.Fields {
				if fd.Sensitive {
					sensitive++
					if slices.Contains(cols, colConst(e, fd)) {
						t.Errorf("%s: %s reads the sensitive %s", label, typeVar(ty), fd.Name)
					}
				}
			}
		}
		if label == "testdata" && sensitive == 0 {
			t.Error("the testdata model has no sensitive field")
		}
	}
	// The key, the foreign keys on the row and the order columns, always, in
	// this order.
	read := connLit(t, render(t, modeltest.Model(t)), "postType")["AlwaysRead"]
	if len(read) < 2 || read[0] != "postColID" || read[1] != "postColAuthorID" {
		t.Errorf("postType's AlwaysRead %v, want the key and then the foreign keys", read)
	}
}

// TestPaginateQueryFilters: an entity's PaginateQuery is newXConnection of
// q with its options' order and where, which hands its source to
// gqlgraph.Paginate with the where input in Args (gqlgraph.WhereOf), and
// Paginate checks the query, filters it and pages it.
func TestPaginateQueryFilters(t *testing.T) {
	for label, m := range map[string]*plugin.Model{"testdata": modeltest.Model(t), "types": typesModel(t)} {
		surf, err := sdl.Build(m)
		if err != nil {
			t.Fatal(err)
		}
		files := render(t, m)
		for _, ty := range surf.Types {
			pq := ty.Entity.Name + ".PaginateQuery"
			src := funcSrc(t, files, pq)
			args := strings.NewReplacer("orderBy", "o.orderBy", "where", "o.where").Replace(connArgs(ty))
			if want := "return " + newConnImpl(ty) + "(ctx, gqlgraph.Query(untyped.FromQuery(q)), " + args + ")"; !strings.Contains(src, want) {
				t.Errorf("%s: %s is not %q:\n%s", label, pq, want, src)
			}
			src = funcSrc(t, files, newConnImpl(ty))
			page := strings.Index(src, "gqlgraph.Paginate(ctx, from, &"+typeVar(ty)+",")
			if page < 0 {
				t.Errorf("%s: %s does not call gqlgraph.Paginate:\n%s", label, newConnImpl(ty), src)
			}
			if w := strings.Index(src, "Where: gqlgraph.WhereOf(where, (*"+ty.Name+"WhereInput).predicate)"); (w >= 0) != (ty.Where != nil) || w > page {
				t.Errorf("%s: %s: the where input not before Paginate:\n%s", label, newConnImpl(ty), src)
			}
		}
	}
}

// TestM2MWithoutInverse: a many-to-many edge needs no inverse edge, since
// the edge method reads the join table through the relation.
func TestM2MWithoutInverse(t *testing.T) {
	entity := func(name string) *plugin.Entity {
		return &plugin.Entity{
			Name: name, Label: strings.ToLower(name), Handles: name + "s", PrimaryKey: []int{0}, Edges: []*plugin.Edge{},
			Fields: []*plugin.Field{
				{Name: "id", GoName: "ID", Type: "int64", GoType: "int64", ElemType: "int64"},
				{Name: "x", GoName: "X", Type: "string", GoType: "string", ElemType: "string"},
			},
		}
	}
	author := entity("Author")
	author.Edges = []*plugin.Edge{{Name: "books", GoName: "Books", Target: "Book", Kind: plugin.M2M, Join: "AuthorBook"}}
	files := render(t, &plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{author, entity("Book")}})
	src := funcSrc(t, files, "Author.Books")
	if !strings.Contains(src, "return newBookConnection(ctx, authorType.Edge(a, authorEdgeBooks), after, first, before, last") {
		t.Errorf("Author.Books:\n%s", src)
	}
}

// TestMutationGodocPoints: the godoc of an input's Setters is short and
// points to "Mutations" in the package documentation, which holds what it
// promises; it does not repeat it. The comment is read with its whitespace
// normalised, since f.doc wraps it. It names the write the setters are for.
func TestMutationGodocPoints(t *testing.T) {
	const pointer = `See "Mutations" in the documentation of package github.com/syssam/velox/contrib/graphql`
	seen := map[string]int{}
	models := map[string]*plugin.Model{"testdata": modeltest.Model(t), "update-only": updateOnlyModel(t), "mutations": mutationsModel(t)}
	for label, m := range models {
		_, funcs, _ := mutationDecls(t, render(t, m))
		for name, fn := range funcs {
			in, kind, ok := strings.Cut(name, ".")
			if !ok || kind != "Setters" {
				continue
			}
			kind = "Update"
			if strings.HasPrefix(in, "Create") {
				kind = "Create"
			}
			seen[kind]++
			doc := strings.Join(strings.Fields(fn.Doc.Text()), " ")
			who := label + " " + in
			if !strings.Contains(doc, pointer) {
				t.Errorf("%s: no pointer in %q", who, doc)
			}
			if n := len(strings.Fields(doc)); n > 120 {
				t.Errorf("%s: %d words, want at most 120", who, n)
			}
			for _, gone := range []string{"savepoint", "written one way", "FOR UPDATE", "clientFoundRows"} {
				if strings.Contains(doc, gone) {
					t.Errorf("%s: %q is the package doc's", who, gone)
				}
			}
			want := "Setters returns the input's fields and edges as setters for db.Insert(x).SetInput."
			if kind == "Update" {
				want = "Setters returns the input's fields and edges as setters for db.Update["
			}
			if !strings.HasPrefix(doc, want) {
				t.Errorf("%s: doc does not start with %q: %q", who, want, doc)
			}
		}
	}
	for _, kind := range []string{"Create", "Update"} {
		if seen[kind] == 0 {
			t.Errorf("no %s method rendered", kind)
		}
	}
}

// TestMutationsHeading: the generated godoc points to "Mutations" in the
// package documentation, so doc.go must keep that heading.
func TestMutationsHeading(t *testing.T) {
	doc := readFile(t, filepath.Join("..", "..", "doc.go"))
	if !strings.Contains(doc, "\n// # Mutations\n") {
		t.Error(`contrib/graphql/doc.go has no "// # Mutations" heading, which the generated godoc of Setters points to`)
	}
}

// TestMethodNameTaken: a field or an edge whose Go name is a method the plugin writes on
// the row is refused here, naming it, where it failed in gqlgen's build.
func TestMethodNameTaken(t *testing.T) {
	for _, name := range []struct{ name, goName string }{{"title_gql", "TitleGQL"}} {
		m := &plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{{
			Name: "Thing", Label: "thing", Handles: "Things", PrimaryKey: []int{0}, Edges: []*plugin.Edge{},
			Fields: []*plugin.Field{
				{Name: "id", GoName: "ID", Type: "int64", GoType: "int64", ElemType: "int64"},
				{Name: "title", GoName: "Title", Type: "string", GoType: "MyTitle", ElemType: "MyTitle", Kind: "string"},
				{Name: name.name, GoName: name.goName, Type: "string", GoType: "string", ElemType: "string", Kind: "string"},
			},
		}}}
		_, err := Render(m, "example.com/app/gen", "gen")
		if err == nil || !strings.Contains(err.Error(), "Thing."+name.goName) || !strings.Contains(err.Error(), name.name) {
			t.Errorf("%s: %v", name.name, err)
		}
	}
}

// TestBytesGetter: a NOT NULL bytes column reads as an empty value, never the nil
// gqlgen refuses in a Bytes!, whatever the driver gives for an empty blob; a
// Nillable one keeps its nil, which is NULL.
func TestBytesGetter(t *testing.T) {
	blob := func(name, goName string, nullable bool) *plugin.Field {
		return &plugin.Field{Name: name, GoName: goName, Type: "bytes", GoType: "[]byte", ElemType: "[]byte", Kind: "slice", Nullable: nullable, Pointer: nullable}
	}
	m := &plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{{
		Name: "Thing", Label: "thing", Handles: "Things", PrimaryKey: []int{0}, Edges: []*plugin.Edge{},
		Fields: []*plugin.Field{
			{Name: "id", GoName: "ID", Type: "int64", GoType: "int64", ElemType: "int64"},
			blob("data", "Data", false),
			blob("opt", "Opt", true),
		},
	}}}
	got := strings.Join(strings.Fields(render(t, m)["thing_gql.go"]), " ")
	for _, want := range []string{"if t.Data == nil {", "return veloxgql.Bytes{}"} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "if x.Opt == nil { return veloxgql.Bytes{}") {
		t.Errorf("a Nillable field lost its null:\n%s", got)
	}
}

// TestPackageNamedAsALocal: the import of an application's package named as a
// local the generated code declares, such as a key named after an input field
// (writerKey), ctx or a clear<Edge> flag, is named <name>pkg, so that the local
// does not shadow it.
func TestPackageNamedAsALocal(t *testing.T) {
	for _, pkg := range []string{"writerKey", "ctx", "tagsJSON", "emailVal", "clearRelated", "idsKeys", "key", "u"} {
		m := &plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{{
			Name: "Thing", Label: "thing", Handles: "Things", PrimaryKey: []int{0}, Edges: []*plugin.Edge{},
			Fields: []*plugin.Field{{Name: "id", GoName: "ID", Type: "int64", GoType: pkg + ".ID", ElemType: pkg + ".ID", PkgPath: "example.com/" + pkg, Kind: "int64", Annotations: map[string]json.RawMessage{"graphql": json.RawMessage(`{"where_ops":1}`)}}},
		}}}
		files := render(t, m)
		want := pkg + `pkg "example.com/` + pkg + `"`
		var n int
		for name, src := range files {
			if strings.Contains(src, `"example.com/`+pkg+`"`) {
				n++
				if !strings.Contains(src, want) {
					t.Errorf("package %s: %s imports it, not as %q:\n%s", pkg, name, want, src)
				}
			}
		}
		if n == 0 {
			t.Errorf("package %s: no file imports it", pkg)
		}
	}
}

// TestNullableOrderTerm: a Nillable order field of another Go type is a
// column of the connection like any other: gqlgraph reads its pointer, NULL
// for nil, and converts its value.
func TestNullableOrderTerm(t *testing.T) {
	thing := &plugin.Entity{Name: "Thing", Label: "thing", Handles: "Things", PrimaryKey: []int{0}, Edges: []*plugin.Edge{},
		Fields: []*plugin.Field{{Name: "id", GoName: "ID", Type: "int64", GoType: "int64", ElemType: "int64"}}}
	m := &plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{withField(thing,
		&plugin.Field{Name: "n", GoName: "N", Type: "int64", GoType: "*types.N", ElemType: "types.N", PkgPath: "example.com/types", Kind: "int64", Pointer: true, Nullable: true, Optional: true,
			Annotations: map[string]json.RawMessage{"graphql": json.RawMessage(`{"order_field":"N"}`)}})}}
	got := strings.Join(strings.Fields(render(t, m)["thing_gql.go"]), " ")
	want := `Order: []gqlgraph.Column{ {Name: "N", Index: thingColN}, },`
	if !strings.Contains(got, want) {
		t.Errorf("no %q in:\n%s", want, got)
	}
}

// TestZeroDefaultNotRefused: a create input that gives a field with a zero
// Default (false, 0, "") its zero value stores what the Default would, so
// Setters does not refuse it; a non-zero Default's zero is still refused.
// A field with graphql.CreateDefault is a value, always set.
func TestZeroDefaultNotRefused(t *testing.T) {
	def := func(name, typ, d string) *plugin.Field {
		return &plugin.Field{Name: name, GoName: plugin.GoName(name), Type: typ, GoType: typ, ElemType: typ, Optional: true, HasDefault: true, Default: json.RawMessage(d)}
	}
	active := &plugin.Field{Name: "active", GoName: "Active", Type: "bool", GoType: "bool", ElemType: "bool"}
	b, err := json.Marshal(graphql.CreateDefault(true))
	if err != nil {
		t.Fatal(err)
	}
	active.Annotations = map[string]json.RawMessage{"graphql": b}
	mut, err := json.Marshal(graphql.Mutations(graphql.MutationCreate))
	if err != nil {
		t.Fatal(err)
	}
	m := &plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{{
		Name: "Thing", Label: "thing", Handles: "Things", PrimaryKey: []int{0}, Edges: []*plugin.Edge{},
		Annotations: map[string]json.RawMessage{"graphql": mut},
		Fields: []*plugin.Field{
			{Name: "id", GoName: "ID", Type: "int64", GoType: "int64", ElemType: "int64", AutoID: true},
			def("off", "bool", "false"), def("views", "int64", "0"), def("score", "float64", "0.0"), def("s", "string", `""`),
			def("handle", "string", `"anon"`), active,
		},
	}}}
	got := strings.Join(strings.Fields(render(t, m)["thing_gql.go"]), " ")
	for _, f := range []string{"Off", "Views", "Score", "S"} {
		if strings.Contains(got, "gqlgraph.NotZero(\"CreateThingInput."+strings.ToLower(f[:1])+f[1:]+"\"") {
			t.Errorf("%s: a zero Default's zero refused:\n%s", f, got)
		}
	}
	for _, want := range []string{`gqlgraph.NotZero("CreateThingInput.handle", *v)`, "Active bool", "set = append(set, Things.Active.Set(in.Active))"} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in:\n%s", want, got)
		}
	}
}
