package codegen

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syssam/velox2/internal/tempmod"
)

// e2eModule writes a temporary module holding testdata/e2e/schema, generates
// it into gen/ with cfg, and copies in the user's files: each file of the
// listed testdata/e2e directories (user/, say) and, as gen/main_test.go, the
// test file. It returns the module's directory.
func e2eModule(t *testing.T, cfg Config, userDirs []string, testFile string) string {
	t.Helper()
	root := filepath.Join("testdata", "e2e")
	read := func(path string) []byte {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	files := map[string]string{}
	matches, err := filepath.Glob(filepath.Join(root, "schema", "*.go"))
	if err != nil || len(matches) == 0 {
		t.Fatalf("no schema files: %v", err)
	}
	for _, p := range matches {
		// The first line is the //go:build ignore that keeps the fixture out of
		// the packages of velox2.
		_, src, _ := bytes.Cut(read(p), []byte("\n"))
		files["schema/"+filepath.Base(p)] = string(src)
	}
	dir := module(t, files)
	m, err := Load(dir, "./schema", offline())
	if err != nil {
		t.Fatal(err)
	}
	g, err := Build(m)
	if err != nil {
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
		if err := os.WriteFile(filepath.Join(genDir, name), data, 0o644); err != nil {
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
			write(filepath.Base(p), read(p))
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
// testdata/e2e/main_test.go runs inside the generated package.
func TestGeneratedRunsOnSQLite(t *testing.T) {
	dir := e2eModule(t, Config{Package: "gen", Bound: map[string]Bound{
		"Post": {Policy: true},
		"User": {Hooks: true},
	}}, []string{"user"}, "main_test.go")
	if out, err := tempmod.Go(dir, "vet", "./..."); err != nil {
		t.Fatalf("go vet:\n%v", out)
	}
	if out, err := tempmod.Go(dir, "test", "-count=1", "./..."); err != nil {
		t.Fatalf("go test:\n%v", out)
	}
}

// A Policy method with another signature than the one velox binds does not
// compile once the metadata binds it (Review Focus 3).
func TestWrongPolicySignatureDoesNotCompile(t *testing.T) {
	dir := e2eModule(t, Config{Package: "gen", Bound: map[string]Bound{"Post": {Policy: true}}}, []string{"wrongsig"}, "")
	out, err := tempmod.Go(dir, "vet", "./...")
	if err == nil {
		t.Fatal("go vet accepted a Policy method that returns a pointer")
	}
	if !strings.Contains(out, "BindPolicy") || !strings.Contains(out, "Policy[Post]") || !strings.Contains(out, "does not match") {
		t.Errorf("the error does not name BindPolicy and the mismatched *velox.Policy[Post]:\n%s", out)
	}
	if !strings.Contains(out, "post_velox.go") {
		t.Errorf("the error is not in the generated file:\n%s", out)
	}
}

// A method the metadata does not bind is refused at the entity's first use,
// rather than silently never called.
func TestUnboundMethodIsRefused(t *testing.T) {
	dir := e2eModule(t, Config{Package: "gen"}, []string{"user"}, "unbound/main_test.go")
	if out, err := tempmod.Go(dir, "vet", "./..."); err != nil {
		t.Fatalf("go vet:\n%v", out)
	}
	if out, err := tempmod.Go(dir, "test", "-count=1", "./..."); err != nil {
		t.Fatalf("go test:\n%v", out)
	}
}
