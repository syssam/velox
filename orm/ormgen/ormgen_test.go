package ormgen_test

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/syssam/velox/orm/ormgen"
)

// The generator knows no GraphQL: the ORM does not depend on what is built
// on top of it. A program that generates a tree without the GraphQL layer
// links none of it, and nothing of the schema language's parser.
func TestGeneratorDoesNotDependOnGraphQL(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "github.com/syssam/velox/orm/ormgen").Output()
	if err != nil {
		t.Fatal(err)
	}
	deps := strings.Fields(string(out))
	if len(deps) < 20 {
		t.Fatalf("go list -deps gave %d packages", len(deps))
	}
	for _, dep := range deps {
		if strings.Contains(dep, "graphql") || strings.Contains(dep, "gqlparser") {
			t.Errorf("orm/ormgen depends on %s", dep)
		}
	}
}

// What the package gives is the generator's: the names of the library the
// root package of a tree repeats are the ones velox-migrate goes by.
func TestRootNames(t *testing.T) {
	names := ormgen.RootNames()
	for _, want := range []string{"DB", "Tx", "Executor", "IsNotFound", "Must"} {
		found := false
		for _, n := range names {
			found = found || n == want
		}
		if !found {
			t.Errorf("RootNames has no %s", want)
		}
	}
}
