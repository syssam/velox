package engine

import (
	"reflect"
	"testing"
)

// TestSwapExpr: on a bidirectional join entity an Expr's columns 0 and 1
// trade places, as a leaf's do, and the original is left as it was.
func TestSwapExpr(t *testing.T) {
	n := ExprNode("? = ? AND ? > ?", []int32{0, 1, 2, -1}, []any{5})
	if n.Op != OpExpr {
		t.Fatalf("got %v", n)
	}
	got := swapCols(n).V.(*exprData)
	if want := []int32{1, 0, 2, -1}; !reflect.DeepEqual(got.cols, want) {
		t.Errorf("swapped cols %v, want %v", got.cols, want)
	}
	if want := []int32{0, 1, 2, -1}; !reflect.DeepEqual(n.V.(*exprData).cols, want) {
		t.Errorf("the original is now %v, want %v", n.V.(*exprData).cols, want)
	}
	if !reflect.DeepEqual(got.args, []any{5}) || !reflect.DeepEqual(got.parts, n.V.(*exprData).parts) {
		t.Errorf("swapping changed the text or values: %+v", got)
	}
}

// TestExprColumnsChecked: an Expr that names a column the entity lacks is
// refused by the statement checks, not left to panic in rendering.
func TestExprColumnsChecked(t *testing.T) {
	in := &EntityInfo{Name: "E", Columns: make([]ColumnInfo, 2)}
	n := ExprNode("? = 1", []int32{7}, nil)
	if err := checkTags(n, in); err == nil {
		t.Error("checkTags accepted column 7 of 2")
	}
	if err := checkTags(ExprNode("? = 1", []int32{1}, nil), in); err != nil {
		t.Errorf("checkTags refused column 1 of 2: %v", err)
	}
	st := &Statement{Entity: in}
	if err := checkNode(st, "a statement names", n); err == nil {
		t.Error("checkNode accepted column 7 of 2")
	}
}
