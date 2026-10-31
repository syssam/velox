package main

import (
	"strings"
	"testing"

	"github.com/syssam/velox2/examples/internal/golden"
)

// TestOutput runs the tour and compares what it prints with
// testdata/output.golden; go test -update rewrites the file.
func TestOutput(t *testing.T) {
	var out strings.Builder
	if err := run(t.Context(), &out); err != nil {
		t.Fatal(err)
	}
	golden.Check(t, out.String())
}
