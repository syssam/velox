package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
