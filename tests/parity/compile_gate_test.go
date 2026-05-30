package parity_test

import (
	"os/exec"
	"testing"
)

// TestCompileGate regenerates both the velox and Ent clients from their
// schemas and then compiles the generated packages. It is the foundation's
// guard rail: if a schema change breaks either generator or produces code
// that does not build, this test fails. Skipped under -short because it
// shells out to the toolchain (codegen + compile) and is not a unit test.
func TestCompileGate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping compile gate in -short mode (runs codegen + build)")
	}

	run := func(name string, args ...string) {
		t.Helper()
		cmd := exec.Command(name, args...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v failed: %v\n%s", name, args, err, out)
		}
	}

	// Regenerate both clients from their schemas.
	run("go", "run", "generate.go")
	// Compile the generated packages for both ORMs.
	run("go", "build", "./velox/...", "./ent/...")
}
