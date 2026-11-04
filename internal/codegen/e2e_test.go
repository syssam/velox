package codegen

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syssam/velox/internal/tempmod"
)

// e2eModule writes a temporary module holding testdata/e2e/schema and the
// files of the listed schemaDirs (behaviour/, say), generates it into gen/
// with cfg, and copies in the user's files: each file of the listed
// testdata/e2e userDirs (user/, say), into gen when it is package gen's,
// else into a directory of the userDir's name below gen, and, as
// gen/main_test.go, the test file.
// It returns the module's directory. A schema file's first line is the
// //go:build ignore that keeps the fixture out of the packages of velox.
func e2eModule(t *testing.T, cfg Config, schemaDirs, userDirs []string, testFile string) string {
	t.Helper()
	root := filepath.Join("testdata", "e2e")
	read := func(path string) []byte {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	schemaFiles := func(d string) map[string]string {
		matches, err := filepath.Glob(filepath.Join(root, d, "*.go"))
		if err != nil || len(matches) == 0 {
			t.Fatalf("no schema files in %s: %v", d, err)
		}
		files := map[string]string{}
		for _, p := range matches {
			_, src, _ := bytes.Cut(read(p), []byte("\n"))
			files["schema/"+filepath.Base(p)] = string(src)
		}
		return files
	}
	dir := module(t, schemaFiles("schema"))
	// The schemaDirs' files import gen, which does not exist yet: they join
	// after module's go mod tidy, and Load reads the schema without them.
	for _, d := range schemaDirs {
		for name, src := range schemaFiles(d) {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	m, err := Load(dir, "./schema", offline())
	if err != nil {
		t.Fatal(err)
	}
	g, err := Build(m)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Funcs, err = FuncPackages(dir, g, cfg, offline()); err != nil {
		t.Fatal(err)
	}
	out, err := Generate(g, cfg)
	if err != nil {
		t.Fatal(err)
	}
	genDir := filepath.Join(dir, "gen")
	if err := os.MkdirAll(genDir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name string, data []byte) {
		p := filepath.Join(genDir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range out {
		write(f.Name, f.Data)
	}
	for _, d := range userDirs {
		matches, err := filepath.Glob(filepath.Join(root, d, "*.go"))
		if err != nil || len(matches) == 0 {
			t.Fatalf("no files in %s: %v", d, err)
		}
		for _, p := range matches {
			if src := read(p); bytes.HasPrefix(src, []byte("package gen\n")) {
				write(filepath.Base(p), src)
			} else {
				write(filepath.Join(d, filepath.Base(p)), src)
			}
		}
	}
	if testFile != "" {
		write("main_test.go", read(filepath.Join(root, testFile)))
	}
	// The generated code and its test import packages the schema does not.
	if _, err := tempmod.Go(dir, "mod", "tidy"); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The generated code compiles, and works against the runtime on SQLite:
// testdata/e2e/main_test.go runs beside the generated package, and links
// package runtime. closed/closed_test.go links gen alone, which fails
// closed.
func TestGeneratedRunsOnSQLite(t *testing.T) {
	dir := e2eModule(t, Config{Package: "gen", SchemaImportsGen: true, GenPath: "example.com/tmp/gen", Bound: map[string]Bound{
		"Post": {Policy: true},
		"User": {Hooks: true},
		"Note": {Policy: true, Hooks: true},
	}}, []string{"behaviour"}, []string{"user", "closed"}, "main_test.go")
	if out, err := tempmod.Go(dir, "vet", "./..."); err != nil {
		t.Fatalf("go vet:\n%v", out)
	}
	if out, err := tempmod.Go(dir, "test", "-count=1", "./..."); err != nil {
		t.Fatalf("go test:\n%v", out)
	}
}

// A schema's Policy method with another signature than the one velox binds,
// which SchemaMethods would refuse, does not compile: package runtime
// checks it in runtime.go.
func TestWrongPolicySignatureDoesNotCompile(t *testing.T) {
	dir := e2eModule(t, Config{Package: "gen", SchemaImportsGen: true, GenPath: "example.com/tmp/gen", Bound: map[string]Bound{"Post": {Policy: true}}}, []string{"wrongsig"}, nil, "")
	out, err := tempmod.Go(dir, "vet", "./...")
	if err == nil {
		t.Fatal("go vet accepted a Policy method that returns a pointer")
	}
	if !strings.Contains(out, "sch.Post{}.Policy") || !strings.Contains(out, "*velox.Policy[gen.Post]") {
		t.Errorf("the error does not name Post.Policy and the mismatched *velox.Policy[gen.Post]:\n%s", out)
	}
	if !strings.Contains(out, "gen/runtime/runtime.go") {
		t.Errorf("the error is not in the generated file:\n%s", out)
	}
}

// A method the metadata does not bind, written in the generated package where
// velox-gen refuses it, is refused at the entity's first use, rather than
// silently never called.
func TestUnboundMethodIsRefused(t *testing.T) {
	dir := e2eModule(t, Config{Package: "gen"}, nil, []string{"user", "unboundgen"}, "unbound/main_test.go")
	if out, err := tempmod.Go(dir, "vet", "./..."); err != nil {
		t.Fatalf("go vet:\n%v", out)
	}
	if out, err := tempmod.Go(dir, "test", "-count=1", "./..."); err != nil {
		t.Fatalf("go test:\n%v", out)
	}
}
