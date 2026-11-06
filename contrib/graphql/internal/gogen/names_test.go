package gogen

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/contrib/graphql/internal/modeltest"
	"github.com/syssam/velox/plugin"
)

// TestNoUnusedFunc: every function the plugin writes outside a method is
// called somewhere in the package. <label>KeyGQL was written for every Node
// type and called nowhere.
func TestNoUnusedFunc(t *testing.T) {
	for label, m := range map[string]*plugin.Model{"testdata": modeltest.Model(t), "types": typesModel(t)} {
		files := render(t, m)
		uses := map[string]int{}
		var funcs []string
		for name, src := range files {
			f, err := parser.ParseFile(token.NewFileSet(), name, src, parser.SkipObjectResolution)
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range f.Decls {
				if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil && !fd.Name.IsExported() {
					funcs = append(funcs, fd.Name.Name)
				}
			}
			ast.Inspect(f, func(n ast.Node) bool {
				if id, ok := n.(*ast.Ident); ok {
					uses[id.Name]++
				}
				return true
			})
		}
		for _, fn := range funcs {
			if uses[fn] < 2 { // its declaration alone
				t.Errorf("%s: %s is written and never called", label, fn)
			}
		}
	}
}

// TestEdgeMethodTaken: an edge's method is its Go name, so an edge named as
// a field of the row, or as a method velox-gen writes on it, is refused,
// naming both, where gqlgen's build would fail. The edge's GraphQL field is
// renamed, so the SDL has no clash of its own.
func TestEdgeMethodTaken(t *testing.T) {
	rename, err := json.Marshal(graphql.FieldName("holder"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ edge, goName, other string }{
		{"owner", "Owner", "velox-gen's field owner"},
		{"string", "String", "velox-gen's method String"},
		{"edges", "Edges", "velox-gen's field Edges"},
	} {
		thing := &plugin.Entity{
			Name: "Thing", Label: "thing", Handles: "Things", PrimaryKey: []int{0},
			Fields: []*plugin.Field{
				{Name: "id", GoName: "ID", Type: "int64", GoType: "int64", ElemType: "int64"},
				{Name: "owner", GoName: "Owner", Type: "string", GoType: "string", ElemType: "string", Kind: "string"},
				{Name: "user_id", GoName: "UserID", Type: "int64", GoType: "int64", ElemType: "int64"},
			},
			Edges: []*plugin.Edge{{Name: c.edge, GoName: c.goName, Target: "User", Kind: plugin.M2O, Unique: true,
				Field: "user_id", FieldOn: "Thing", OwnsKey: true, Annotations: map[string]json.RawMessage{"graphql": rename}}},
		}
		user := &plugin.Entity{
			Name: "User", Label: "user", Handles: "Users", PrimaryKey: []int{0}, Edges: []*plugin.Edge{},
			Fields: []*plugin.Field{{Name: "id", GoName: "ID", Type: "int64", GoType: "int64", ElemType: "int64"}},
		}
		_, err := Render(&plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{thing, user}}, "example.com/app/gen", "gen")
		if err == nil || !strings.Contains(err.Error(), "Thing."+c.goName) || !strings.Contains(err.Error(), c.other) {
			t.Errorf("edge %s: %v", c.edge, err)
		}
	}
}
