package graphql

import (
	"os/exec"
	"strings"
	"testing"
)

// Schema packages import this one for its annotations, and velox's generated
// runtime imports the schema package, so everything this package imports is
// linked into every server built from velox. The generator lives in
// graphqlgen for that reason: here it put velox's compiler, jennifer,
// golang.org/x/tools/go/packages and gqlgen's codegen into production
// binaries, 88 packages of build tooling.
func TestAnnotationsImportNoGenerator(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}", ".").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	allowed := map[string]bool{
		"github.com/syssam/velox/contrib/graphql": true,
		"github.com/syssam/velox/schema":          true,
	}
	for _, dep := range strings.Fields(string(out)) {
		if !allowed[dep] {
			t.Errorf("the annotations package imports %s; schema packages, and so every server, would link it", dep)
		}
	}
}
