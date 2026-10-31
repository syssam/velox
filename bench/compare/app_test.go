package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/mod/modfile"
)

func TestAppGoMod(t *testing.T) {
	hd, err := harnessDir()
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(filepath.Join(hd, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := appGoMod(src, "/r")
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, want := range []string{
		"module example.com/tmp",
		"entgo.io/ent v0.14.6",
		"github.com/syssam/velox v0.0.0",
		"golang.org/x/tools v0.52.0",
		"=> /r",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("go.mod lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "../..") {
		t.Errorf("go.mod keeps the relative replace:\n%s", got)
	}
	// Not a substring check for "tool": golang.org/x/tools contains it.
	f, err := modfile.Parse("go.mod", out, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Tool) != 0 {
		t.Errorf("go.mod keeps its tool directives: %v", f.Tool)
	}
}

// TestVeloxeApp: veloxe's app pins veloxe from the module's go.mod and go.sum,
// and has no replace, which would build it on this repository.
func TestVeloxeApp(t *testing.T) {
	dir := t.TempDir()
	if err := writeApp(veloxeORM, dir, nil); err != nil {
		t.Fatal(err)
	}
	mod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	f, err := modfile.Parse("go.mod", mod, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Replace) != 0 {
		t.Errorf("go.mod has a replace:\n%s", mod)
	}
	if !strings.Contains(string(mod), "github.com/syssam/velox v0.3.1-") {
		t.Errorf("go.mod does not pin veloxe:\n%s", mod)
	}
	hd, _ := harnessDir()
	want, _ := os.ReadFile(filepath.Join(hd, "veloxe", "go.sum"))
	if got, err := os.ReadFile(filepath.Join(dir, "go.sum")); err != nil || string(got) != string(want) {
		t.Errorf("go.sum is not veloxe's module's: %v", err)
	}
}

// TestVeloxeModuleMatchesHarness: what the two go.mods share is at one version,
// or the apps would compare different dependencies.
func TestVeloxeModuleMatchesHarness(t *testing.T) {
	hd, err := harnessDir()
	if err != nil {
		t.Fatal(err)
	}
	harness, err := requirements(filepath.Join(hd, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	veloxe, err := requirements(filepath.Join(hd, "veloxe", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	for path, v := range harness {
		if w, ok := veloxe[path]; ok && path != "github.com/syssam/velox" && w != v {
			t.Errorf("%s: %s in the harness, %s in veloxe/go.mod", path, v, w)
		}
	}
}

func TestWriteApp(t *testing.T) {
	dir := t.TempDir()
	if err := writeApp(veloxORM, dir, map[string]string{"a/b.go": "package a\n"}); err != nil {
		t.Fatal(err)
	}
	hd, _ := harnessDir()
	wantSum, _ := os.ReadFile(filepath.Join(hd, "go.sum"))
	if gotSum, err := os.ReadFile(filepath.Join(dir, "go.sum")); err != nil || string(gotSum) != string(wantSum) {
		t.Errorf("go.sum is not the harness's: %v", err)
	}
	mod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil || !strings.Contains(string(mod), "module example.com/tmp") {
		t.Errorf("go.mod: %s, %v", mod, err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "a", "b.go")); err != nil || string(b) != "package a\n" {
		t.Errorf("a/b.go: %q, %v", b, err)
	}
}

func TestGoEnv(t *testing.T) {
	t.Setenv("GOFLAGS", "-p=2")
	env := goEnv("X=1")
	last := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		last[k] = v
	}
	for k, want := range map[string]string{"GOFLAGS": "-p=2 -mod=mod", "GOPROXY": "off", "GOWORK": "off", "X": "1"} {
		if last[k] != want {
			t.Errorf("%s = %q, want %q", k, last[k], want)
		}
	}
}

func TestCountGenerated(t *testing.T) {
	dir := t.TempDir()
	for name, src := range map[string]string{
		"a.go":               "package a\n\nvar x = 1\n",
		"sub/b.go":           "package b\n",
		"schema/skip.go":     "package schema\n\n\n\n",
		"sub/schema/keep.go": "package schema\n",
		"readme.txt":         "not go\n",
	} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lines, bytes, err := countGenerated(dir, "")
	if err != nil || lines != 3+1+4+1 || bytes != 21+10+18+15 {
		t.Errorf("no skip: %d lines, %d bytes, %v", lines, bytes, err)
	}
	// Only dir/schema is skipped, not sub/schema, and dir may be relative.
	t.Chdir(dir)
	for _, d := range []string{dir, ".", "./"} {
		lines, bytes, err = countGenerated(d, "schema")
		if err != nil || lines != 5 || bytes != 46 {
			t.Errorf("dir %q, skip schema: %d lines, %d bytes, %v", d, lines, bytes, err)
		}
	}
	for name, want := range map[string]string{"velox": "", "veloxe": "", "ent": "schema"} {
		if o, _ := ormByName(name); o.genSkip() != want {
			t.Errorf("%s: genSkip = %q, want %q", name, o.genSkip(), want)
		}
	}
}

// toolPkgs maps an ORM to the package of its generator, which TestGenerateCompiles builds.
var toolPkgs = map[string]string{
	"velox":  "github.com/syssam/velox/cmd/velox-gen",
	"veloxe": "github.com/syssam/velox/cmd/velox",
	"ent":    "entgo.io/ent/cmd/ent",
}

// TestGenerateCompiles runs each ORM's code generator on 20 entities in a
// temporary app, and vets the result and a service that uses it. It is heavy.
func TestGenerateCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("three code generations and vets")
	}
	const n = 20
	hd, err := harnessDir()
	if err != nil {
		t.Fatal(err)
	}
	tools := t.TempDir()
	lines := map[string]int{}
	for _, o := range orms {
		t.Run(o.Name, func(t *testing.T) {
			build := exec.Command("go", "build", "-o", tools+string(filepath.Separator), toolPkgs[o.Name])
			build.Dir, build.Env = filepath.Join(hd, o.ModDir), goEnv()
			if out, err := build.CombinedOutput(); err != nil {
				t.Fatalf("building the generator: %v\n%s", err, out)
			}
			app := t.TempDir()
			if err := writeApp(o, app, o.Schema(n)); err != nil {
				t.Fatal(err)
			}
			cpu, wall, err := codegen(app, o, tools)
			if err != nil {
				t.Fatal(err)
			}
			if cpu <= 0 || wall <= 0 {
				t.Errorf("codegen measured cpu %v, wall %v", cpu, wall)
			}
			svc := filepath.Join(app, "s0")
			if err := os.MkdirAll(svc, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(svc, "main.go"), []byte(o.Service(serviceEntities(0, n))), 0o644); err != nil {
				t.Fatal(err)
			}
			vet := exec.Command("go", "vet", "./...")
			vet.Dir, vet.Env = app, goEnv()
			if out, err := vet.CombinedOutput(); err != nil {
				t.Fatalf("go vet: %v\n%s", err, out)
			}
			for i := range n {
				// velox generates a file per entity, the others a package.
				name := "e" + strconv.Itoa(i)
				if _, err := os.Stat(filepath.Join(app, o.GenDir, name+"_velox.go")); err != nil {
					if fi, err := os.Stat(filepath.Join(app, o.GenDir, name)); err != nil || !fi.IsDir() {
						t.Errorf("nothing generated for E%d under %s", i, o.GenDir)
					}
				}
			}
			l, b, err := countGenerated(filepath.Join(app, o.GenDir), o.genSkip())
			if err != nil || l <= 0 || b <= 0 {
				t.Fatalf("countGenerated = %d lines, %d bytes, %v", l, b, err)
			}
			t.Logf("%s: %d lines, %d bytes generated (codegen cpu %.1fs, wall %.1fs)", o.Name, l, b, cpu, wall)
			lines[o.Name] = l
		})
	}
	if lines["velox"] > 0 && lines["ent"] > 0 && lines["velox"] >= lines["ent"] {
		t.Errorf("velox generated %d lines, ent %d: velox should generate less", lines["velox"], lines["ent"])
	}
}
