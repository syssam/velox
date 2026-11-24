package filterent_test

import (
	"testing"

	"github.com/syssam/velox/internal/engine"
	"github.com/syssam/velox/internal/filterent"
)

// TestEntity: an Entity, made by New or StandIn, names its entity and has
// exactly its columns; the zero Entity names nothing and has no column.
func TestEntity(t *testing.T) {
	e := filterent.StandIn("Invoice", "id", "deleted_at")
	if e.Name() != "Invoice" || !e.HasColumn("deleted_at") || e.HasColumn("org_id") {
		t.Errorf("stand-in: %q, deleted_at %v, org_id %v", e.Name(), e.HasColumn("deleted_at"), e.HasColumn("org_id"))
	}
	if n := filterent.New(engine.StandInInfo("Tag", "id")); n.Name() != "Tag" || !n.HasColumn("id") {
		t.Errorf("New: %q, id %v", n.Name(), n.HasColumn("id"))
	}
	var zero filterent.Entity
	if zero.Name() != "" || zero.HasColumn("id") {
		t.Errorf("zero Entity: %q, id %v", zero.Name(), zero.HasColumn("id"))
	}
}
