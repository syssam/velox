package velox

import (
	"testing"

	"github.com/syssam/velox/internal/engine"
)

// TestEveryHandleSelects: each comparable handle's Select, InSelect and
// NotInSelect build the correlated EXISTS of a ColRef on their columns.
func TestEveryHandleSelects(t *testing.T) {
	check := func(name string, p Pred[ent], wantNot bool, owner, target int32) {
		t.Helper()
		n := engine.PredNode(Predicate(p))
		if wantNot {
			if n.Op != engine.OpNot {
				t.Fatalf("%s: %+v, want a Not", name, n)
			}
			n = n.Kids[0]
		}
		r, ok := n.V.(engine.ColRef)
		if n.Op != engine.OpExists || n.Col != -1 || !ok || r.OwnerCol != owner || r.TargetCol != target {
			t.Errorf("%s: %+v", name, n)
		}
	}
	check("Col", Col[ent, bool]{I: 1}.InSelect(Col[ent, bool]{I: 2}.Select()), false, 1, 2)
	check("Col not", Col[ent, bool]{I: 1}.NotInSelect(Col[ent, bool]{I: 2}.Select()), true, 1, 2)
	check("OrderedCol", OrderedCol[ent, int]{I: 1}.InSelect(OrderedCol[ent, int]{I: 2}.Select()), false, 1, 2)
	check("OrderedCol not", OrderedCol[ent, int]{I: 1}.NotInSelect(OrderedCol[ent, int]{I: 2}.Select()), true, 1, 2)
	check("NumberCol", NumberCol[ent, int]{I: 1}.InSelect(NumberCol[ent, int]{I: 2}.Select()), false, 1, 2)
	check("NumberCol not", NumberCol[ent, int]{I: 1}.NotInSelect(NumberCol[ent, int]{I: 2}.Select()), true, 1, 2)
	check("StringCol", StringCol[ent, string]{I: 1}.InSelect(StringCol[ent, string]{I: 2}.Select()), false, 1, 2)
	check("StringCol not", StringCol[ent, string]{I: 1}.NotInSelect(StringCol[ent, string]{I: 2}.Select()), true, 1, 2)
	check("NullCol", NullCol[ent, bool]{I: 1}.InSelect(NullCol[ent, bool]{I: 2}.Select()), false, 1, 2)
	check("NullCol not", NullCol[ent, bool]{I: 1}.NotInSelect(NullCol[ent, bool]{I: 2}.Select()), true, 1, 2)
	check("NullOrderedCol", NullOrderedCol[ent, int]{I: 1}.InSelect(NullOrderedCol[ent, int]{I: 2}.Select()), false, 1, 2)
	check("NullOrderedCol not", NullOrderedCol[ent, int]{I: 1}.NotInSelect(NullOrderedCol[ent, int]{I: 2}.Select()), true, 1, 2)
	check("NullNumberCol", NullNumberCol[ent, int]{I: 1}.InSelect(NullNumberCol[ent, int]{I: 2}.Select()), false, 1, 2)
	check("NullNumberCol not", NullNumberCol[ent, int]{I: 1}.NotInSelect(NullNumberCol[ent, int]{I: 2}.Select()), true, 1, 2)
	check("NullStringCol", NullStringCol[ent, string]{I: 1}.InSelect(NullStringCol[ent, string]{I: 2}.Select()), false, 1, 2)
	check("NullStringCol not", NullStringCol[ent, string]{I: 1}.NotInSelect(NullStringCol[ent, string]{I: 2}.Select()), true, 1, 2)
}

// TestSelectOfAnInvalidCondition: Select keeps a condition's error, and the
// InSelect made from it is that error.
func TestSelectOfAnInvalidCondition(t *testing.T) {
	invalid := NumberCol[ent, int]{I: 1}.InSelect(Subquery[int]{}) // the zero Subquery
	s := NumberCol[ent, int]{I: 2}.Select(invalid)
	if s.err != errZeroSubquery {
		t.Fatalf("Select: %v", s.err)
	}
	n := engine.PredNode(Predicate(NumberCol[ent, int]{I: 3}.InSelect(s)))
	if n.Op != engine.OpInvalid || n.V != errZeroSubquery {
		t.Errorf("InSelect: %+v", n)
	}
}
