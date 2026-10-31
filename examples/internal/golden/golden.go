// Package golden compares an example's output with its testdata/output.golden,
// so a change to velox's API or behavior that an example does not follow
// fails CI.
package golden

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata/output.golden with the example's output")

// Check fails t at the first line where got differs from
// testdata/output.golden. With -update it rewrites the file first.
func Check(t *testing.T, got string) {
	t.Helper()
	path := filepath.Join("testdata", "output.golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	g, w := strings.Split(got, "\n"), strings.Split(string(want), "\n")
	for i := range max(len(g), len(w)) {
		var gl, wl string
		if i < len(g) {
			gl = g[i]
		}
		if i < len(w) {
			wl = w[i]
		}
		if gl != wl {
			t.Fatalf("line %d differs from %s (go test -update accepts the new output):\ngot:  %q\nwant: %q", i+1, path, gl, wl)
		}
	}
}
