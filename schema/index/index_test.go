package index_test

import (
	"testing"

	"github.com/syssam/velox2/schema/index"
)

func TestIndex(t *testing.T) {
	d := index.Fields("org_id", "name").Edges("owner").Unique().StorageKey("ux").Where("deleted_at IS NULL").Descriptor()
	if len(d.Fields) != 2 || d.Edges[0] != "owner" || !d.Unique || d.StorageKey != "ux" || d.Where != "deleted_at IS NULL" {
		t.Fatalf("%+v", d)
	}
}

func TestEdgesFirstAppends(t *testing.T) {
	d := index.Edges("a").Edges("b").Fields("x").Fields("y").Descriptor()
	if len(d.Edges) != 2 || len(d.Fields) != 2 {
		t.Fatalf("%+v", d)
	}
}
