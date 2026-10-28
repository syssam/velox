package graphql_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestAnnotationsImportOnlySchema pins that the annotations package
// compiles no GraphQL library. It imports the standard library and
// velox2/schema, and no other package; its dependencies are those and what
// schema itself imports.
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
		if !std && p != "github.com/syssam/velox2/contrib/graphql" && !isSchema(p) {
			t.Errorf("the annotations package depends on %s", p)
		}
	}
}

// isSchema reports whether p is velox2/schema or one of its packages, and not
// a sibling such as velox2/schemaX.
func isSchema(p string) bool {
	return p == "github.com/syssam/velox2/schema" || strings.HasPrefix(p, "github.com/syssam/velox2/schema/")
}
