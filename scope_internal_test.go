package velox

import (
	"context"
	"testing"
)

// TestScopeSkipsTreesWithoutExists: And, Or and Not mark a node that holds
// an EXISTS at any depth, and the scope returns a tree without one as it is.
func TestScopeSkipsTreesWithoutExists(t *testing.T) {
	a, b, c := hn.ID.EQ(1), hn.N.GT(2), hn.Title.EQ("c")
	plain := And(a, Or(b, c))
	if plain.n.deep || plain.n.kids[1].deep {
		t.Errorf("And(a, Or(b, c)): deep %v, Or's %v", plain.n.deep, plain.n.kids[1].deep)
	}
	has := Rel[hnote, hnote]{I: 0}.Has()
	exists := And(a, Or(b, has))
	if !exists.n.deep || !exists.n.kids[1].deep {
		t.Errorf("And(a, Or(b, Has())): deep %v, Or's %v", exists.n.deep, exists.n.kids[1].deep)
	}
	if n := Not(Or(b, has)); !n.n.deep {
		t.Error("Not(Or(b, Has())) is not deep")
	}
	if n := And(Allow[hnote](), a, has); !n.n.deep {
		t.Error("And(Allow(), a, Has()) is not deep")
	}
	leaves := make([]Pred[hnote], 1000)
	for i := range leaves {
		leaves[i] = hn.N.EQ(int64(i))
	}
	big := And(leaves...)
	sc := &scoper{ctx: context.Background(), entity: "velox.hnote"}
	var changed bool
	var err error
	allocs := testing.AllocsPerRun(100, func() { _, changed, err = sc.node(big.n) })
	if changed || err != nil || allocs != 0 {
		t.Errorf("a 1000-leaf And without EXISTS: changed %v, err %v, %.0f allocations", changed, err, allocs)
	}
}
