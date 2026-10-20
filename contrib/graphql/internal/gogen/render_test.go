package gogen

import (
	"encoding/json"
	"flag"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
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

// TestNamesMatchSDL checks the Go against the SDL, as gqlgen's autobind will
// see it: every Go type the plugin writes is named after the SDL type it
// binds to and has a Go field for each of the SDL type's fields; every SDL
// object, input and enum without a Go type of its name, from the plugin or
// velox-gen, is listed here with the reason.
func TestNamesMatchSDL(t *testing.T) {
	m := testModel(t)
	sdlText, err := sdl.Render(m)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := sdl.Check(sdlText, readFile(t, filepath.Join("..", "sdl", "testdata", "app.graphqls")))
	if err != nil {
		t.Fatal(err)
	}
	files := render(t, m)
	d := parseGo(t, files)

	// velox-gen's Go types in the generated package: the entities' structs and
	// the enum types.
	veloxGen := map[string]bool{}
	for _, e := range m.Entities {
		veloxGen[e.Name] = true
	}
	for _, en := range m.Enums {
		veloxGen[en.Type] = true
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
		}
		return ""
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
			if !veloxGen[gt.alias] {
				t.Errorf("%s is an alias of %s, which velox-gen does not declare", name, gt.alias)
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
			// A connection's fields are methods; edges is Task 3's.
			for _, f := range def.Fields {
				if strings.HasSuffix(name, "Connection") && (f.Name == "edges" || f.Name == "pageInfo") {
					continue
				}
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
	// Each Node entity is a veloxgql.Noder, and Node and Nodes are written.
	for _, e := range []string{"Category", "Group", "Post", "User"} {
		var ms []string
		if d.types[e] != nil {
			ms = d.types[e].methods
		}
		if !slices.Contains(ms, "IsNode") || !slices.Contains(ms, "IDGQL") {
			t.Errorf("%s's methods %v: want IsNode and IDGQL", e, ms)
		}
	}
	if like := d.types["Like"]; like != nil && slices.Contains(like.methods, "IsNode") {
		t.Error("Like, whose key is two columns, is a Node")
	}
	if !d.funcs["Node"] || !d.funcs["Nodes"] {
		t.Errorf("funcs %v: want Node and Nodes", d.funcs)
	}
}

// TestGoTypes pins the Go types of where predicates, keys and enums the
// testdata model does not have: a UUID key, the application's own enum type,
// and the scalars veloxgql binds.
func TestGoTypes(t *testing.T) {
	where := func(ops graphql.WhereOp) map[string]json.RawMessage {
		b, err := json.Marshal(graphql.Annotation{WhereOps: ops})
		if err != nil {
			t.Fatal(err)
		}
		return map[string]json.RawMessage{"graphql": b}
	}
	m := &plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{{
		Name: "Event", Label: "event", Handles: "Events", Tenant: -1, PrimaryKey: []int{0}, Edges: []*plugin.Edge{},
		Fields: []*plugin.Field{
			{Name: "id", GoName: "ID", Type: "uuid", GoType: "uuid.UUID", ElemType: "uuid.UUID", PkgPath: "github.com/google/uuid", Annotations: where(graphql.OpsDefault)},
			{Name: "level", GoName: "Level", Type: "enum", GoType: "levels.Level", ElemType: "levels.Level", PkgPath: "example.com/app/types/v2", Enum: "Level", EnumValues: []string{"low", "high"}, Annotations: where(graphql.OpEQ)},
			{Name: "size", GoName: "Size", Type: "uint64", GoType: "uint64", ElemType: "uint64", Annotations: where(graphql.OpGT)},
			{Name: "rank", GoName: "Rank", Type: "int8", GoType: "int8", ElemType: "int8", Annotations: where(graphql.OpIn)},
			{Name: "at", GoName: "At", Type: "time", GoType: "*time.Time", ElemType: "time.Time", PkgPath: "time", Nullable: true, Pointer: true, Annotations: where(graphql.OpLT | graphql.OpIsNil)},
			{Name: "ref", GoName: "Ref", Type: "uuid", GoType: "uuid.UUID", ElemType: "uuid.UUID", PkgPath: "github.com/google/uuid", Annotations: where(graphql.OpNEQ)},
		},
	}}}
	files := render(t, m)
	// Compared with the spaces squashed, as gofmt aligns them.
	squash := func(s string) string { return " " + strings.Join(strings.Fields(s), " ") + " " }
	ev, node := squash(files["gql_event.go"]), squash(files["gql_node.go"])
	for _, want := range []string{
		`func (x *Event) IDGQL() string { return veloxgql.EncodeID("Event", [16]byte(x.ID)) }`,
		" ID *string ", " IDNEQ *string ", " IDIn []string ",
		" Level *levels.Level ",
		" SizeGT *veloxgql.Uint64 ",
		" RankIn []int8 ",
		" AtLT *veloxgql.Time ", " AtIsNil *bool ",
		" RefNEQ *veloxgql.UUID ",
		` levels "example.com/app/types/v2" `,
	} {
		if !strings.Contains(ev, want) {
			t.Errorf("no %q in\n%s", want, ev)
		}
	}
	if strings.Contains(ev, "MarshalGQL") {
		t.Errorf("a marshaler on the application's own enum type:\n%s", ev)
	}
	for _, want := range []string{
		"k, err := veloxgql.DecodeID[[16]byte](id, \"Event\")",
		"Events.ID.EQ(uuid.UUID(k))",
		"keysEvent = append(keysEvent, uuid.UUID(k))",
		"by := make(map[uuid.UUID]*Event, len(rows))",
		` "github.com/google/uuid" `,
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
		"key type": {&plugin.Model{Version: 1, Entities: []*plugin.Entity{entity("Clock", "float64")}}, "schema Clock: its key, of type float64, cannot be a GraphQL ID"},
		"node file": {&plugin.Model{Version: 1, Entities: []*plugin.Entity{func() *plugin.Entity {
			e := entity("Node", "int64")
			e.Annotations = map[string]json.RawMessage{"graphql": json.RawMessage(`{"type":"Vertex"}`)}
			return e
		}()}}, "schema Node"},
		"sdl": {&plugin.Model{Version: 1, Entities: []*plugin.Entity{entity("Query", "int64")}}, "Query"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Render(c.m, outPath, "gen"); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err %v, want one with %q", err, c.want)
			}
		})
	}
}
