package index_test

import (
	"fmt"
	"runtime"
	"slices"
	"testing"

	"github.com/syssam/velox/schema/index"
	"github.com/syssam/velox/schema/internal/desc"
)

func TestIndex(t *testing.T) {
	d := index.Fields("org_id", "name").Edges("owner").Unique().StorageKey("ux").Where("deleted_at IS NULL").Method("gin").Descriptor()
	if len(d.Fields) != 2 || d.Edges[0] != "owner" || !d.Unique || d.StorageKey != "ux" || d.Where != "deleted_at IS NULL" || d.Method != "gin" {
		t.Fatalf("%+v", d)
	}
}

// Desc and OpClass add up over calls, in order, and name what the index covers.
func TestIndexKeyOptions(t *testing.T) {
	d := index.Fields("a", "b").Desc("a").Desc("b", "a").OpClass("a", "text_pattern_ops").OpClass("b", "x_ops").Descriptor()
	if !slices.Equal(d.Desc, []string{"a", "b", "a"}) {
		t.Errorf("Desc %v", d.Desc)
	}
	if want := []desc.IndexOpClass{{Name: "a", Class: "text_pattern_ops"}, {Name: "b", Class: "x_ops"}}; !slices.Equal(d.OpClass, want) {
		t.Errorf("OpClass %v", d.OpClass)
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
	if want := fmt.Sprintf("%s:%d", file, line+1); desc.PosOf(a.Descriptor().PC) != want {
		t.Errorf("Fields: %q, want %q", desc.PosOf(a.Descriptor().PC), want)
	}
	if want := fmt.Sprintf("%s:%d", file, line+2); desc.PosOf(b.Descriptor().PC) != want {
		t.Errorf("Edges: %q, want %q", desc.PosOf(b.Descriptor().PC), want)
	}
}
