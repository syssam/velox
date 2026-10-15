package main

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata/output.golden with the walkthrough's output")

// TestWalkthrough runs the example and compares what it prints with
// testdata/output.golden, so a change to velox's API or behavior that the
// example does not follow fails CI.
func TestWalkthrough(t *testing.T) {
	var out bytes.Buffer
	if err := run(t.Context(), &out); err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "output.golden")
	if *update {
		if err := os.WriteFile(golden, out.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	got, wantLines := strings.Split(out.String(), "\n"), strings.Split(string(want), "\n")
	for i := range max(len(got), len(wantLines)) {
		var g, w string
		if i < len(got) {
			g = got[i]
		}
		if i < len(wantLines) {
			w = wantLines[i]
		}
		if g != w {
			t.Fatalf("line %d differs from %s (go test -update accepts the new output):\ngot:  %q\nwant: %q", i+1, golden, g, w)
		}
	}
}
