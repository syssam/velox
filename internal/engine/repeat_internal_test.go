package engine

import (
	"errors"
	"testing"
)

// TestSameTerm: terms are the same when their kind, function, columns,
// text and values match, names aside; values == cannot compare never are.
func TestSameTerm(t *testing.T) {
	expr := func(text string, args ...any) Term {
		slots := make([]ExprSlot, len(args))
		for i := range slots {
			slots[i] = ExprSlot{Col: -1}
		}
		return ExprTerm(text, slots, args)
	}
	withCol := func(col int32, v any) Term { return ExprTerm("? + ?", []ExprSlot{{Col: col}, {Col: -1}}, []any{v}) }
	withTerm := func(inner Term) Term { return ExprTerm("? + 1", []ExprSlot{{Col: -1, Term: &inner}}, nil) }
	json := func(keys ...string) Term { return JSONTerm(1, JSONPath{Kind: JSONKindText, Keys: keys}) }
	a := expr("? * ?", 1, 2)
	for i, c := range []struct {
		a, b Term
		want bool
	}{
		{a, a, true},
		{a, expr("? * ?", 1, 2), true},
		{a, NamedTerm(expr("? * ?", 1, 2), "x"), true},
		{a, expr("? * ?", 1, 3), false},
		{a, expr("? * ?", 1, int64(2)), false},
		{a, expr("? / ?", 1, 2), false},
		{a, expr("? * ? * 1", 1, 2), false},
		{expr("?", []byte("x")), expr("?", []byte("x")), false},
		{expr("?", nil), expr("?", nil), true},
		{expr("?", any([]int(nil))), expr("?", any([]int(nil))), false},
		{withCol(1, 5), withCol(1, 5), true},
		{withCol(1, 5), withCol(2, 5), false},
		{withTerm(a), withTerm(expr("? * ?", 1, 2)), true},
		{withTerm(a), withTerm(expr("? * ?", 1, 3)), false},
		{withTerm(a), expr("? + 1", 7), false},
		{ColumnTerm(1), ColumnTerm(1), true},
		{ColumnTerm(1), ColumnTerm(2), false},
		{AggTerm(TermSum, a), AggTerm(TermSum, expr("? * ?", 1, 2)), true},
		{AggTerm(TermSum, a), AggTerm(TermAvg, a), false},
		{AggTerm(TermCountAll, Term{}), AggTerm(TermCountAll, Term{}), true},
		{json("a", "b"), json("a", "b"), true},
		{json("a", "b"), json("a", "c"), false},
		{InvalidTerm(errors.New("x")), InvalidTerm(errors.New("x")), false},
	} {
		if got := sameTerm(c.a, c.b); got != c.want {
			t.Errorf("%d: sameTerm = %v, want %v", i, got, c.want)
		}
	}
	for i, c := range []struct {
		t    Term
		want bool
	}{
		{a, true}, {expr("1"), false}, {withTerm(a), true}, {withTerm(expr("1")), false},
		{AggTerm(TermSum, a), true}, {AggTerm(TermCountAll, Term{}), false},
		{json("a"), true}, {json(), false}, {ColumnTerm(1), false},
	} {
		if got := termArgs(c.t); got != c.want {
			t.Errorf("%d: termArgs = %v, want %v", i, got, c.want)
		}
	}
}
