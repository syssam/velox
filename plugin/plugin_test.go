package plugin_test

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/syssam/velox/plugin"
)

// TestPluginPackageImportsNothing pins that package plugin depends on the
// standard library alone.
func TestPluginPackageImportsNothing(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}", "github.com/syssam/velox/plugin").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	var extra []string
	for _, p := range strings.Fields(string(out)) {
		if p != "github.com/syssam/velox/plugin" {
			extra = append(extra, p)
		}
	}
	if len(extra) > 0 {
		t.Fatalf("package plugin imports %v besides the standard library", extra)
	}
}

// TestEdgeKindConstants pins the edge kinds' JSON values, which are model 2's.
func TestEdgeKindConstants(t *testing.T) {
	for k, want := range map[plugin.EdgeKind]string{plugin.O2O: "O2O", plugin.O2M: "O2M", plugin.M2O: "M2O", plugin.M2M: "M2M"} {
		if string(k) != want {
			t.Errorf("edge kind %q, want %q", k, want)
		}
		b, err := json.Marshal(plugin.Edge{Kind: k})
		if err != nil || !strings.Contains(string(b), `"kind":"`+want+`"`) {
			t.Errorf("edge of kind %s marshals to %s (%v)", want, b, err)
		}
	}
	if plugin.ModelVersion != 2 {
		t.Errorf("ModelVersion %d, want 2", plugin.ModelVersion)
	}
}
