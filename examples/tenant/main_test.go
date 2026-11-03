package main

import (
	"context"
	"strings"
	"testing"

	"github.com/syssam/velox/examples/internal/golden"
)

// TestOutput runs the example and compares what it prints with
// testdata/output.golden; go test -update rewrites the file.
func TestOutput(t *testing.T) {
	var out strings.Builder
	if err := run(t.Context(), &out); err != nil {
		t.Fatal(err)
	}
	golden.Check(t, out.String())
}

// TestRunStopsOnACancelledContext: run returns the first error, here the
// migrations', and prints nothing of the demo.
func TestRunStopsOnACancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var out strings.Builder
	if err := run(ctx, &out); err == nil {
		t.Fatal("run succeeded on a cancelled context")
	}
	if out.Len() != 0 {
		t.Errorf("run printed %q before failing", out.String())
	}
}
