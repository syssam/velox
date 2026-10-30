package velox

import (
	"context"
	"testing"

	"github.com/syssam/velox2/internal/engine"
)

// TestScopeSkipsTreesWithoutExists: And, Or and Not mark a node that holds
// an EXISTS at any depth, and the scope returns a tree without one as it is.
func TestScopeSkipsTreesWithoutExists(t *testing.T) {
	a, b, c := hn.ID.EQ(1), hn.N.GT(2), hn.Title.EQ("c")
	plain := And(a, Or(b, c))
	if engine.PredNode(Predicate(plain)).Deep || engine.PredNode(Predicate(plain)).Kids[1].Deep {
		t.Errorf("And(a, Or(b, c)): deep %v, Or's %v", engine.PredNode(Predicate(plain)).Deep, engine.PredNode(Predicate(plain)).Kids[1].Deep)
	}
	has := Rel[hnote, hnote]{I: 0}.Has()
	exists := And(a, Or(b, has))
	if !engine.PredNode(Predicate(exists)).Deep || !engine.PredNode(Predicate(exists)).Kids[1].Deep {
		t.Errorf("And(a, Or(b, Has())): deep %v, Or's %v", engine.PredNode(Predicate(exists)).Deep, engine.PredNode(Predicate(exists)).Kids[1].Deep)
	}
	if n := Not(Or(b, has)); !engine.PredNode(Predicate(n)).Deep {
		t.Error("Not(Or(b, Has())) is not deep")
	}
	if n := And(Allow[hnote](), a, has); !engine.PredNode(Predicate(n)).Deep {
		t.Error("And(Allow(), a, Has()) is not deep")
	}
	leaves := make([]Pred[hnote], 1000)
	for i := range leaves {
		leaves[i] = hn.N.EQ(int64(i))
	}
	big := And(leaves...)
	sc := &engine.Scoper{Ctx: context.Background(), Entity: "velox.hnote"}
	var changed bool
	var err error
	allocs := testing.AllocsPerRun(100, func() { _, changed, err = sc.Node(engine.PredNode(Predicate(big))) })
	if changed || err != nil || allocs != 0 {
		t.Errorf("a 1000-leaf And without EXISTS: changed %v, err %v, %.0f allocations", changed, err, allocs)
	}
}

// TestResolveNodeSkipsTreesWithoutExists: ResolveNode returns an And, Or or
// Not that holds no EXISTS as it is, without walking it or allocating, and
// resolves one that does without editing it.
func TestResolveNodeSkipsTreesWithoutExists(t *testing.T) {
	id := NumberCol[company, int64]{I: 0}
	a, b, c := id.EQ(1), id.GT(2), id.LT(9)
	for _, p := range []Pred[company]{And(a, Or(b, c)), Not(Or(b, c)), Or(And(a, b), c)} {
		var got engine.Node
		var changed bool
		var err error
		allocs := testing.AllocsPerRun(100, func() { got, changed, err = engine.ResolveNode(engine.PredNode(Predicate(p))) })
		if changed || err != nil || allocs != 0 || len(got.Kids) != len(engine.PredNode(Predicate(p)).Kids) || &got.Kids[0] != &engine.PredNode(Predicate(p)).Kids[0] {
			t.Errorf("%v: changed %v, err %v, %.0f allocations, kids shared %v", engine.PredNode(Predicate(p)).Op, changed, err, allocs, &got.Kids[0] == &engine.PredNode(Predicate(p)).Kids[0])
		}
	}
	has := Rel[company, employee]{I: 0}.Has()
	// The skip trusts deep: a node that says it holds no EXISTS is not
	// walked, so one built without the mark is returned unresolved.
	unmarked := engine.Node{Op: engine.OpAnd, Kids: []engine.Node{engine.PredNode(Predicate(a)), engine.PredNode(Predicate(has))}}
	if got, changed, err := engine.ResolveNode(unmarked); changed || err != nil || &got.Kids[0] != &unmarked.Kids[0] {
		t.Errorf("an And not marked deep was walked: changed %v, err %v", changed, err)
	}
	deep := And(a, Or(b, has))
	got, changed, err := engine.ResolveNode(engine.PredNode(Predicate(deep)))
	if err != nil || !changed {
		t.Fatalf("And(a, Or(b, Has())): changed %v, err %v", changed, err)
	}
	if _, ok := engine.PredNode(Predicate(deep)).Kids[1].Kids[1].V.(engine.RelRef); !ok {
		t.Error("resolveNode edited the Pred it was given")
	}
	if _, ok := got.Kids[1].Kids[1].V.(engine.Link); !ok {
		t.Errorf("the EXISTS is not resolved: %T", got.Kids[1].Kids[1].V)
	}
	if &got.Kids[1].Kids[0] == &engine.PredNode(Predicate(deep)).Kids[1].Kids[0] {
		t.Error("the resolved Or shares its kids slice with the Pred given")
	}
}
