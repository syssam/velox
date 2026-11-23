package gogen

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/syssam/velox/contrib/graphql/internal/modeltest"
	"github.com/syssam/velox/contrib/graphql/internal/sdl"
)

// Under compat=veloxe, noder_gql.go makes each type with an ID a Noder, as
// gqlgen's binding of the interface Node needs, and resolves node and nodes
// through gqlgraph; each type's gqlgraph.Type has Veloxe, which gives its
// where input veloxe's semantics. Without it there is neither.
func TestVeloxeNoder(t *testing.T) {
	files, err := RenderConfig(modeltest.Model(t), outPath, "gen", sdl.Config{Veloxe: true, Package: outPath})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range files {
		got[f.Path] = f.Content
	}
	noder := got["noder_gql.go"]
	if _, err := parser.ParseFile(token.NewFileSet(), "noder_gql.go", noder, 0); err != nil {
		t.Fatalf("%v\n%s", err, noder)
	}
	for _, want := range []string{
		"type Noder interface {\n\t// IsNode marks a type of Node.\n\tIsNode()\n}\n",
		"func (*Post) IsNode() {}\n",
		"func (*User) IsNode() {}\n",
		"\t&categoryType,\n\t&groupType,\n\t&postType,\n}\n",
		"func ResolveNode(ctx context.Context, db *velox.DB, id int64) (Noder, error) {\n",
		"func ResolveNodes(ctx context.Context, db *velox.DB, ids []int64) ([]Noder, error) {\n",
		"func NodeTypes() []*gqlgraph.Type { return slices.Clone(nodeTypes) }\n",
	} {
		if !strings.Contains(noder, want) {
			t.Errorf("noder_gql.go lacks %q:\n%s", want, noder)
		}
	}
	if !strings.Contains(strings.Join(strings.Fields(got["post_gql.go"]), " "), " Veloxe: true,") {
		t.Errorf("post_gql.go's postType has no Veloxe:\n%s", got["post_gql.go"])
	}

	def := render(t, modeltest.Model(t))
	if _, ok := def["noder_gql.go"]; ok {
		t.Error("velox's own Go has noder_gql.go")
	}
	for name, src := range def {
		if strings.Contains(src, "Veloxe") || strings.Contains(src, "IsNode") {
			t.Errorf("velox's own %s holds Veloxe or IsNode", name)
		}
	}
}
