package generate_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/syssam/velox/generate"
	"github.com/syssam/velox/internal/genrun"
	"github.com/syssam/velox/internal/tempmod"
	"github.com/syssam/velox/plugin"
)

const userSchema = `package veloxschema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
)

type User struct{ schema.Schema }

func (User) Fields() []schema.Field {
	return []schema.Field{
		field.String("name"),
		field.Time("created_at"),
		field.String("email"),
		field.Int64("workspace_id"),
	}
}
`

// offline is the environment of the go commands Run starts in a test: the
// module cache holds what the modules need, and nothing is downloaded.
func offline() []string {
	return append(os.Environ(), "GOFLAGS="+tempmod.GoFlags("-mod=mod"), "GOPROXY=off", "GOWORK=off")
}

// module writes a module whose ./veloxschema holds files, and returns its
// directory.
func module(t *testing.T, files map[string]string) string {
	t.Helper()
	if testing.Short() {
		t.Skip("builds a module")
	}
	repo, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := tempmod.Write(dir, repo, files); err != nil {
		t.Fatal(err)
	}
	return dir
}

// tree is the files below dir by slash path, with their content; nil when
// dir does not exist.
func tree(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
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

// The zero Config generates ./veloxgen, package veloxgen, from
// ./veloxschema; Package defaults to Out's base name; Stdout gets each path
// written, and Result lists them.
func TestRunDefaults(t *testing.T) {
	t.Parallel()
	dir := module(t, map[string]string{"veloxschema/user.go": userSchema})
	var stdout, stderr bytes.Buffer
	res, err := generate.Run(context.Background(), dir, generate.Config{Env: offline(), Stdout: &stdout, Stderr: &stderr})
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr.String())
	}
	want := []string{filepath.Join("veloxgen", "user.go"), filepath.Join("veloxgen", "velox.go")}
	if !slices.Equal(res.Written, want) || res.Removed != nil || res.Stale != nil {
		t.Errorf("%+v", res)
	}
	if stdout.String() != strings.Join(want, "\n")+"\n" {
		t.Errorf("stdout: %q", stdout.String())
	}
	src, err := os.ReadFile(filepath.Join(dir, "veloxgen", "user.go"))
	if err != nil || !bytes.Contains(src, []byte("\npackage veloxgen\n")) {
		t.Fatalf("%v\n%s", err, src)
	}

	// A second run writes nothing, and a check finds nothing stale.
	if res, err := generate.Run(context.Background(), dir, generate.Config{Env: offline()}); err != nil || res.Written != nil {
		t.Errorf("second run: %+v %v", res, err)
	}
	if res, err := generate.Run(context.Background(), dir, generate.Config{Env: offline(), Check: true}); err != nil || res.Stale != nil {
		t.Errorf("check: %+v %v", res, err)
	}

	res, err = generate.Run(context.Background(), dir, generate.Config{Env: offline(), Out: "./db/models"})
	if err != nil || len(res.Written) != 2 {
		t.Fatalf("%+v %v", res, err)
	}
	if src, err := os.ReadFile(filepath.Join(dir, "db", "models", "user.go")); err != nil || !bytes.Contains(src, []byte("\npackage models\n")) {
		t.Errorf("Package is not Out's base name: %v\n%s", err, src)
	}
	if _, err := generate.Run(context.Background(), dir, generate.Config{Env: offline(), Out: "./db/models", Package: "not-a-name"}); err == nil || !strings.Contains(err.Error(), `"not-a-name" is not a package name`) {
		t.Errorf("a bad Package: %v", err)
	}
}

// A Check writes nothing, lists the stale files and returns ErrStale.
func TestRunCheck(t *testing.T) {
	t.Parallel()
	dir := module(t, map[string]string{"veloxschema/user.go": userSchema})
	var stderr bytes.Buffer
	res, err := generate.Run(context.Background(), dir, generate.Config{Env: offline(), Check: true, Stderr: &stderr})
	want := []string{filepath.Join("veloxgen", "user.go"), filepath.Join("veloxgen", "velox.go")}
	if !errors.Is(err, generate.ErrStale) || !slices.Equal(res.Stale, want) || res.Written != nil {
		t.Fatalf("%+v %v", res, err)
	}
	if got := tree(t, filepath.Join(dir, "veloxgen")); got != nil {
		t.Errorf("a check wrote %v", slices.Sorted(maps.Keys(got)))
	}
	if !strings.Contains(stderr.String(), "velox-gen: "+want[0]+" is out of date\n") || strings.Contains(stderr.String(), "velox-gen: run") {
		t.Errorf("stderr: %q", stderr.String())
	}
}

