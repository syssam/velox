package graphqlgen_test

import (
	"bytes"
	"context"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox/contrib/graphql/graphqlgen"
	"github.com/syssam/velox/generate"
	"github.com/syssam/velox/internal/tempmod"
	"github.com/syssam/velox/plugin"
)

// TestPluginAnswers: Plugin is named as the tool, passes its options on, and
// answers a request as the tool does, an option it does not take included.
func TestPluginAnswers(t *testing.T) {
	m := &plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{{
		Name: "User", Label: "user", Handles: "Users", PrimaryKey: []int{0},
		Fields: []*plugin.Field{{Name: "id", GoName: "ID", Type: "int64", ElemType: "int64"}, {Name: "name", GoName: "Name", Type: "string", ElemType: "string"}},
	}}}
	p := graphqlgen.Plugin(map[string]string{"names": "schema"})
	if p.Tool != "velox-graphql" || p.Options["names"] != "schema" || p.Run == nil {
		t.Fatalf("%+v", p)
	}
	req := &plugin.Request{Protocol: plugin.ProtocolVersion, OutPackage: "gen", OutPath: "example.com/app/gen", Model: m}
	resp, err := graphqlgen.Plugin(nil).Run(context.Background(), req)
	if err != nil || resp.Error != "" || len(resp.Files) != 2 || resp.Files[0].Path != "velox.graphql" || resp.Files[1].Path != "user_gql.go" {
		t.Fatalf("%v %+v", err, resp)
	}
	// engine=graphql-go reaches the generator: velox.graphql binds User.
	req.Options = map[string]string{"engine": "graphql-go"}
	resp, err = graphqlgen.Plugin(req.Options).Run(context.Background(), req)
	if err != nil || resp.Error != "" || len(resp.Files) != 2 || !strings.Contains(resp.Files[0].Content, `type User @goModel(model: "example.com/app/gen.User")`) {
		t.Fatalf("engine=graphql-go: %v %+v", err, resp)
	}
	req.Options = map[string]string{"x": "y"}
	resp, err = graphqlgen.Plugin(req.Options).Run(context.Background(), req)
	if err != nil || len(resp.Files) != 0 || !strings.Contains(resp.Error, "velox-graphql takes the options") {
		t.Fatalf("%v %+v", err, resp)
	}
	req.Options, req.Model = nil, &plugin.Model{Version: 1}
	if resp, err = graphqlgen.Plugin(nil).Run(context.Background(), req); err != nil || !strings.Contains(resp.Error, "model is version 1") {
		t.Fatalf("%v %+v", err, resp)
	}
}

// TestPluginMatchesTheTool: generate.Run with Plugin, on examples/graphql's
// schema in a module of the examples' path that holds only the schema
// package, writes the files that velox-gen -plugin velox-graphql wrote into
// examples/graphql/veloxgen, byte for byte.
func TestPluginMatchesTheTool(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	repo, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	ex := filepath.Join(repo, "examples")
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var sum []byte
	for _, p := range []string{"go.sum", "contrib/graphql/go.sum"} {
		b, err := os.ReadFile(filepath.Join(repo, p))
		if err != nil {
			t.Fatal(err)
		}
		sum = append(sum, b...)
	}
	// The examples' module path, so that the generated code imports the
	// schema package by the path the committed files name.
	files := map[string][]byte{"go.mod": []byte("module github.com/syssam/velox/examples\n\ngo 1.27.0\n\n" +
		"require (\n\tgithub.com/syssam/velox v0.0.0\n\tgithub.com/syssam/velox/contrib/graphql v0.0.0\n)\n\n" +
		"replace (\n\tgithub.com/syssam/velox => " + repo + "\n" +
		"\tgithub.com/syssam/velox/contrib/graphql => " + filepath.Join(repo, "contrib", "graphql") + "\n)\n"), "go.sum": sum}
	schemas, err := filepath.Glob(filepath.Join(ex, "graphql", "veloxschema", "*.go"))
	if err != nil || len(schemas) == 0 {
		t.Fatalf("%v %v", schemas, err)
	}
	for _, p := range schemas {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		files["graphql/veloxschema/"+filepath.Base(p)] = b
	}
	for name, b := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	env := append(os.Environ(), "GOFLAGS=-mod=mod", "GOPROXY=off", "GOTOOLCHAIN=local", "GOWORK=off")
	// -e: tidy also looks for test dependencies an offline module cache may
	// lack, as velox-graphql's e2e tests explain; nothing here builds them.
	tidy := exec.Command("go", "mod", "tidy", "-e")
	tidy.Dir, tidy.Env = dir, env
	if out, err := tidy.CombinedOutput(); err != nil {
		t.Fatalf("go mod tidy: %v\n%s%s", err, out, tempmod.Hint(string(out)))
	}
	var stderr bytes.Buffer
	res, err := generate.Run(context.Background(), dir, generate.Config{
		Schema:  "./graphql/veloxschema",
		Out:     "./graphql/veloxgen",
		Plugins: []generate.Plugin{graphqlgen.Plugin(nil)},
		Env:     env,
		Stderr:  &stderr,
	})
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr.String())
	}
	got, want := tree(t, filepath.Join(dir, "graphql", "veloxgen")), tree(t, filepath.Join(ex, "graphql", "veloxgen"))
	if !slices.Equal(slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want))) {
		t.Fatalf("Run wrote %v; the tool wrote %v", slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want)))
	}
	for name, b := range want {
		if !bytes.Equal(got[name], b) {
			t.Errorf("%s differs from the tool's", name)
		}
	}
	if len(res.Written) != len(want) {
		t.Errorf("Result.Written = %v", res.Written)
	}
}

// tree is the files below dir by slash path, with their content.
func tree(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		out[filepath.ToSlash(rel)] = b
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
