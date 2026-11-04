package closed

import (
	"strings"
	"testing"

	"github.com/syssam/velox"

	"example.com/tmp/gen"
)

// A program that links gen without package runtime, which binds the
// schema, fails at an entity's first use and names the line to add. The
// failure is not kept: once the runtime is set, the entity works.
func TestGenWithoutRuntime(t *testing.T) {
	err := velox.CheckEntity[gen.Group]()
	want := `gen: the schema's hooks, policies and defaults are not linked: add import _ "example.com/tmp/gen/runtime" to the program's main package, or to the test`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("got %v, want %s", err, want)
	}
	if !strings.Contains(err.Error(), "gen.Group") {
		t.Errorf("the error does not name gen.Group: %v", err)
	}
	gen.SetVeloxRuntime(gen.VeloxRuntime{})
	if err := velox.CheckEntity[gen.Group](); err != nil {
		t.Fatalf("after SetVeloxRuntime: %v", err)
	}
}
