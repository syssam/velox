package engine

import (
	"testing"
)

// A DefaultFunc returns a pointer field's element type, which FillDefault
// stores through a new pointer; a set pointer keeps its value.
func TestFillDefaultFuncOnPointer(t *testing.T) {
	c := ColumnInfo{DefaultFunc: func() any { return 3 }}
	var p *int
	FillDefault(&c, &p)
	if p == nil || *p != 3 {
		t.Fatalf("unset pointer: %v", p)
	}
	zero := 0
	p = &zero
	FillDefault(&c, &p)
	if *p != 0 {
		t.Fatalf("a set 0 was replaced by %d", *p)
	}
	var v int
	FillDefault(&c, &v)
	if v != 3 {
		t.Fatalf("value field: %d", v)
	}
}

// TestCheckColumnsAllow: an Allow names no column, so CheckColumns does not
// check its col, which on an entity of no columns would be out of range.
func TestCheckColumnsAllow(t *testing.T) {
	st := &Statement{Op: OpSelect, Entity: &EntityInfo{Name: "none"}, Where: []Predicate{{Node{Op: OpAllow}}}}
	if err := CheckColumns(st); err != nil {
		t.Fatal(err)
	}
}
