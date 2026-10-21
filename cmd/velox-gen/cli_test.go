package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/syssam/velox2/plugin"
)

// root is the repository's root, the library the test modules use.
const root = "../.."

// offlineEnv is the environment of go commands in tests: the module cache
// already holds the library's dependencies (any go build of the library
// fills it), and nothing is downloaded.
func offlineEnv() []string {
	return append(os.Environ(), "GOFLAGS=-mod=mod", "GOPROXY=off", "GOTOOLCHAIN=local")
}

// tempModule writes files into a new module, example.com/app, that uses this
// checkout's library, and returns its directory.
func tempModule(t *testing.T, files map[string]string) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tempModuleAt(t, dir, files)
	return dir
}

// tempModuleAt is tempModule in the directory dir, which it creates.
func tempModuleAt(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	lib, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	sum, err := os.ReadFile(filepath.Join(lib, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	files["go.mod"] = "module example.com/app\n\ngo 1.27.0\n\nrequire github.com/syssam/velox2 v0.0.0\n\nreplace github.com/syssam/velox2 => " + lib + "\n"
	files["go.sum"] = string(sum)
	for name, src := range files {
		writeFile(t, filepath.Join(dir, name), src)
	}
	cmd := exec.Command("go", "mod", "tidy")
	cmd.Dir, cmd.Env = dir, offlineEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go mod tidy: %v\n%s", err, out)
	}
}

func writeFile(t *testing.T, path, src string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

// velox runs the command in dir.
func velox(t *testing.T, dir string, args ...string) (int, string, string) {
	t.Helper()
	var out, errs bytes.Buffer
	code := run(args, dir, offlineEnv(), &out, &errs)
	return code, out.String(), errs.String()
}

func TestDescribeCommand(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	dir := tempModule(t, map[string]string{"schema/user.go": `package schema

import (
	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/field"
)

type User struct{ schema.Schema }

func (User) Fields() []schema.Field { return []schema.Field{field.String("name")} }
`})
	code, out, errs := velox(t, dir, "describe", "-json", "./schema")
	if code != 0 || !strings.Contains(out, `"name": "User"`) || !strings.Contains(out, `"table": "users"`) {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	code, out, errs = velox(t, dir, "describe")
	if code != 0 || !strings.HasPrefix(out, "User (users)\n") || !strings.Contains(out, "name") {
		t.Fatalf("default package: %d %q %q", code, out, errs)
	}
	code, _, errs = velox(t, dir, "describe", "./nothing")
	if code != 1 || !strings.HasPrefix(errs, "velox-gen: ") || strings.Contains(errs, "velox-gen: velox-gen: ") {
		t.Fatalf("%d %q", code, errs)
	}
}

func TestGenerateSubcommand(t *testing.T) {
	a := genModule(t, nil)
	b := genModule(t, nil)
	if code, _, errs := velox(t, a, "-out", "x", "./schema"); code != 0 {
		t.Fatalf("%d %q", code, errs)
	}
	if code, _, errs := velox(t, b, "generate", "-out", "x", "./schema"); code != 0 {
		t.Fatalf("%d %q", code, errs)
	}
	for _, f := range []string{"user_velox.go", "tag_velox.go", "velox.go"} {
		if read(t, filepath.Join(a, "x", f)) != read(t, filepath.Join(b, "x", f)) {
			t.Errorf("%s differs", f)
		}
	}
	if code, _, errs := velox(t, b, "generate", "-check", "-out", "x", "./schema"); code != 0 {
		t.Fatalf("generate -check: %d %q", code, errs)
	}
	// No subcommand and no flags is generate too.
	if code, _, errs := velox(t, b); code != 0 {
		t.Fatalf("bare: %d %q", code, errs)
	}
}

func TestSubcommandFlags(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{
		{"verify", "-check"},
		{"describe", "-out", "x"},
		{"describe", "-plugin", "p"},
		{"version", "-json"},
		{"version", "extra"},
		{"generate", "-json"},
		{"-json"},
		{"frobnicate"},
		{"verify", "-bogus"},
	} {
		code, out, errs := velox(t, dir, args...)
		if code != 2 || out != "" || errs == "" {
			t.Errorf("%v: %d %q %q", args, code, out, errs)
		}
	}
	// generate -h lists generate's flags only.
	code, _, errs := velox(t, dir, "generate", "-h")
	if code != 0 && code != 2 {
		t.Fatalf("-h: %d", code)
	}
	flags := errs[strings.Index(errs, "Flags of"):]
	if !strings.Contains(flags, "-check") || !strings.Contains(flags, "-plugin") || strings.Contains(flags, "-json") {
		t.Errorf("generate -h:\n%s", errs)
	}
	_, _, errs = velox(t, dir, "describe", "-h")
	flags = errs[strings.Index(errs, "Flags of"):]
	if !strings.Contains(flags, "-json") || strings.Contains(flags, "-check") || strings.Contains(flags, "-plugin") {
		t.Errorf("describe -h:\n%s", errs)
	}
	for _, w := range []string{"velox-gen version", "Exit codes", "stale"} {
		if !strings.Contains(errs, w) {
			t.Errorf("usage lacks %q:\n%s", w, errs)
		}
	}
}

func TestCheckExits3(t *testing.T) {
	dir := genModule(t, nil)
	if code, _, errs := velox(t, dir, "./schema"); code != 0 {
		t.Fatalf("%d %q", code, errs)
	}
	if code, _, errs := velox(t, dir, "-check", "./schema"); code != 0 {
		t.Fatalf("fresh: %d %q", code, errs)
	}
	writeFile(t, filepath.Join(dir, "gen", "user_velox.go"), "// Code generated by velox-gen. DO NOT EDIT.\n\npackage gen\n")
	code, _, errs := velox(t, dir, "generate", "-check", "./schema")
	if code != 3 || !strings.Contains(errs, "user_velox.go is out of date") {
		t.Fatalf("%d %q", code, errs)
	}
	// An error stays 1.
	if code, _, _ := velox(t, dir, "-check", "./nothing"); code != 1 {
		t.Errorf("error: %d", code)
	}
}

func TestVersion(t *testing.T) {
	code, out, errs := velox(t, t.TempDir(), "version")
	if code != 0 || errs != "" || !regexp.MustCompile(`^velox-gen \S+ \(generated code 4, plugin protocol 2, model 2\)\n$`).MatchString(out) {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	if want := "velox-gen " + strings.TrimPrefix(generator(), "velox-gen ") + " ("; !strings.HasPrefix(out, want) {
		t.Errorf("%q does not start with %q", out, want)
	}
}

func TestDescribeJSONIsPluginModel(t *testing.T) {
	dir := pluginModule(t)
	dump := filepath.Join(t.TempDir(), "req.json")
	env := fakeEnv("FAKEPLUGIN_V=1", "FAKEPLUGIN_DUMP="+dump)
	if code, _, errs := veloxEnv(t, dir, env, "-plugin", "fakeplugin", "./schema"); code != 0 {
		t.Fatalf("%d %q", code, errs)
	}
	var req struct {
		Model plugin.Model `json:"model"`
	}
	if err := json.Unmarshal([]byte(read(t, dump)), &req); err != nil {
		t.Fatal(err)
	}
	code, out, errs := velox(t, dir, "describe", "-json", "./schema")
	if code != 0 {
		t.Fatalf("%d %q", code, errs)
	}
	var got plugin.Model
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, req.Model) {
		t.Errorf("describe -json differs from the request's model:\n%s", out)
	}
	if !strings.HasPrefix(out, "{\n  \"") {
		t.Errorf("not indented: %q", out[:20])
	}
}
