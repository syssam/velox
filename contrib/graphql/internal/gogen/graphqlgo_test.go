package gogen

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syssam/graphql-go"

	"github.com/syssam/velox/contrib/graphql/internal/modeltest"
	"github.com/syssam/velox/contrib/graphql/internal/sdl"
)

// graphqlGoConfig is the Config of engine=graphql-go.
var graphqlGoConfig = sdl.Config{GraphQLGo: true, Package: outPath}

// TestGraphQLGoGolden pins the Go of engine=graphql-go over the test model
// less what the engine refuses. Run it with -update to rewrite
// testdata/graphqlgo.
func TestGraphQLGoGolden(t *testing.T) {
	files, err := RenderConfig(modeltest.GraphQLGo(t), outPath, "gen", graphqlGoConfig)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join("testdata", "graphqlgo")
	if *update {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if err := os.WriteFile(filepath.Join(dir, f.Path), []byte(f.Content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(files) {
		t.Fatalf("%d files, want %d (run go test -update to accept)", len(files), len(entries))
	}
	for _, f := range files {
		if w := readFile(t, filepath.Join(dir, f.Path)); f.Content != w {
			t.Fatalf("%s differs (run go test -update to accept)", f.Path)
		}
	}
}

// TestGraphQLGoInputs: under engine=graphql-go an input field whose
// GraphQL name graphql-go does not derive from its Go name carries a graphql
// tag with the name, and no other does; an optional list is a slice, nil
// when not given, where gqlgen's is a pointer to one. gqlgen's Go of the
// same model has no tags.
func TestGraphQLGoInputs(t *testing.T) {
	files, err := RenderConfig(modeltest.GraphQLGo(t), outPath, "gen", graphqlGoConfig)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range files {
		got[f.Path] = f.Content
	}
	post := got["post_gql.go"]
	for _, want := range []string{
		"\tAuthorID      *int64  `graphql:\"authorID\"`\n",
		"\tIDNEQ   *int64\n",
		"\tRelatedIDs []int64 `graphql:\"relatedIDs\"`\n",
		"\tTags        []string\n",
		"\tif v := in.Tags; v != nil {\n\t\tset = append(set, Posts.Tags.Set(v))\n",
	} {
		if !strings.Contains(post, want) {
			t.Errorf("no %q in post_gql.go:\n%s", want, post)
		}
	}
	gqlgen, err := RenderConfig(modeltest.GraphQLGo(t), outPath, "gen", sdl.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range gqlgen {
		if strings.Contains(f.Content, "`graphql:") {
			t.Errorf("gqlgen's %s has a graphql tag", f.Path)
		}
	}
}

// TestGraphQLName: graphqlName is graphql-go's derivation of an input
// field's GraphQL name from its Go name. For each Go name, a struct whose
// one field has it, untagged, binds by graphql-go to an input whose field is
// graphqlName's, and not to one whose field is another.
func TestGraphQLName(t *testing.T) {
	for _, c := range []struct {
		goName, want string
		binds        func(field string) error
	}{
		{"ID", "id", binds[struct{ ID *string }]},
		{"IDNEQ", "idNEQ", binds[struct{ IDNEQ *string }]},
		{"IDs", "ids", binds[struct{ IDs *string }]},
		{"AuthorID", "authorId", binds[struct{ AuthorID *string }]},
		{"AuthorIDNotIn", "authorIdNotIn", binds[struct{ AuthorIDNotIn *string }]},
		{"ImageURL", "imageUrl", binds[struct{ ImageURL *string }]},
		{"URLPath", "urlPath", binds[struct{ URLPath *string }]},
		{"ExternalUUID", "externalUuid", binds[struct{ ExternalUUID *string }]},
		{"GUID", "guid", binds[struct{ GUID *string }]},
		{"HasPostsWith", "hasPostsWith", binds[struct{ HasPostsWith *string }]},
		{"CreatedAtGTE", "createdAtGTE", binds[struct{ CreatedAtGTE *string }]},
		{"X", "x", binds[struct{ X *string }]},
	} {
		if got := graphqlName(c.goName); got != c.want {
			t.Errorf("graphqlName(%q) = %q, want %q", c.goName, got, c.want)
		}
		if err := c.binds(c.want); err != nil {
			t.Errorf("%s: graphql-go does not bind it to %s: %v", c.goName, c.want, err)
		}
		if c.binds(c.goName) == nil {
			t.Errorf("%s: graphql-go binds it to %s too", c.goName, c.goName)
		}
	}
}

// binds is graphql-go's error building a schema whose input T has the one
// field named field, of type String, bound to the Go type In.
func binds[In any](field string) error {
	src := "input T { " + field + ": String }\ntype Query { x: String }"
	return graphql.Validate(graphql.SDL(src), graphql.Input[In]("T"),
		graphql.Query(graphql.Resolve("x", func(context.Context, graphql.Root) (*string, error) { return nil, nil })))
}
