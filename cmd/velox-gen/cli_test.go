package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/syssam/velox/internal/tempmod"
	"github.com/syssam/velox/plugin"
)

// root is the repository's root, the library the test modules use.
const root = "../.."

// offlineEnv is the environment of go commands in tests: the module cache
// already holds the library's dependencies (any go build of the library
// fills it), and nothing is downloaded.
func offlineEnv() []string {
	return append(os.Environ(), "GOFLAGS=-mod=mod", "GOPROXY=off", "GOTOOLCHAIN=local")
}

// moduleSlots bounds the module tests that run at once: each runs go
// commands, which use several cores and some memory. Half the CPUs, and at
// least 3: 3 on CI's 4 cores, 7 on a 14-core machine.
var moduleSlots = make(chan struct{}, max(3, runtime.NumCPU()/2))

var slotted sync.Map // the names of the tests that hold a slot

// parallel makes t a parallel test that holds one of moduleSlots until it
// ends. Calling it twice for one test does nothing. A subtest of a test that
// holds a slot runs in its parent's slot, and not in parallel: a parent keeps
// its slot until its subtests end, so a subtest that waited for a slot of its
// own could wait forever once such parents held every slot.
func parallel(t *testing.T) {
	t.Helper()
	name := t.Name()
	for p := name; ; p = p[:strings.LastIndex(p, "/")] {
		if _, held := slotted.Load(p); held {
			return
		}
		if !strings.Contains(p, "/") {
			break
		}
	}
	slotted.Store(name, true)
	t.Parallel()
	moduleSlots <- struct{}{}
	t.Cleanup(func() {
		<-moduleSlots
		slotted.Delete(name)
	})
}

// A subtest of a test that holds a slot takes none of its own, so that
// parents cannot hold every slot while their subtests wait for one.
func TestParallelSubtestUsesItsParentsSlot(t *testing.T) {
	parallel(t)
	if _, held := slotted.Load(t.Name()); !held {
		t.Fatal("the test holds no slot")
	}
	t.Run("sub", func(t *testing.T) {
		parallel(t)
		t.Run("subsub", func(t *testing.T) {
			parallel(t)
			if _, held := slotted.Load(t.Name()); held {
				t.Error("a subtest two levels down took a slot of its own")
			}
		})
		if _, held := slotted.Load(t.Name()); held {
			t.Error("the subtest took a slot of its own")
		}
	})
}

// tempModule writes files into a new module, example.com/app, that uses this
// checkout's library, and returns its directory. The test runs in parallel
// with others (see parallel); a test that changes the working directory uses
// tempModuleSerial.
func tempModule(t *testing.T, files map[string]string) string {
	t.Helper()
	if !testing.Short() {
		parallel(t)
	}
	return tempModuleSerial(t, files)
}

// tempModuleSerial is tempModule for a test that must not run in parallel.
func tempModuleSerial(t *testing.T, files map[string]string) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tempModuleAt(t, dir, files)
	return dir
}

// tidied is what go mod tidy made of a set of files: tidying is the slow part
// of a test module, and the same files always tidy the same, so it is done
// once per set.
var tidied sync.Map // key -> *tidyResult

type tidyResult struct {
	once         sync.Once
	gomod, gosum []byte
	err          string
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
	files["go.mod"] = "module example.com/app\n\ngo 1.27.0\n\nrequire github.com/syssam/velox v0.0.0\n\nreplace github.com/syssam/velox => " + lib + "\n"
	files["go.sum"] = string(sum)
	var key strings.Builder
	for _, name := range slices.Sorted(maps.Keys(files)) {
		fmt.Fprintf(&key, "%q:%q;", name, files[name])
	}
	for name, src := range files {
		writeFile(t, filepath.Join(dir, name), src)
	}
	v, _ := tidied.LoadOrStore(key.String(), new(tidyResult))
	r := v.(*tidyResult)
	r.once.Do(func() {
		cmd := exec.Command("go", "mod", "tidy")
		cmd.Dir, cmd.Env = dir, offlineEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			r.err = fmt.Sprintf("go mod tidy: %v\n%s%s", err, out, tempmod.Hint(string(out)))
			return
		}
		r.gomod, _ = os.ReadFile(filepath.Join(dir, "go.mod"))
		r.gosum, _ = os.ReadFile(filepath.Join(dir, "go.sum"))
	})
	if r.err != "" {
		t.Fatal(r.err)
	}
	writeFile(t, filepath.Join(dir, "go.mod"), string(r.gomod))
	writeFile(t, filepath.Join(dir, "go.sum"), string(r.gosum))
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

// runCLI runs the command in dir.
func runCLI(t *testing.T, dir string, args ...string) (int, string, string) {
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
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
)

type User struct{ schema.Schema }

