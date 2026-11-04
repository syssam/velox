package entity_test

import (
	"strings"
	"testing"

	"github.com/syssam/velox"

	"example.com/tmp/gen/entity"
)

// A program that links package entity without package gen, which binds the
// schema, fails at an entity's first use and names the import. The failure
// is not kept: once the runtime is set, the entity works.
func TestEntityWithoutGen(t *testing.T) {
	err := velox.CheckEntity[entity.Group]()
	if want := `entity: the schema's hooks, policies and defaults are not linked: import "example.com/tmp/gen", which re-exports "example.com/tmp/gen/entity" and links the schema`; err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("got %v, want %s", err, want)
	}
	entity.SetVeloxRuntime(entity.VeloxRuntime{})
	if err := velox.CheckEntity[entity.Group](); err != nil {
		t.Fatalf("after SetVeloxRuntime: %v", err)
	}
}
