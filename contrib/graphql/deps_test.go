package graphql_test

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// TestAnnotationsImportOnlySchema pins that the annotations package
// compiles no GraphQL library. It imports the standard library and
// velox/schema, and no other package; its dependencies are those and what
// schema itself imports.
func TestAnnotationsImportOnlySchema(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go list")
	}
	out, err := exec.Command("go", "list", "-f", `{{join .Imports "\n"}}`, ".").CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	for p := range strings.FieldsSeq(string(out)) {
		std := !strings.Contains(strings.Split(p, "/")[0], ".")
		if !std && p != "github.com/syssam/velox/schema" {
			t.Errorf("the annotations package imports %s", p)
		}
	}
	out, err = exec.Command("go", "list", "-deps", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	for p := range strings.FieldsSeq(string(out)) {
		std := !strings.Contains(strings.Split(p, "/")[0], ".")
		if !std && p != "github.com/syssam/velox/contrib/graphql" && !isSchema(p) {
			t.Errorf("the annotations package depends on %s", p)
		}
	}
}

// isSchema reports whether p is velox/schema or one of its packages, and not
// a sibling such as velox/schemaX.
func isSchema(p string) bool {
	return p == "github.com/syssam/velox/schema" || strings.HasPrefix(p, "github.com/syssam/velox/schema/")
}

// The import paths of the GraphQL engines, and of what only a server needs.
const (
	gqlgen    = "github.com/99designs/gqlgen"
	graphqlGo = "github.com/syssam/graphql-go"
)

// depends returns the packages pkg depends on, through go list -deps.
func depends(t *testing.T, pkg string) []string {
	t.Helper()
	out, err := exec.Command("go", "list", "-deps", pkg).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	return strings.Fields(string(out))
}

// under reports whether p is root or one of its packages, and not a sibling
// such as rootX.
func under(p, root string) bool {
	return p == root || strings.HasPrefix(p, root+"/")
}

// refuse fails t for each package of pkgs that depends on one under a root
// of roots, naming the rule.
func refuse(t *testing.T, rule string, pkgs []string, roots ...string) {
	t.Helper()
	for _, pkg := range pkgs {
		for _, p := range depends(t, pkg) {
			for _, root := range roots {
				if under(p, root) {
					t.Errorf("%s depends on %s: %s", pkg, p, rule)
				}
			}
		}
	}
}

// TestCoreImportsNoEngine: the runtime that generated code imports (the core
// veloxgql, gqlgraph), the packages under it, and gqlgenx's document walks
// compile neither gqlgen nor
// graphql-go, nor net/http, nor gqlparser's validator, so one generated
// package serves either engine and a server of one compiles none of the
// other.
func TestCoreImportsNoEngine(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go list")
	}
	refuse(t, "the core imports no engine", []string{
		"./veloxgql", "./veloxgql/gqlgraph", "./veloxgql/internal/gqlcore", "./veloxgql/internal/cost", "./veloxgql/gqlgenx/internal/request",
	}, gqlgen, graphqlGo, "net/http", "github.com/vektah/gqlparser/v2/validator", "github.com/sosodev/duration", "github.com/agnivade/levenshtein")
}

// TestCoreImportsNoGqlparser: the core veloxgql and gqlgraph, which
// generated code imports, compile no gqlparser at all: what reads a
// document (the cost, the request checks) is gqlgenx's.
func TestCoreImportsNoGqlparser(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go list")
	}
	refuse(t, "the core imports no gqlparser", []string{"./veloxgql", "./veloxgql/gqlgraph", "./veloxgql/internal/gqlcore"}, "github.com/vektah/gqlparser")
}

// TestGqlgenxImportsNoGraphqlGo: gqlgen's adapter compiles no graphql-go.
func TestGqlgenxImportsNoGraphqlGo(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go list")
	}
	refuse(t, "gqlgenx imports no graphql-go", []string{"./veloxgql/gqlgenx"}, graphqlGo)
}

// TestGraphqlgoImportsNoGqlgen: graphql-go's adapter compiles no gqlgen and
// no net/http: it imports graphql-go and the core.
func TestGraphqlgoImportsNoGqlgen(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go list")
	}
	refuse(t, "graphqlgo imports no gqlgen", []string{"./veloxgql/graphqlgo"}, gqlgen, "net/http", "github.com/sosodev/duration")
	deps := depends(t, "./veloxgql/graphqlgo")
	for _, want := range []string{graphqlGo, "github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"} {
		if !slices.Contains(deps, want) {
			t.Errorf("graphqlgo does not depend on %s", want)
		}
	}
}

// TestGeneratorImportsNoEngine: the plugin and the packages that write the
// SDL and the Go compile neither engine: they name gqlgen's types as strings
// only.
func TestGeneratorImportsNoEngine(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go list")
	}
	refuse(t, "the generator imports no engine", []string{
		"./cmd/velox-graphql", "./internal/gqlplugin", "./internal/sdl", "./internal/gogen",
	}, gqlgen, graphqlGo)
}
