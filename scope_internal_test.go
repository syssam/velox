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

// TestResolveNodeSkipsTreesWithoutExists: resolveNode returns an And, Or or
// Not that holds no EXISTS as it is, without walking it or allocating, and
// resolves one that does without editing it.
func TestResolveNodeSkipsTreesWithoutExists(t *testing.T) {
	id := NumberCol[company, int64]{I: 0}
	a, b, c := id.EQ(1), id.GT(2), id.LT(9)
	for _, p := range []Pred[company]{And(a, Or(b, c)), Not(Or(b, c)), Or(And(a, b), c)} {
		var got node
		var changed bool
		var err error
		allocs := testing.AllocsPerRun(100, func() { got, changed, err = resolveNode(p.n) })
		if changed || err != nil || allocs != 0 || len(got.kids) != len(p.n.kids) || &got.kids[0] != &p.n.kids[0] {
			t.Errorf("%v: changed %v, err %v, %.0f allocations, kids shared %v", p.n.op, changed, err, allocs, &got.kids[0] == &p.n.kids[0])
		}
	}
	has := Rel[company, employee]{I: 0}.Has()
	// The skip trusts deep: a node that says it holds no EXISTS is not
	// walked, so one built without the mark is returned unresolved.
	unmarked := node{op: opAnd, kids: []node{a.n, has.n}}
	if got, changed, err := resolveNode(unmarked); changed || err != nil || &got.kids[0] != &unmarked.kids[0] {
		t.Errorf("an And not marked deep was walked: changed %v, err %v", changed, err)
	}
	deep := And(a, Or(b, has))
	got, changed, err := resolveNode(deep.n)
	if err != nil || !changed {
		t.Fatalf("And(a, Or(b, Has())): changed %v, err %v", changed, err)
	}
	if _, ok := deep.n.kids[1].kids[1].v.(relRef); !ok {
		t.Error("resolveNode edited the Pred it was given")
	}
	if _, ok := got.kids[1].kids[1].v.(link); !ok {
		t.Errorf("the EXISTS is not resolved: %T", got.kids[1].kids[1].v)
	}
	if &got.kids[1].kids[0] == &deep.n.kids[1].kids[0] {
		t.Error("the resolved Or shares its kids slice with the Pred given")
	}
}
