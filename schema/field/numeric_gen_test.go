package field_test

import (
	"os/exec"
	"testing"
)

// numeric.go is generated. Methods once added to it by hand -- Nullable on
// every builder, Float64 -- were missing from the template, so the next
// go generate would have deleted public API without a word.
func TestNumericMatchesTemplate(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the generator")
	}
	out, err := exec.Command("go", "run", "internal/gen.go", "-check").CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}
