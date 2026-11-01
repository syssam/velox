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
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	gqlast "github.com/vektah/gqlparser/v2/ast"

	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/contrib/graphql/internal/english"
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
		checkNames(t, modeltest.Model(t), readFile(t, filepath.Join("..", "sdl", "testdata", "app.graphqls")))
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
	ev, owner, node := squash(files["event_gql.go"]), squash(files["owner_gql.go"]), squash(files["node_gql.go"])
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
	node := render(t, m)["node_gql.go"]
	if !strings.Contains(node, "func Node(_ context.Context, id string) (veloxgql.Noder, error) {") || strings.Contains(node, "DBFromContext(ctx)") {
		t.Errorf("node_gql.go:\n%s", node)
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
		}()}}, "schema Node: its Go file would be node_gql.go"},
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
		// Only a Nillable field is a pointer, and keyset paging needs an
		// order column without NULLs.
		"a pointer order field": {&plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{withField(entity("Thing", "int64"),
			&plugin.Field{Name: "n", GoName: "N", Type: "int64", GoType: "*int64", ElemType: "int64", Pointer: true, Annotations: map[string]json.RawMessage{"graphql": json.RawMessage(`{"order_field":"N"}`)}})}},
			"schema Thing, field n: a connection orders by it, and its Go field is a pointer, which only a Nillable field is"},
		"a create-input field named String": {&plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{creatable(withField(entity("Thing", "int64"),
			&plugin.Field{Name: "text", GoName: "Text", Type: "string", GoType: "string", ElemType: "string", Annotations: map[string]json.RawMessage{"graphql": json.RawMessage(`{"field_name":"string"}`)}}))}},
			"schema Thing: the field string of CreateThingInput is the Go field String, which is the input's method"},
		"create-input fields one to gqlgen": {&plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{creatable(withField(entity("Thing", "int64"),
			&plugin.Field{Name: "ab", GoName: "Ab", Type: "string", GoType: "string", ElemType: "string"},
			&plugin.Field{Name: "other", GoName: "Other", Type: "string", GoType: "string", ElemType: "string", Annotations: map[string]json.RawMessage{"graphql": json.RawMessage(`{"field_name":"aB"}`)}}))}},
			"schema Thing: the fields Ab and AB of CreateThingInput are one Go field to gqlgen"},
		"CreateX is velox-gen's": {&plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{creatable(entity("Thing", "int64")), entity("CreateThing", "int64")}},
			"schema Thing: its Go name CreateThing is also velox-gen's type of schema CreateThing"},
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

// creatable is e with createX, and an auto ID.
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
			if n := strings.Count(src, "const _ = veloxgql.GeneratedCodeIsVersion5\n"); n != 1 {
				t.Errorf("%s: %s has the guard %d times, want once", label, name, n)
			}
			if !strings.Contains(src, `"github.com/syssam/velox/contrib/graphql/veloxgql"`) {
				t.Errorf("%s: %s does not import veloxgql", label, name)
			}
		}
	}
}

// TestEnumErrorPercent: a stored enum value with a % in it is text in the
// generated error, never a format verb of gqlimpl.InputErrorf.
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
	_, rest, ok := strings.Cut(src, "gqlimpl.InputErrorf(")
	if !ok {
		t.Fatalf("group_gql.go calls no gqlimpl.InputErrorf:\n%s", src)
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

// TestConnectionOfEveryNode: every Node entity's file declares its
// <Plural>ConnectionOf, and no file declares or calls a ConnectionGQL.
func TestConnectionOfEveryNode(t *testing.T) {
	const decl = "ConnectionOf(ctx context.Context, q velox.Query["
	for label, m := range map[string]*plugin.Model{"testdata": modeltest.Model(t), "types": typesModel(t)} {
		files := render(t, m)
		total := 0
		for name, src := range files {
			if strings.Contains(src, "ConnectionGQL(") {
				t.Errorf("%s: %s declares or calls a ConnectionGQL", label, name)
			}
			n, of := strings.Count(src, "Connection struct {"), strings.Count(src, decl)
			total += of
			if n != of {
				t.Errorf("%s: %s has %d connections and %d ConnectionOf functions", label, name, n, of)
			}
		}
		if total == 0 {
			t.Errorf("%s: no ConnectionOf at all", label)
		}
	}
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
		case "Event":
			e.Fields[0].HasDefault = true
		case "Owner":
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
					if st != nil || funcs["Create"+ty.Name] != nil {
						t.Errorf("%s has no createX, and %s or Create%s is declared", ty.Name, in, ty.Name)
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
						want = append(want, f.GoName+" []string")
					case sdl.InputEdgeID:
						want = append(want, f.GoName+" "+ptrUnless(f.Required, "string"))
					default:
						// No GoType of these models names a package imported
						// under another name, or the generated one.
						want = append(want, f.GoName+" "+ptrUnless(f.Required, f.GoType))
					}
				}
				if !slices.Equal(got, want) {
					t.Errorf("%s: fields\n %v\nwant\n %v", in, got, want)
				}
				fn := funcs["Create"+ty.Name]
				if fn == nil {
					t.Errorf("no func Create%s", ty.Name)
					continue
				}
				sig := expr(fn.Type)
				if want := "func(ctx context.Context, input " + in + ") (*" + ty.Entity.Name + ", error)"; sig != want {
					t.Errorf("Create%s: %s, want %s", ty.Name, sig, want)
				}
			}
			if n == 0 {
				t.Error("no type has createX")
			}
		})
	}
}

