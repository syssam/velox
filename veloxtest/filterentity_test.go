package veloxtest_test

import (
	"testing"

	"github.com/syssam/velox/veloxtest"
)

func TestFilterEntity(t *testing.T) {
	e := veloxtest.FilterEntity("Invoice", "id", "deleted_at")
	if got := e.Name(); got != "Invoice" {
		t.Fatalf("Name() = %q, want Invoice", got)
	}
	if !e.HasColumn("deleted_at") || !e.HasColumn("id") {
		t.Fatal("HasColumn is false for a given column")
	}
	if e.HasColumn("org_id") {
		t.Fatal("HasColumn is true for a column not given")
	}
	if veloxtest.FilterEntity("Tag").HasColumn("id") {
		t.Fatal("an entity given no columns has one")
	}
}
