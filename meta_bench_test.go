package velox

import (
	"testing"
)

// buildOrder builds the metadata of a Desc entity from its literals, as the
// first use of a generated entity does.
func buildOrder() {
	if _, err := loadDesc(orderedDesc()); err != nil {
		panic(err)
	}
}

// TestMetadataAllocationBudget pins the first-use cost by its
// allocations (36 measured in simulation, budget 72); its time,
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
