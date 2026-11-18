package engine

import (
	"reflect"
	"testing"
)

// cols is the slots of the columns cs, with -1 for a value.
func cols(cs ...int32) []ExprSlot {
	out := make([]ExprSlot, len(cs))
	for i, c := range cs {
		out[i] = ExprSlot{Col: c}
	}
	return out
}

func colsOf(d *exprData) []int32 {
	out := make([]int32, len(d.slots))
	for i, s := range d.slots {
		out[i] = s.Col
	}
	return out
}

// TestSwapExpr: on a bidirectional join entity an Expr's columns 0 and 1
// trade places, as a leaf's do, in its terms too, and the original is left
// as it was.
func TestSwapExpr(t *testing.T) {
	n := ExprNode("? = ? AND ? > ?", cols(0, 1, 2, -1), []any{5})
	if n.Op != OpExpr {
		t.Fatalf("got %v", n)
	}
	got := swapCols(n).V.(*exprData)
	if want := []int32{1, 0, 2, -1}; !reflect.DeepEqual(colsOf(got), want) {
		t.Errorf("swapped cols %v, want %v", colsOf(got), want)
	}
	if want := []int32{0, 1, 2, -1}; !reflect.DeepEqual(colsOf(n.V.(*exprData)), want) {
		t.Errorf("the original is now %v, want %v", colsOf(n.V.(*exprData)), want)
	}
	if !reflect.DeepEqual(got.args, []any{5}) || !reflect.DeepEqual(got.parts, n.V.(*exprData).parts) {
		t.Errorf("swapping changed the text or values: %+v", got)
	}
	// A term in a slot swaps too.
	sum := AggTerm(TermSum, ColumnTerm(0))
	m := ExprNode("? > 1", []ExprSlot{{Col: -1, Term: &sum}}, nil)
	swapped := swapCols(m).V.(*exprData).slots[0].Term
	if swapped.of.col != 1 || sum.of.col != 0 {
		t.Errorf("a term's column: swapped %d, original %d", swapped.of.col, sum.of.col)
	}
}

// TestExprColumnsChecked: an Expr that names a column the entity lacks,
// directly or in a term, is refused by the statement checks, not left to
// panic in rendering.
func TestExprColumnsChecked(t *testing.T) {
	in := &EntityInfo{Name: "E", Columns: make([]ColumnInfo, 2)}
	n := ExprNode("? = 1", cols(7), nil)
	if err := checkTags(n, in); err == nil {
		t.Error("checkTags accepted column 7 of 2")
	}
	if err := checkTags(ExprNode("? = 1", cols(1), nil), in); err != nil {
		t.Errorf("checkTags refused column 1 of 2: %v", err)
	}
	st := &Statement{Entity: in}
	if err := checkNode(st, "a statement names", n); err == nil {
		t.Error("checkNode accepted column 7 of 2")
	}
	far := AggTerm(TermSum, ColumnTerm(9))
	if err := checkNode(st, "a statement names", ExprNode("? > 1", []ExprSlot{{Col: -1, Term: &far}}, nil)); err == nil {
		t.Error("checkNode accepted column 9 of 2 inside a term")
	}
}
