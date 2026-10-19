package plugin_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestPluginPackageImportsNothing pins that package plugin depends on the
// standard library alone.
func TestPluginPackageImportsNothing(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}", "github.com/syssam/velox2/plugin").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	var extra []string
	for _, p := range strings.Fields(string(out)) {
		if p != "github.com/syssam/velox2/plugin" {
			extra = append(extra, p)
		}
	}
	if len(extra) > 0 {
		t.Fatalf("package plugin imports %v besides the standard library", extra)
	}
}
