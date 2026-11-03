package entity_test

import (
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"

	"example.com/tmp/gen/entity"
)

// A program that links package entity without package gen, which binds the
// schema, fails at an entity's first use and names the import. A schema
// value without the method velox-gen bound fails at the first use too, and
// names the method and its type.
func TestEntityWithoutGen(t *testing.T) {
	err := velox.CheckEntity[entity.Group]()
	if want := `entity: the schema's hooks, policies and defaults are not linked: import "example.com/tmp/gen", which re-exports "example.com/tmp/gen/entity" and links the schema`; err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("got %v, want %s", err, want)
	}
	entity.SetVeloxRuntime(entity.VeloxRuntime{Post: schema.Schema{}})
	err = velox.CheckEntity[entity.Post]()
	if want := "entity: schema.Schema, the schema of Post, has no method Policy() velox.Policy[entity.Post]: run velox-gen"; err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("got %v, want %s", err, want)
	}
}
