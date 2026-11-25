package gqlplugin

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syssam/velox/plugin"
)

// writeFiles writes files below dir, each at its slash path.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, src := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestCheckGQLGenConfig: the gqlgen configuration nearest the generated
// package, up to its module's root, must bind something from gqlgenx; a
// name in a comment or of another package does not count. With no
// configuration, or a package outside the module, nothing is checked.
func TestCheckGQLGenConfig(t *testing.T) {
	const mod = "module example.com/app\n\ngo 1.27\n"
	const binds = "models:\n  ID:\n    model: github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx.Int64ID\n"
	const autobind = "autobind:\n  - \"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx\"\n"
	const none = "# IDs bind to github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx.Int64ID elsewhere\nmodels:\n  ID:\n    model: github.com/99designs/gqlgen/graphql.ID\n  X:\n    model: github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenxother.X\n"
	for _, c := range []struct {
		name  string
		files map[string]string
		out   string
		fail  bool
	}{
		{"binds a model", map[string]string{"gqlgen.yml": binds}, "example.com/app/veloxgen", false},
		{"autobinds the package", map[string]string{"gqlgen.yaml": autobind}, "example.com/app/veloxgen", false},
		{"binds none", map[string]string{"gqlgen.yml": none}, "example.com/app/veloxgen", true},
		{"nearest wins", map[string]string{"gqlgen.yml": binds, "graphql/.gqlgen.yml": none}, "example.com/app/graphql/veloxgen", true},
		{"nested config", map[string]string{"gqlgen.yml": none, "graphql/gqlgen.yml": binds}, "example.com/app/graphql/veloxgen", false},
		{"no config", map[string]string{}, "example.com/app/veloxgen", false},
		{"another module", map[string]string{"gqlgen.yml": none}, "example.com/other/veloxgen", false},
		{"below the module only", map[string]string{"../gqlgen.yml": none}, "example.com/app/veloxgen", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			top := t.TempDir()
			root := filepath.Join(top, "app")
			files := map[string]string{"go.mod": mod}
			maps.Copy(files, c.files)
			writeFiles(t, root, files)
			err := checkGQLGenConfig(filepath.Join(root, "veloxschema"), c.out)
			if c.fail != (err != nil) {
				t.Fatalf("err = %v, want failure %v", err, c.fail)
			}
			if err != nil && (!strings.Contains(err.Error(), "gqlgenx.Int64ID") || !strings.Contains(err.Error(), "gqlgenx=blank")) {
				t.Errorf("the error names no fix: %v", err)
			}
		})
	}
}

// TestGenerateChecksGQLGenConfig: Generate refuses a configuration that
// binds nothing from gqlgenx under gqlgen, and passes it with gqlgenx=blank
// and under engine=graphql-go.
func TestGenerateChecksGQLGenConfig(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"go.mod":     "module example.com/app\n\ngo 1.27\n",
		"gqlgen.yml": "models:\n  ID:\n    model: github.com/99designs/gqlgen/graphql.ID\n",
	})
	t.Chdir(dir)
	model := &plugin.Model{Version: plugin.ModelVersion, SchemaPath: "example.com/app/veloxschema"}
	for _, c := range []struct {
		opts map[string]string
		fail bool
	}{
		{nil, true},
		{map[string]string{"gqlgenx": "blank"}, false},
		{map[string]string{"engine": "graphql-go"}, false},
	} {
		_, _, err := Generate(&plugin.Request{Protocol: plugin.ProtocolVersion, OutPackage: "veloxgen", OutPath: "example.com/app/veloxgen", Options: c.opts, Model: model})
		if refused := err != nil && strings.Contains(err.Error(), "binds nothing from"); refused != c.fail {
			t.Errorf("options %v: %v, want refused %v", c.opts, err, c.fail)
		}
	}
}

// TestCheckGQLGenConfigEdges: a directory with no go.mod above it, or one
// whose go.mod names no module, is checked against nothing; a
// configuration that cannot be read fails with its error; and a package at
// the module's own path reads the configuration in its root.
func TestCheckGQLGenConfigEdges(t *testing.T) {
	bare := t.TempDir()
	if err := checkGQLGenConfig(bare, "example.com/app/veloxgen"); err != nil {
		t.Errorf("no go.mod: %v, want nil", err)
	}
	writeFiles(t, bare, map[string]string{"go.mod": "go 1.27\n", "gqlgen.yml": "models: {}\n"})
	if err := checkGQLGenConfig(bare, "example.com/app/veloxgen"); err != nil {
		t.Errorf("a go.mod with no module: %v, want nil", err)
	}

	root := t.TempDir()
	writeFiles(t, root, map[string]string{"go.mod": "module example.com/app\n"})
	if err := os.Mkdir(filepath.Join(root, "gqlgen.yml"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := checkGQLGenConfig(root, "example.com/app/veloxgen"); err == nil || !strings.Contains(err.Error(), "gqlgen.yml") {
		t.Errorf("an unreadable configuration: %v, want its error", err)
	}

	own := t.TempDir()
	writeFiles(t, own, map[string]string{"go.mod": "module example.com/app\n", "gqlgen.yml": "models: {}\n"})
	if err := checkGQLGenConfig(own, "example.com/app"); err == nil || !strings.Contains(err.Error(), "binds nothing") {
		t.Errorf("a package at the module's path: %v, want a refusal", err)
	}
}

// TestModulePath: the module directive's path, quoted or not, after lines
// that are not it; "" when there is none.
func TestModulePath(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{"// c\nmodule example.com/a\n", "example.com/a"},
		{"go 1.27\nmodule \"example.com/q\"\n", "example.com/q"},
		{"module\ngo 1.27\n", ""},
		{"", ""},
	} {
		if got := modulePath([]byte(c.src)); got != c.want {
			t.Errorf("modulePath(%q) = %q, want %q", c.src, got, c.want)
		}
	}
}
