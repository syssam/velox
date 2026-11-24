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
		"func ResolveNode(ctx context.Context, db *velox.DB, id int64, opts ...NodeOption) (Noder, error) {\n",
		"\tn, err := gqlgraph.ResolveNode(ctx, db, nodeTypes, id, opts...)\n",
		"func ResolveNodes(ctx context.Context, db *velox.DB, ids []int64, opts ...NodeOption) ([]Noder, error) {\n",
		"type NodeOption = gqlgraph.NodeOption\n",
		"func WithNodeType(fn func(ctx context.Context, id int64) (string, error)) NodeOption {\n\treturn gqlgraph.WithNodeType(fn)\n}\n",
		"func WithFixedNodeType(name string) NodeOption { return gqlgraph.WithFixedNodeType(name) }\n",
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

// node=on writes noder_gql.go outside compat, and its type literals keep
// velox's own semantics: no Veloxe. compat=veloxe,node=off writes no
// noder_gql.go and keeps veloxe's Veloxe (ADR 0152).
func TestNodeOption(t *testing.T) {
	for _, c := range []struct {
		cfg           sdl.Config
		noder, veloxe bool
	}{
		{sdl.Config{Node: "on", Package: outPath}, true, false},
		{sdl.Config{Veloxe: true, Node: "off", Package: outPath}, false, true},
		{sdl.Config{Node: "off"}, false, false},
	} {
		files, err := RenderConfig(modeltest.Model(t), outPath, "gen", c.cfg)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]string{}
		for _, f := range files {
			got[f.Path] = f.Content
		}
		noder, ok := got["noder_gql.go"]
		if ok != c.noder || ok && !strings.Contains(noder, "func WithFixedNodeType(name string) NodeOption") {
			t.Errorf("%+v: noder_gql.go %v, want %v:\n%s", c.cfg, ok, c.noder, noder)
		}
		if v := strings.Contains(strings.Join(strings.Fields(got["post_gql.go"]), " "), " Veloxe: true,"); v != c.veloxe {
			t.Errorf("%+v: post_gql.go's Veloxe %v, want %v", c.cfg, v, c.veloxe)
		}
	}
}