// An in-process plugin is given the request a tool would read; its files
// are written with velox-gen's header, its warnings printed and returned,
// and a check sees its files as a tool's.
func TestRunInProcessPlugin(t *testing.T) {
	t.Parallel()
	dir := module(t, map[string]string{"veloxschema/user.go": userSchema})
	var got *plugin.Request
	p := generate.Plugin{Tool: "notes", Options: map[string]string{"k": "v"}, Run: func(_ context.Context, req *plugin.Request) (*plugin.Response, error) {
		got = req
		return &plugin.Response{
			Files:    []plugin.File{{Path: "notes/entities.graphql", Content: "type User\n"}},
			Warnings: []string{"a note\nwith a newline"},
		}, nil
	}}
	var stderr bytes.Buffer
	res, err := generate.Run(context.Background(), dir, generate.Config{Env: offline(), Plugins: []generate.Plugin{p}, Stderr: &stderr})
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr.String())
	}
	if got == nil || got.Protocol != plugin.ProtocolVersion || got.OutPackage != "veloxgen" || got.OutPath != "example.com/tmp/veloxgen" ||
		got.Options["k"] != "v" || got.Model == nil || len(got.Model.Entities) != 1 || got.Model.Entities[0].Name != "User" {
		t.Fatalf("the request: %+v", got)
	}
	b, err := os.ReadFile(filepath.Join(dir, "veloxgen", "notes", "entities.graphql"))
	if err != nil || string(b) != "# Code generated by velox-gen. DO NOT EDIT.\n\ntype User\n" {
		t.Errorf("%v %q", err, b)
	}
	if want := []string{`plugin notes: a note\nwith a newline`}; !slices.Equal(res.Warnings, want) {
		t.Errorf("Warnings = %q, want %q", res.Warnings, want)
	}
	if !strings.Contains(stderr.String(), "velox-gen: plugin notes: warning: a note\\nwith a newline\n") {
		t.Errorf("stderr: %q", stderr.String())
	}
	// Without the plugin, a check finds its file stale, and a run removes it.
	if res, err := generate.Run(context.Background(), dir, generate.Config{Env: offline(), Check: true, Stderr: io.Discard}); !errors.Is(err, generate.ErrStale) ||
		!slices.Equal(res.Stale, []string{filepath.Join("veloxgen", "notes", "entities.graphql")}) {
		t.Errorf("%+v %v", res, err)
	}
	if res, err := generate.Run(context.Background(), dir, generate.Config{Env: offline()}); err != nil ||
		!slices.Equal(res.Removed, []string{filepath.Join("veloxgen", "notes", "entities.graphql")}) || res.Written != nil {
		t.Errorf("%+v %v", res, err)
	}
}

// A plugin's failure fails the run, naming the plugin, and writes nothing:
// an error it returns, an error it reports, no response, a panic, a path the
// protocol refuses; a plugin with no Tool is refused first.
func TestRunPluginErrors(t *testing.T) {
	t.Parallel()
	dir := module(t, map[string]string{"veloxschema/user.go": userSchema})
	answer := func(resp *plugin.Response, err error) func(context.Context, *plugin.Request) (*plugin.Response, error) {
		return func(context.Context, *plugin.Request) (*plugin.Response, error) { return resp, err }
	}
	for name, c := range map[string]struct {
		p    generate.Plugin
		want string
	}{
		"returned": {generate.Plugin{Tool: "p", Run: answer(nil, errors.New("boom"))}, "plugin p: boom"},
		"reported": {generate.Plugin{Tool: "p", Run: answer(&plugin.Response{Error: "bad input"}, nil)}, "plugin p: bad input"},
		"nil":      {generate.Plugin{Tool: "p", Run: answer(nil, nil)}, "plugin p: it returned no response"},
		"panic": {generate.Plugin{Tool: "p", Run: func(context.Context, *plugin.Request) (*plugin.Response, error) {
			panic("out of cheese")
		}}, "plugin p: panic: out of cheese"},
		"path":         {generate.Plugin{Tool: "p", Run: answer(&plugin.Response{Files: []plugin.File{{Path: "../x.go", Content: "package x\n"}}}, nil)}, `plugin p: invalid path "../x.go"`},
		"collision":    {generate.Plugin{Tool: "p", Run: answer(&plugin.Response{Files: []plugin.File{{Path: "user.go", Content: "package veloxgen\n"}}}, nil)}, "plugin p: veloxgen/user.go collides with veloxgen/user.go, which velox-gen writes"},
		"no tool":      {generate.Plugin{Run: answer(&plugin.Response{}, nil)}, "plugin 1 has no Tool"},
		"no such tool": {generate.Plugin{Tool: "velox-nothing"}, "the module has no tool velox-nothing"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := generate.Run(context.Background(), dir, generate.Config{Env: offline(), Plugins: []generate.Plugin{c.p}})
			if err == nil || !strings.Contains(err.Error(), filepath.FromSlash(c.want)) {
				t.Errorf("%v, want %q", err, c.want)
			}
			if got := tree(t, filepath.Join(dir, "veloxgen")); got != nil {
				t.Errorf("a failed run wrote %v", slices.Sorted(maps.Keys(got)))
			}
		})
	}
}

