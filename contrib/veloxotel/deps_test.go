package veloxotel_test

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// TestNoSDKDependency pins that the adapter needs the OpenTelemetry API only.
// The SDK may appear in tests, never in the package. The package imports the
// API, velox and the standard library, and no semconv package: its names are
// constants in attrs.go, which TestNamesMatchSemconv, a test file, checks
// against semconv v1.43.0. The API's own trace package imports one, so that is
// checked on the package's imports, not its deps.
func TestNoSDKDependency(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go list")
	}
	out, err := exec.Command("go", "list", "-deps", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	for _, p := range strings.Fields(string(out)) {
		if strings.HasPrefix(p, "go.opentelemetry.io/otel/sdk") {
			t.Errorf("veloxotel depends on %s", p)
		}
	}
	out, err = exec.Command("go", "list", "-f", `{{join .Imports "\n"}}`, ".").CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	allowed := []string{
		"github.com/syssam/velox",
		"go.opentelemetry.io/otel",
		"go.opentelemetry.io/otel/attribute",
		"go.opentelemetry.io/otel/codes",
		"go.opentelemetry.io/otel/metric",
		"go.opentelemetry.io/otel/trace",
	}
	for _, p := range strings.Fields(string(out)) {
		std := !strings.Contains(strings.Split(p, "/")[0], ".")
		if !std && !slices.Contains(allowed, p) {
			t.Errorf("veloxotel imports %s", p)
		}
	}
}
