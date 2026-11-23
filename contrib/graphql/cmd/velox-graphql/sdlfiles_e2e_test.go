package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestGqlcPerEntity is sdlfiles=entity and entities= end to end with gqlc
// (ADR 0125). Tag's Query field is postTags, so gqlc names the argument
// struct of Query.postTags and of Post.tags alike, PostTagsArgs: from the
// one velox.graphql, which gqlc makes one Go package, the package declares
// it twice and does not build. From
// velox.graphql and a file per entity, Query is in package velox and
// Post.tags in package post, and the application's module builds. With
// entities=Post the SDL is Post's alone, with the where inputs its own
// nests, and the module builds too.
//
// It builds three tools and a module, offline, from the module cache, as
// the other end-to-end tests do, and is left out under -short.
func TestGqlcPerEntity(t *testing.T) {
	t.Parallel() // its own module, in its own directory
	if testing.Short() {
		t.Skip("builds a module and gqlc")
	}
	schema := strings.Replace(gqlcSchema, `graphql.QueryField("labels")`, `graphql.QueryField("postTags")`, 1)
	dir, run := app(t, map[string]string{"schema/schema.go": schema, "graph/app.graphql": gqlcApp, "gqlc.yaml": gqlcConfig})
	withGqlc(t, dir, run)
	gen := func(opts string) {
		t.Helper()
		if err := os.RemoveAll(filepath.Join(dir, "gen")); err != nil {
			t.Fatal(err)
		}
		if out, err := run("tool", "velox-gen", "-out", "gen", "-plugin", "velox-graphql:engine=graphql-go"+opts, "./schema"); err != nil {
			t.Fatalf("velox-gen %s: %v\n%s", opts, err, out)
		}
	}
	graphqlFiles := func() []string {
		t.Helper()
		m, err := filepath.Glob(filepath.Join(dir, "gen", "*.graphql"))
		if err != nil {
			t.Fatal(err)
		}
		for i := range m {
			m[i] = filepath.Base(m[i])
		}
		slices.Sort(m)
		return m
	}

	gen("")
	if out, err := run("tool", "gqlc", "-config", "gqlc.yaml"); err != nil {
		t.Fatalf("gqlc over one velox.graphql: %v\n%s", err, out)
	}
	if out, err := run("vet", "./graph/velox"); err == nil || !strings.Contains(out, "PostTagsArgs redeclared") {
		t.Fatalf("go vet over one velox.graphql: %v, want PostTagsArgs declared twice:\n%s", err, out)
	}

	gen(",sdlfiles=entity")
	if got, want := graphqlFiles(), []string{"velox.graphql", "velox_like.graphql", "velox_post.graphql", "velox_tag.graphql", "velox_user.graphql"}; !slices.Equal(got, want) {
		t.Fatalf("gen has %v, want %v", got, want)
	}
	if out, err := run("tool", "gqlc", "-config", "gqlc.yaml"); err != nil {
		t.Fatalf("gqlc: %v\n%s", err, out)
	}
	if out, err := run("vet", "./..."); err != nil {
		t.Fatalf("go vet: %v\n%s", err, out)
	}

	// gqlc's packages of the run before: entities=Post has no group post.
	groups, err := os.ReadDir(filepath.Join(dir, "graph"))
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range groups {
		if g.IsDir() {
			if err := os.RemoveAll(filepath.Join(dir, "graph", g.Name())); err != nil {
				t.Fatal(err)
			}
		}
	}
	gen(",entities=Post")
	if got := graphqlFiles(); !slices.Equal(got, []string{"velox.graphql"}) {
		t.Fatalf("gen has %v, want velox.graphql alone", got)
	}
	sdlb, err := os.ReadFile(filepath.Join(dir, "gen", "velox.graphql"))
	if err != nil {
		t.Fatal(err)
	}
	for _, absent := range []string{"type User ", "type Label ", "postTags(", "  tags(", "  author: "} {
		if strings.Contains(string(sdlb), absent) {
			t.Errorf("entities=Post: the SDL has %q", absent)
		}
	}
	for _, present := range []string{"type Post ", "posts(", "input LabelWhereInput", "hasTagsWith: [LabelWhereInput!]"} {
		if !strings.Contains(string(sdlb), present) {
			t.Errorf("entities=Post: the SDL has no %q", present)
		}
	}
	if out, err := run("tool", "gqlc", "-config", "gqlc.yaml"); err != nil {
		t.Fatalf("gqlc, entities=Post: %v\n%s", err, out)
	}
	if out, err := run("vet", "./..."); err != nil {
		t.Fatalf("go vet, entities=Post: %v\n%s", err, out)
	}
}
