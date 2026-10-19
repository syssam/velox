package graphql_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestAnnotationsImportOnlySchema pins spec §2: the annotations package
// compiles no GraphQL library. Its dependencies are the standard library,
// velox2/schema and what schema itself imports.
func TestAnnotationsImportOnlySchema(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go list")
	}
	out, err := exec.Command("go", "list", "-f", `{{join .Imports "\n"}}`, ".").CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	for _, p := range strings.Fields(string(out)) {
		std := !strings.Contains(strings.Split(p, "/")[0], ".")
		if !std && p != "github.com/syssam/velox2/schema" {
			t.Errorf("the annotations package imports %s", p)
		}
	}
	out, err = exec.Command("go", "list", "-deps", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	for _, p := range strings.Fields(string(out)) {
		std := !strings.Contains(strings.Split(p, "/")[0], ".")
		if !std && p != "github.com/syssam/velox2/contrib/graphql" && !strings.HasPrefix(p, "github.com/syssam/velox2/schema") {
			t.Errorf("the annotations package depends on %s", p)
		}
	}
}