// mutationDecls are files' struct types and funcs, by name, and how to
// print an expression of them.
func mutationDecls(t *testing.T, files map[string]string) (map[string]*ast.StructType, map[string]*ast.FuncDecl, func(ast.Expr) string) {
	t.Helper()
	fset := token.NewFileSet()
	structs, funcs := map[string]*ast.StructType{}, map[string]*ast.FuncDecl{}
	for name, src := range files {
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			switch decl := decl.(type) {
			case *ast.FuncDecl:
				if decl.Recv == nil {
					funcs[decl.Name.Name] = decl
				}
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

// updateOnlyModel is typesModel with updateX and no createX on both types:
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

// TestUpdateInputs: each type with updateX has Update<T>Input, with one Go
// field for each of the Surface's InputFields, in order: a pointer for a
// value or a unique edge's ID, a []string for an ID list and a bool for a
// clear; and Update<T>, which takes the ID and the input and returns the
// entity. A type with updateX and no createX still has <entity>FieldGQL,
// which its UpdateX names.
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
				in, fn := "Update"+ty.Name+"Input", "Update"+ty.Name
				st := structs[in]
				if ty.Update == nil {
					if st != nil || funcs[fn] != nil {
						t.Errorf("%s has no updateX, and %s or %s is declared", ty.Name, in, fn)
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
						want = append(want, f.GoName+" []string")
					case sdl.InputClear, sdl.InputClearEdge:
						want = append(want, f.GoName+" bool")
					case sdl.InputEdgeID:
						want = append(want, f.GoName+" *string")
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
				if sig, want := expr(d.Type), "func(ctx context.Context, id string, input "+in+") (*"+ty.Entity.Name+", error)"; sig != want {
					t.Errorf("%s: %s, want %s", fn, sig, want)
				}
				gql := strings.ToLower(ty.Entity.Name[:1]) + ty.Entity.Name[1:] + "FieldGQL"
				if funcs[gql] == nil {
					t.Errorf("no func %s, which %s names", gql, fn)
				}
				if src := files[ty.Entity.Label+"_gql.go"]; !strings.Contains(src, ", "+gql+")") {
					t.Errorf("%s does not name %s", fn, gql)
				}
			}
			if n == 0 {
				t.Error("no type has updateX")
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

// TestCreateConversions pins how CreateX turns its input into the entity
// for mutationsModel's Go types: IDs decoded as the target's key and
// converted to the application's key type, JSON decoded before any
// statement, the application's own Go types converted from the scalar's.
func TestCreateConversions(t *testing.T) {
	files := render(t, mutationsModel(t))
	squash := func(s string) string { return " " + strings.Join(strings.Fields(s), " ") + " " }
	ev, owner := squash(files["event_gql.go"]), squash(files["owner_gql.go"])
	for _, want := range []string{
		` OwnerID *string `,
		` Tags veloxgql.JSON `,
		`gqlimpl.DecodeIDAs[int64, idspkg.UserID]("CreateEventInput.ownerID", *input.OwnerID, "Owner", gqlimpl.AsInt[idspkg.UserID, int64])`,
		`gqlimpl.FromJSON[[]string]("CreateEventInput.tags", input.Tags)`,
		`gqlimpl.CheckExists(ctx, db.Query[Owner]().Where(Owners.ID.EQ(k`,
		`), 1, "CreateEventInput.ownerID", "Owner")`,
		`return gqlimpl.WriteError(err, "CreateEventInput", "gen.Event", eventFieldGQL)`,
		`case "Tags": return "tags"`,
	} {
		if !strings.Contains(ev, want) {
			t.Errorf("no %q in\n%s", want, ev)
		}
	}
	for _, want := range []string{
		` Email *string `, ` Code string `, ` Rank int32 `,
		`x.Code = idspkg.Code(input.Code)`,
		`v0 := idspkg.Email(*input.Email) x.Email = &v0`,
		`x.Rank = idspkg.Rank(input.Rank)`,
		`x.Ref = idspkg.Ref(input.Ref)`,
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
}

// TestDeleteFunc: each type with deleteX has DeleteX, taking the ID and
// returning it; a type without it has none.
func TestDeleteFunc(t *testing.T) {
	for label, m := range map[string]*plugin.Model{"testdata": modeltest.Model(t), "update-only": updateOnlyModel(t), "mutations": mutationsModel(t)} {
		t.Run(label, func(t *testing.T) {
			surf, err := sdl.Build(m)
			if err != nil {
				t.Fatal(err)
			}
			_, funcs, expr := mutationDecls(t, render(t, m))
			with, without := 0, 0
			for _, ty := range surf.Types {
				fn := "Delete" + ty.Name
				d := funcs[fn]
				if !ty.Delete {
					without++
					if d != nil {
						t.Errorf("%s has no deleteX, and %s is declared", ty.Name, fn)
					}
					continue
				}
				with++
				if d == nil {
					t.Errorf("no func %s", fn)
					continue
				}
				if sig, want := expr(d.Type), "func(ctx context.Context, id string) (string, error)"; sig != want {
					t.Errorf("%s: %s, want %s", fn, sig, want)
				}
			}
			t.Logf("%d types with deleteX, %d without", with, without)
			if label == "testdata" && (with == 0 || without == 0) {
				t.Errorf("%d types with deleteX, %d without: want some of each", with, without)
			}
		})
	}
}

// TestDeleteJoinRows pins the join rows DeleteX deletes: those of each M2M
// edge's join entity, from either side of a pair, once per join entity, and
// both columns of a join of the entity to itself; each after a locking
// existence check under the DELETE's scope (Writable), and before the
// DELETE of the row.
func TestDeleteJoinRows(t *testing.T) {
	m := modeltest.Model(t)
	var user, group *plugin.Entity
	for _, e := range m.Entities {
		switch e.Name {
		case "User":
			user = e
		case "Group":
			group = e
		}
	}
	setMutations(t, user, "create", "delete")
	setMutations(t, group, "delete")
	// Follow joins User to itself: follows and its inverse, followers.
	m.Entities = append(m.Entities, &plugin.Entity{
		Name: "Follow", Label: "follow", Handles: "Follows", Table: "follows", Join: true, Tenant: -1, PrimaryKey: []int{0, 1},
		Fields: []*plugin.Field{
			{Name: "follower_id", GoName: "FollowerID", Column: "follower_id", Type: "int64", GoType: "int64", ElemType: "int64"},
			{Name: "followee_id", GoName: "FolloweeID", Column: "followee_id", Type: "int64", GoType: "int64", ElemType: "int64"},
		},
	})
	user.Edges = append(user.Edges,
		&plugin.Edge{Name: "follows", GoName: "Follows", Target: "User", Ref: "followers", Kind: plugin.M2M, Join: "Follow"},
		&plugin.Edge{Name: "followers", GoName: "Followers", Target: "User", Ref: "follows", Kind: plugin.M2M, Join: "Follow", Inverse: true},
	)
	files := render(t, m)
	squash := func(s string) string { return " " + strings.Join(strings.Fields(s), " ") + " " }
	for file, wants := range map[string][]string{
		"user_gql.go": {
			`func DeleteAccount(ctx context.Context, id string) (string, error) {`,
			`chk := db.Query[User]().Where(Users.ID.EQ(key)).Writable() if db.Dialect() != velox.SQLite { chk = chk.ForUpdate() } if ok, err := chk.Exists(ctx);`,
			`return gqlimpl.NotFound("deleteAccount", "Account")`,
			`db.Delete[UserGroup]().Where(UserGroups.UserID.EQ(key)).Exec(ctx)`,
			`db.Delete[Follow]().Where(velox.Or(Follows.FollowerID.EQ(key), Follows.FolloweeID.EQ(key))).Exec(ctx)`,
			`db.Delete[User]().Where(Users.ID.EQ(key)).Exec(ctx)`,
			`return veloxgql.EncodeID("Account", key), nil`,
		},
		"group_gql.go": {
			`chk := db.Query[Group]().Where(Groups.ID.EQ(key)).Writable() if db.Dialect() != velox.SQLite { chk = chk.ForUpdate() } if ok, err := chk.Exists(ctx);`,
			`db.Delete[UserGroup]().Where(UserGroups.GroupID.EQ(key)).Exec(ctx)`,
			`db.Delete[Group]().Where(Groups.ID.EQ(key)).Exec(ctx)`,
		},
	} {
		src := squash(files[file])
		at := -1
		for _, w := range wants {
			i := strings.Index(src, w)
			if i < 0 {
				t.Errorf("%s: no %q in\n%s", file, w, src)
				continue
			}
			if i < at {
				t.Errorf("%s: %q is out of order", file, w)
			}
			at = i
			if strings.HasPrefix(w, "db.") && strings.Count(src, w) != 1 {
				t.Errorf("%s: %q %d times, want once", file, w, strings.Count(src, w))
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

// TestUpdateEdgeToRenamedType: UpdateX writes the edges of an M2M edge
// whose target's GraphQL type is renamed (Group.users to User, the type
// Account), finding the target by its entity, not by its type's name.
func TestUpdateEdgeToRenamedType(t *testing.T) {
	m := modeltest.Model(t)
	for _, e := range m.Entities {
		if e.Name == "Group" {
			setMutations(t, e, "update")
		}
	}
	src := strings.Join(strings.Fields(render(t, m)["group_gql.go"]), " ")
	for _, w := range []string{
		`func UpdateGroup(ctx context.Context, id string, input UpdateGroupInput) (*Group, error) {`,
		`db.Delete[UserGroup]().Where(UserGroups.GroupID.EQ(key), UserGroups.UserID.In(`,
		`db.Query[User]().Where(Users.ID.In(`,
	} {
		if !strings.Contains(src, w) {
			t.Errorf("group_gql.go: no %q in\n%s", w, src)
		}
	}
}

// TestUpdateLocksTheRow: UpdateX and DeleteX of an entity with an M2M edge
// check their row with a Writable Exists, locked with ForUpdate unless the
// dialect is SQLite, and no longer with a Count, which cannot lock.
func TestUpdateLocksTheRow(t *testing.T) {
	m := modeltest.Model(t)
	for _, e := range m.Entities {
		switch e.Name {
		case "Group":
			setMutations(t, e, "update", "delete")
		case "User":
			setMutations(t, e, "delete")
		}
	}
	files := render(t, m)
	const lock = `if db.Dialect() != velox.SQLite { chk = chk.ForUpdate() } if ok, err := chk.Exists(ctx); err != nil {`
	for _, fn := range []string{"UpdateGroup", "DeleteGroup", "DeleteAccount"} {
		src := funcSrc(t, files, fn)
		if src == "" {
			t.Errorf("no %s", fn)
			continue
		}
		if !strings.Contains(src, lock) || !strings.Contains(src, ".Writable() ") {
			t.Errorf("%s does not lock its row before its edges:\n%s", fn, src)
		}
		if strings.Contains(src, "Writable().Count") {
			t.Errorf("%s still checks its row with a Count:\n%s", fn, src)
		}
	}
}

// colsLiteral is the elements of the cols literal of the function name in
// files, as source: Posts.ID, Posts.AuthorID, ...; nil when it has none.
func colsLiteral(t *testing.T, files map[string]string, name string) []string {
	t.Helper()
	fset := token.NewFileSet()
	for fname, src := range files {
		f, err := parser.ParseFile(fset, fname, src, 0)
		if err != nil {
			t.Fatalf("%s: %v", fname, err)
		}
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Name.Name != name || fd.Recv != nil {
				continue
			}
			for _, st := range fd.Body.List {
				as, ok := st.(*ast.AssignStmt)
				if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
					continue
				}
				if id, ok := as.Lhs[0].(*ast.Ident); !ok || id.Name != "cols" {
					continue
				}
				lit, ok := as.Rhs[0].(*ast.CompositeLit)
				if !ok {
					continue
				}
				var out []string
				for _, el := range lit.Elts {
					var b strings.Builder
					if err := printer.Fprint(&b, fset, el); err != nil {
						t.Fatal(err)
					}
					out = append(out, b.String())
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
				return strings.Join(strings.Fields(b.String()), " ")
			}
		}
	}
	return ""
}

// TestEdgesBatch: every edge method reads its edge through gqlimpl's
// batching (EdgeConnection, EdgeOne or EdgeList, by its kind, with the
// owner's relation handle), and none reads the relation's field, which an
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
				// The edge's GraphQL field, which batches it, and a
				// connection's arguments, which its batch keeps.
				field := "\"" + ty.Name + "." + ed.Name + "\""
				want := map[sdl.EdgeKind]string{
					sdl.EdgeConnection: "gqlimpl.EdgeConnection(ctx, c.c, x, " + e.Handles + "." + pe.GoName + ", " + field + ", []any{" + connArgs(ed.Target) + "})",
					sdl.EdgeUnique:     "gqlimpl.EdgeOne(ctx, q, x, " + e.Handles + "." + pe.GoName + ", " + field + ")",
					sdl.EdgeList:       "gqlimpl.EdgeList(ctx, q, x, " + e.Handles + "." + pe.GoName + ", " + field + ")",
				}[ed.Kind]
				if !strings.Contains(src, want) {
					t.Errorf("%s: %s.%s: no %q in\n%s", label, e.Name, ed.Method, want, src)
				}
				if strings.Contains(src, ".Get()") || strings.Contains(src, "LoadedConnection") {
					t.Errorf("%s: %s.%s reads the relation's field:\n%s", label, e.Name, ed.Method, src)
				}
				if ed.Kind != sdl.EdgeConnection {
					// The target's query reads only the columns selected.
					tt := ed.Target
					sel := "if fields, ok := gqlimpl.Selected(ctx, \"" + tt.Name + "\"); ok { q = gqlimpl.Narrow(q, " + lowerFirst(tt.Entity.Name) + "ColumnsGQL(fields)) }"
					if !strings.Contains(src, sel) {
						t.Errorf("%s: %s.%s: no %q in\n%s", label, e.Name, ed.Method, sel, src)
					}
				}
			}
		}
		if n == 0 {
			t.Errorf("%s: no edge", label)
		}
	}
	// A key held in a pointer on the owner's row: a NULL sends no statement.
	src := funcSrc(t, render(t, typesModel(t)), "Event.OwnerGQL")
	if i, j := strings.Index(src, "if x.OwnerID == nil { return nil, nil }"), strings.Index(src, "gqlimpl.EdgeOne("); i < 0 || j < i {
		t.Errorf("Event.OwnerGQL does not return nil for a NULL key before EdgeOne:\n%s", src)
	}
}

// TestColumnsGQL: each type has <label>ColumnsGQL, whose switch lists every
// field of its GraphQL type, getters and edges included, and whose columns
// include no sensitive field's.
func TestColumnsGQL(t *testing.T) {
	for label, m := range map[string]*plugin.Model{"testdata": modeltest.Model(t), "types": typesModel(t)} {
		sdlText, err := sdl.Render(m)
		if err != nil {
			t.Fatal(err)
		}
		var others []string
		if label == "testdata" {
			others = append(others, readFile(t, filepath.Join("..", "sdl", "testdata", "app.graphqls")))
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
			name := lowerFirst(e.Name) + "ColumnsGQL"
			src := funcSrc(t, files, name)
			if !strings.HasPrefix(src, "func "+name+"(fields []string) []velox.Column["+e.Name+"] {") {
				t.Errorf("%s: %s: no %s:\n%s", label, ty.Name, name, src)
				continue
			}
			var cases []string
			for _, c := range strings.Split(src, "case ")[1:] {
				list, _, _ := strings.Cut(c, ":")
				for q := range strings.SplitSeq(list, ", ") {
					v, err := strconv.Unquote(q)
					if err != nil {
						t.Fatalf("%s: %s: a case %q", label, name, q)
					}
					cases = append(cases, v)
				}
			}
			var want []string
			for _, f := range schema.Types[ty.Name].Fields {
				want = append(want, f.Name)
			}
			slices.Sort(cases)
			slices.Sort(want)
			if !slices.Equal(cases, want) {
				t.Errorf("%s: %s's cases %v, want the fields of %s %v", label, name, cases, ty.Name, want)
			}
			if !strings.Contains(src, "default: return nil") {
				t.Errorf("%s: %s: another field does not read every column:\n%s", label, name, src)
			}
			cols := colsLiteral(t, files, name)
			if len(cols) == 0 {
				t.Errorf("%s: %s: no cols literal:\n%s", label, name, src)
			}
			for _, k := range e.PrimaryKey {
				if !slices.Contains(cols, e.Handles+"."+e.Fields[k].GoName) {
					t.Errorf("%s: %s: cols %v lack the key %s", label, name, cols, e.Fields[k].Name)
				}
			}
			if e.Tenant >= 0 && !slices.Contains(cols, e.Handles+"."+e.Fields[e.Tenant].GoName) {
				t.Errorf("%s: %s: cols %v lack the tenant column %s", label, name, cols, e.Fields[e.Tenant].Name)
			}
			for _, fd := range e.Fields {
				if fd.Sensitive {
					sensitive++
					if slices.Contains(cols, e.Handles+"."+fd.GoName) || regexp.MustCompile(`\b`+e.Handles+`\.`+fd.GoName+`\b`).MatchString(src) {
						t.Errorf("%s: %s reads the sensitive %s:\n%s", label, name, fd.Name, src)
					}
				}
			}
		}
		if label == "testdata" && sensitive == 0 {
			t.Error("the testdata model has no sensitive field")
		}
	}
	// The key, the foreign keys on the row and the order columns, always, in
	// this order; a tenant column too, so that a row handed to an
	// application's resolver never has a zero tenant.
	cols := colsLiteral(t, render(t, modeltest.Model(t)), "postColumnsGQL")
	if len(cols) < 2 || cols[0] != "Posts.ID" || cols[1] != "Posts.AuthorID" {
		t.Errorf("postColumnsGQL's cols %v, want the key and then the foreign keys", cols)
	}
}

// TestConnectionOfChecksSelects: XConnectionOf refuses a base query that
// does not select the key or an orderBy column, and narrows its columns to
// the selection, before anything else.
func TestConnectionOfChecksSelects(t *testing.T) {
	for label, m := range map[string]*plugin.Model{"testdata": modeltest.Model(t), "types": typesModel(t)} {
		surf, err := sdl.Build(m)
		if err != nil {
			t.Fatal(err)
		}
		files := render(t, m)
		for _, ty := range surf.Types {
			src := funcSrc(t, files, connFunc(ty))
			sel, narrow, conn := strings.Index(src, "q.Selects("), strings.Index(src, "gqlimpl.Narrow(q, "+lowerFirst(ty.Entity.Name)+"ColumnsGQL(fields))"), strings.Index(src, "gqlimpl.NewConnection(")
			if sel < 0 || narrow < sel || conn < narrow {
				t.Errorf("%s: %s does not check q.Selects, then narrow, before NewConnection:\n%s", label, connFunc(ty), src)
			}
			if w := strings.Index(src, "where.Pred("); w >= 0 && w < narrow {
				t.Errorf("%s: %s: the where input before the check:\n%s", label, connFunc(ty), src)
			}
			if ty.Order != nil && !strings.Contains(src, "which orderBy names") {
				t.Errorf("%s: %s does not check orderBy's columns:\n%s", label, connFunc(ty), src)
			}
		}
	}
}

// TestM2MWithoutInverse: a many-to-many edge needs no inverse edge, since
// the edge method reads the join table through the relation.
func TestM2MWithoutInverse(t *testing.T) {
	entity := func(name string) *plugin.Entity {
		return &plugin.Entity{
			Name: name, Label: strings.ToLower(name), Handles: name + "s", Tenant: -1, PrimaryKey: []int{0}, Edges: []*plugin.Edge{},
			Fields: []*plugin.Field{
				{Name: "id", GoName: "ID", Type: "int64", GoType: "int64", ElemType: "int64"},
				{Name: "x", GoName: "X", Type: "string", GoType: "string", ElemType: "string"},
			},
		}
	}
	author := entity("Author")
	author.Edges = []*plugin.Edge{{Name: "books", GoName: "Books", Target: "Book", Kind: plugin.M2M, Join: "AuthorBook"}}
	files := render(t, &plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{author, entity("Book")}})
	src := funcSrc(t, files, "Author.BooksGQL")
	if !strings.Contains(src, "gqlimpl.EdgeConnection(ctx, c.c, x, Authors.Books, \"Author.books\", []any{after, first, before, last") {
		t.Errorf("Author.BooksGQL:\n%s", src)
	}
}

// deleteOnlyModel is typesModel with deleteX and no createX or updateX on
// both types, which have no many-to-many edge.
func deleteOnlyModel(t *testing.T) *plugin.Model {
	t.Helper()
	m := typesModel(t)
	b, err := json.Marshal(graphql.Mutations(graphql.MutationDelete))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range m.Entities {
		e.Annotations = map[string]json.RawMessage{"graphql": b}
	}
	return m
}

// TestMutationGodocPoints: the godoc of Create<T>, Update<T> and Delete<T>
// is short and points to "Mutations" in the package documentation, which
// holds what they promise; it does not repeat it. The comment is read with
// its whitespace normalised, since f.doc wraps it. Create<T> and Update<T>
// name the type with its article ("an Account", "a Post"), and Delete<T>
// names the join rows exactly when the type has a many-to-many edge.
func TestMutationGodocPoints(t *testing.T) {
	const pointer = `See "Mutations" in the documentation of package github.com/syssam/velox/contrib/graphql`
	seen := map[string]int{}
	article := map[string]string{}
	joinRows := map[string]bool{}
	models := map[string]*plugin.Model{"testdata": modeltest.Model(t), "update-only": updateOnlyModel(t), "mutations": mutationsModel(t), "delete-only": deleteOnlyModel(t)}
	for label, m := range models {
		for name, src := range render(t, m) {
			f, err := parser.ParseFile(token.NewFileSet(), name, src, parser.ParseComments)
			if err != nil {
				t.Fatal(err)
			}
			for _, decl := range f.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv != nil {
					continue
				}
				var kind string
				for _, k := range []string{"Create", "Update", "Delete"} {
					if strings.HasPrefix(fn.Name.Name, k) {
						kind = k
					}
				}
				if kind == "" {
					continue
				}
				seen[kind]++
				doc := strings.Join(strings.Fields(fn.Doc.Text()), " ")
				who := label + " " + fn.Name.Name
				if !strings.Contains(doc, pointer) {
					t.Errorf("%s: no pointer in %q", who, doc)
				}
				if n := len(strings.Fields(doc)); n > 80 {
					t.Errorf("%s: %d words, want at most 80", who, n)
				}
				for _, gone := range []string{"savepoint", "written one way", "FOR UPDATE", "clientFoundRows"} {
					if strings.Contains(doc, gone) {
						t.Errorf("%s: %q is the package doc's", who, gone)
					}
				}
				typ := strings.TrimPrefix(fn.Name.Name, kind)
				switch kind {
				case "Create":
					article[who] = strings.Fields(strings.TrimPrefix(doc, fn.Name.Name+" creates "))[0] + " " + typ
					if want := fn.Name.Name + " creates " + english.Article(typ) + " " + typ + " from input"; !strings.HasPrefix(doc, want) {
						t.Errorf("%s: doc does not start with %q: %q", who, want, doc)
					}
				case "Delete":
					joinRows[label+" "+fn.Name.Name] = strings.Contains(doc, "join rows")
				}
			}
		}
	}
	for _, kind := range []string{"Create", "Update", "Delete"} {
		if seen[kind] == 0 {
			t.Errorf("no %s function rendered", kind)
		}
	}
	// The articles themselves, spelled out: a vowel-initial type takes "an".
	for who, want := range map[string]string{
		"testdata CreateAccount": "an Account", "testdata CreatePost": "a Post",
		"mutations CreateEvent": "an Event", "mutations CreateOwner": "an Owner",
	} {
		if article[who] != want {
			t.Errorf("%s: article and type %q, want %q", who, article[who], want)
		}
	}
	for who, want := range map[string]bool{"testdata DeletePost": true, "delete-only DeleteEvent": false, "delete-only DeleteOwner": false} {
		if got, ok := joinRows[who]; !ok || got != want {
			t.Errorf("%s: godoc names join rows = %v (rendered %v), want %v", who, got, ok, want)
		}
	}
}

// TestMutationsHeading: the generated godoc points to "Mutations" in the
// package documentation, so doc.go must keep that heading.
func TestMutationsHeading(t *testing.T) {
	doc := readFile(t, filepath.Join("..", "..", "doc.go"))
	if !strings.Contains(doc, "\n// # Mutations\n") {
		t.Error(`contrib/graphql/doc.go has no "// # Mutations" heading, which the generated godoc of Create<T>, Update<T> and Delete<T> points to`)
	}
}

// TestSingleOwnerKeyEQ: the existence check of one owner key is EQ, and only
// a list of keys is In.
func TestSingleOwnerKeyEQ(t *testing.T) {
	files := render(t, modeltest.Model(t))
	in := regexp.MustCompile(`\.In\(k\d+\)\), 1,`)
	for name, src := range files {
		if in.MatchString(src) {
			t.Errorf("%s checks one owner key with In", name)
		}
	}
	const want = `Users.ID.EQ(k7)), 1, "CreatePostInput.writerID"`
	if !strings.Contains(files["post_gql.go"], want) {
		t.Errorf("post_gql.go lacks %s", want)
	}
}

// TestRepointO2OOrder: re-pointing a one-to-one edge from the owner, whose
// key the target holds, checks the new child first (CheckExists), then lets
// the current child go, then refuses the re-point when a child the caller may
// not change still holds the key (StillLinked), and only then links the new
// child.
func TestRepointO2OOrder(t *testing.T) {
	idField := func() *plugin.Field {
		return &plugin.Field{Name: "id", GoName: "ID", Type: "int64", GoType: "int64", ElemType: "int64", AutoID: true}
	}
	owner := &plugin.Entity{
		Name: "Owner", Label: "owner", Handles: "Owners", Tenant: -1, PrimaryKey: []int{0},
		Fields:      []*plugin.Field{idField()},
		Edges:       []*plugin.Edge{{Name: "pet", GoName: "Pet", Target: "Pet", Ref: "owner", Kind: plugin.O2O, Unique: true, Field: "owner_pet", FieldOn: "Pet"}},
		Annotations: map[string]json.RawMessage{"graphql": json.RawMessage(`{"mutations":["update"]}`)},
	}
	pet := &plugin.Entity{
		Name: "Pet", Label: "pet", Handles: "Pets", Tenant: -1, PrimaryKey: []int{0},
		Fields: []*plugin.Field{idField(),
			{Name: "owner_pet", GoName: "OwnerPet", Type: "int64", GoType: "*int64", ElemType: "int64", Pointer: true, Nullable: true, Optional: true, Implicit: "pet"}},
		Edges: []*plugin.Edge{{Name: "owner", GoName: "Owner", Target: "Owner", Ref: "pet", Kind: plugin.O2O, Unique: true, Inverse: true, Field: "owner_pet", FieldOn: "Pet", OwnsKey: true}},
	}
	src := funcSrc(t, render(t, &plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{owner, pet}}), "UpdateOwner")
	steps := []string{
		`gqlimpl.CheckExists(ctx, db.Query[Pet]().Where(Pets.ID.EQ(`,
		`db.Update[Pet]().Set(Pets.OwnerPet.SetNull()).Where(Pets.OwnerPet.EQ(key), Pets.ID.NEQ(`,
		`gqlimpl.StillLinked(ctx, db.Query[Pet]().Where(Pets.OwnerPet.EQ(key), Pets.ID.NEQ(`,
		`db.Update[Pet]().Set(Pets.OwnerPet.Set(key)).Where(Pets.ID.EQ(`,
	}
	at := -1
	for _, s := range steps {
		i := strings.Index(src, s)
		if i < 0 {
			t.Fatalf("UpdateOwner has no %s:\n%s", s, src)
		}
		if i < at {
			t.Errorf("UpdateOwner: %s comes before the step ahead of it:\n%s", s, src)
		}
		at = i
	}
}