// A done context stops Run before it loads anything.
func TestRunContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := generate.Run(ctx, t.TempDir(), generate.Config{}); !errors.Is(err, context.Canceled) {
		t.Errorf("%v", err)
	}
}

// Run on internal/testmodel's schema, as CONTRIBUTING.md's command
// generates it, finds the committed files current: it generates them byte
// for byte.
func TestRunMatchesTestModel(t *testing.T) {
	if testing.Short() {
		t.Skip("loads a schema package")
	}
	t.Parallel()
	var stderr bytes.Buffer
	res, err := generate.Run(context.Background(), "..", generate.Config{
		Schema: "./internal/testmodel/schema", Out: "internal/testmodel", Package: "testmodel",
		Check: true, Env: offline(), Stderr: &stderr,
	})
	if err != nil || res.Stale != nil {
		t.Fatalf("%+v %v\n%s", res, err, stderr.String())
	}
}

// Run on examples/start's schema, in a module of the examples' path that
// holds only the schema package, writes the files committed in
// examples/start/veloxgen, byte for byte.
func TestRunMatchesExample(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	t.Parallel()
	repo, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	ex := filepath.Join(repo, "examples")
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sum, err := os.ReadFile(filepath.Join(repo, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	// The examples' module path, so that the generated code imports the
	// schema package by the path the committed files name.
	files := map[string][]byte{
		"go.mod": []byte("module github.com/syssam/velox/examples\n\ngo 1.27.0\n\nrequire github.com/syssam/velox v0.0.0\n\nreplace github.com/syssam/velox => " + repo + "\n"),
		"go.sum": sum,
	}
	for name, b := range tree(t, filepath.Join(ex, "start", "veloxschema")) {
		files["start/veloxschema/"+name] = b
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
	if _, err := tempmod.Go(dir, "mod", "tidy"); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if _, err := generate.Run(context.Background(), dir, generate.Config{Schema: "./start/veloxschema", Out: "./start/veloxgen", Env: offline(), Stderr: &stderr}); err != nil {
		t.Fatalf("%v\n%s", err, stderr.String())
	}
	got, want := tree(t, filepath.Join(dir, "start", "veloxgen")), tree(t, filepath.Join(ex, "start", "veloxgen"))
	if !slices.Equal(slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want))) {
		t.Fatalf("Run wrote %v; velox-gen wrote %v", slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want)))
	}
	for name, b := range want {
		if !bytes.Equal(got[name], b) {
			t.Errorf("%s differs from the committed file", name)
		}
	}
}

// A signal while Run writes waits for the write, and Run then returns
// ErrInterrupted: alone when the write is done, joined with the write's
// error when it failed and was undone. Serial: it replaces genrun.WriteTemp.
func TestRunInterrupted(t *testing.T) {
	dir := module(t, map[string]string{"veloxschema/user.go": userSchema})
	signalled := func(fail error) func(string, string, []byte) (string, error) {
		return func(d, pattern string, data []byte) (string, error) {
			c := make(chan os.Signal, 1) // a second holder, to see the signal arrive
			signal.Notify(c, syscall.SIGTERM)
			defer signal.Stop(c)
			if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
				return "", err
			}
			select {
			case <-c:
			case <-time.After(10 * time.Second):
			}
			if fail != nil {
				return "", fail
			}
			return genrun.WriteTempFile(d, pattern, data)
		}
	}
	defer func() { genrun.WriteTemp = genrun.WriteTempFile }()

	injected := errors.New("injected failure")
	genrun.WriteTemp = signalled(injected)
	res, err := generate.Run(context.Background(), dir, generate.Config{Env: offline()})
	if !errors.Is(err, generate.ErrInterrupted) || !errors.Is(err, injected) || res.Written != nil {
		t.Fatalf("%+v %v", res, err)
	}
	if got := tree(t, filepath.Join(dir, "veloxgen")); got != nil {
		t.Errorf("an undone write left %v", slices.Sorted(maps.Keys(got)))
	}

	genrun.WriteTemp = signalled(nil)
	res, err = generate.Run(context.Background(), dir, generate.Config{Env: offline()})
	if err != generate.ErrInterrupted || len(res.Written) != 2 {
		t.Fatalf("%+v %v", res, err)
	}
	if got := tree(t, filepath.Join(dir, "veloxgen")); len(got) != 2 {
		t.Errorf("the write is not done: %v", slices.Sorted(maps.Keys(got)))
	}
}
