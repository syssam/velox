package velox

import (
	"reflect"
	"testing"
	"unsafe"
)

func buildOrder() {
	x := new(order)
	rels := make([]Relation, len(orderRels))
	for i, n := range orderRels {
		rels[i] = Relation{name: n}
	}
	if _, err := buildInfo(reflect.TypeFor[order](), unsafe.Pointer(x), any(x), orderCols, rels); err != nil {
		panic(err)
	}
}

// TestMetadataAllocationBudget pins design §8's first-use cost by its
// allocations (36 measured in the design's simulation, budget 72); its time,
// at most 20 µs, is BenchmarkMetadataFirstUse's to report.
func TestMetadataAllocationBudget(t *testing.T) {
	n := testing.AllocsPerRun(100, buildOrder)
	t.Logf("building an entity's metadata makes %.0f allocations", n)
	if n > 72 {
		t.Errorf("building an entity's metadata makes %.0f allocations, over 72", n)
	}
}

func BenchmarkMetadataFirstUse(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		buildOrder()
	}
}