func (User) Fields() []schema.Field { return []schema.Field{field.String("name")} }
`})
	code, out, errs := runCLI(t, dir, "describe", "-json", "./schema")
	if code != 0 || !strings.Contains(out, `"name": "User"`) || !strings.Contains(out, `"table": "users"`) {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	code, out, errs = runCLI(t, dir, "describe")
	if code != 0 || !strings.HasPrefix(out, "User (users)\n") || !strings.Contains(out, "name") {
		t.Fatalf("default package: %d %q %q", code, out, errs)
	}
	code, _, errs = runCLI(t, dir, "describe", "./nothing")
	if code != 1 || !strings.HasPrefix(errs, "velox-gen: ") || strings.Contains(errs, "velox-gen: velox-gen: ") {
		t.Fatalf("%d %q", code, errs)
	}
}

// One module serves the generate forms: the optional word, flags after the
// package, "--", the stale exit code.
func TestGenerateSubcommand(t *testing.T) {
	dir := genModule(t, nil)
	files := []string{"user_velox.go", "tag_velox.go", "velox.go"}
	gens := func(out string) map[string]string {
		m := map[string]string{}
		for _, f := range files {
			// The package is named for -out, in its clause and its comment;
			// compare the rest.
			src := regexp.MustCompile(`(?m)^package \w+$`).ReplaceAllString(read(t, filepath.Join(dir, out, f)), "package gen")
			m[f] = regexp.MustCompile(`(?m)^// Package \w+ `).ReplaceAllString(src, "// Package gen ")
		}
		return m
	}
	for _, c := range []struct {
		out  string
		args []string
	}{
		{"a", []string{"-out", "a", "./schema"}},
		{"b", []string{"generate", "-out", "b", "./schema"}},
		{"c", []string{"./schema", "-out", "c"}},
		{"d", []string{"generate", "./schema", "-out", "d"}},
		{"e", []string{"-out", "e", "--", "./schema"}},
	} {
		if code, _, errs := runCLI(t, dir, c.args...); code != 0 {
			t.Fatalf("%v: %d %q", c.args, code, errs)
		}
	}
	for _, out := range []string{"b", "c", "d", "e"} {
		if !reflect.DeepEqual(gens("a"), gens(out)) {
			t.Errorf("%s differs from a", out)
		}
	}
	// A flag after the package is a flag of the same subcommand.
	if code, _, errs := runCLI(t, dir, "./schema", "-check", "-out", "a"); code != 0 {
		t.Fatalf("check after the package: %d %q", code, errs)
	}
	if code, out, errs := runCLI(t, dir, "verify", "./schema", "-out", "a"); code != 0 || !strings.Contains(out, "2 entities") {
		t.Fatalf("verify: %d %q %q", code, out, errs)
	}
	if code, out, errs := runCLI(t, dir, "describe", "./schema", "-json"); code != 0 || !strings.HasPrefix(out, "{") {
		t.Fatalf("describe: %d %q %q", code, out, errs)
	}
	// A flag the subcommand does not take stays misuse after the package.
	if code, _, _ := runCLI(t, dir, "verify", "./schema", "-check"); code != 2 {
		t.Errorf("verify ./schema -check: %d", code)
	}
	// Stale is 3, an error 1.
	writeFile(t, filepath.Join(dir, "a", "user_velox.go"), "// Code generated by velox-gen. DO NOT EDIT.\n\npackage a\n")
	code, _, errs := runCLI(t, dir, "generate", "-check", "-out", "a", "./schema")
	if code != 3 || !strings.Contains(errs, "user_velox.go is out of date") {
		t.Fatalf("%d %q", code, errs)
	}
	if code, _, _ := runCLI(t, dir, "-check", "./nothing"); code != 1 {
		t.Errorf("error: %d", code)
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
		{"./schema", "-json"},
		{"./a", "./b"},
		{"generate", "./a", "./b"},
		{"verify", "-out", "x", "./schema", "extra"},
		{"describe", "a", "b"},
		{"describe", "./a", "-json", "./b"},
		{"--", "./a", "./b"},
	} {
		code, out, errs := runCLI(t, dir, args...)
		if code != 2 || out != "" || !strings.Contains(errs, "Usage:") {
			t.Errorf("%v: %d %q %q", args, code, out, errs)
		}
	}
	// generate -h lists generate's flags only.
	code, _, errs := runCLI(t, dir, "generate", "-h")
	if code != 0 {
		t.Fatalf("-h: %d", code)
	}
	flags := flagsOf(t, errs)
	if !strings.Contains(flags, "-check") || !strings.Contains(flags, "-plugin") || strings.Contains(flags, "-json") {
		t.Errorf("generate -h:\n%s", errs)
	}
	code, _, errs = runCLI(t, dir, "describe", "-h")
	if code != 0 {
		t.Fatalf("-h: %d", code)
	}
	flags = flagsOf(t, errs)
	if !strings.Contains(flags, "-json") || strings.Contains(flags, "-check") || strings.Contains(flags, "-plugin") {
		t.Errorf("describe -h:\n%s", errs)
	}
	for _, w := range []string{"velox-gen version", "Exit codes", "stale"} {
		if !strings.Contains(errs, w) {
			t.Errorf("usage lacks %q:\n%s", w, errs)
		}
	}
}

// flagsOf is the flags section of a usage text.
func flagsOf(t *testing.T, usage string) string {
	t.Helper()
	i := strings.Index(usage, "Flags of")
	if i < 0 {
		t.Fatalf("no flags section in\n%s", usage)
	}
	return usage[i:]
}

// A bare word that names a directory is probably a package.
func TestBareWordHint(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "schema"), 0o755); err != nil {
		t.Fatal(err)
	}
	code, out, errs := runCLI(t, dir, "schema")
	if want := "velox-gen: unknown subcommand \"schema\"; for the package write ./schema\n"; code != 2 || out != "" || !strings.HasPrefix(errs, want) {
		t.Errorf("directory: %d %q %q", code, out, errs)
	}
	code, out, errs = runCLI(t, dir, "nothing")
	if want := "velox-gen: unknown subcommand \"nothing\"\n"; code != 2 || out != "" || !strings.HasPrefix(errs, want) || strings.Contains(errs, "for the package") {
		t.Errorf("no directory: %d %q %q", code, out, errs)
	}
}

func TestVersion(t *testing.T) {
	code, out, errs := runCLI(t, t.TempDir(), "version")
	if code != 0 || errs != "" || !regexp.MustCompile(`^velox-gen \S+ \(generated code 10, plugin protocol 2, model 3\)\n$`).MatchString(out) {
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
	code, out, errs := runCLI(t, dir, "describe", "-json", "./schema")
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
