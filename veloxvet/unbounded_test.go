package veloxvet_test

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/syssam/velox/veloxvet"
)

// TestUnbounded runs the analyzer on testdata/a, which uses velox's real
// packages and its test model. The package is copied into a temporary
// module that reaches this repository's root through a replace, with the
// root's go.sum, so that nothing here pins the root's dependencies.
// analysistest loads it offline: the root's modules must be in the module
// cache (go mod download in the root).
func TestUnbounded(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	mod := "module github.com/syssam/velox/veloxvet/testdata\n\ngo 1.27.0\n\n" +
		"require github.com/syssam/velox v0.0.0-00010101000000-000000000000\n\n" +
		"replace github.com/syssam/velox => " + root + "\n"
	write(t, filepath.Join(dir, "go.mod"), []byte(mod))
	write(t, filepath.Join(dir, "go.sum"), read(t, filepath.Join(root, "go.sum")))
	files, err := filepath.Glob(filepath.Join("testdata", "a", "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("testdata/a: %v %v", files, err)
	}
	for _, f := range files {
		write(t, filepath.Join(dir, "a", filepath.Base(f)), read(t, f))
	}
	analysistest.Run(t, dir, veloxvet.Unbounded, "./a")
}

func read(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func write(t *testing.T, name string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
