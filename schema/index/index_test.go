package index_test

import (
	"fmt"
	"runtime"
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

func TestPos(t *testing.T) {
	_, file, line, _ := runtime.Caller(0)
	a := index.Fields("x")
	b := index.Edges("y")
	if want := fmt.Sprintf("%s:%d", file, line+1); a.Descriptor().Pos != want {
		t.Errorf("Fields: %q, want %q", a.Descriptor().Pos, want)
	}
	if want := fmt.Sprintf("%s:%d", file, line+2); b.Descriptor().Pos != want {
		t.Errorf("Edges: %q, want %q", b.Descriptor().Pos, want)
	}
}
