package velox

import (
	"reflect"
	"testing"

	"github.com/syssam/velox/internal/engine"
)

type ent struct{}

func TestSetters(t *testing.T) {
	n := NumberCol[ent, int64]{I: 3}
	ns := NullStringCol[ent, string]{I: 5}
	b := Col[ent, bool]{I: 6}
	for name, c := range map[string]struct{ got, want Assignment }{
		"Set":                  {n.Set(7).a, Assignment{Column: 3, Op: AssignSet, Value: int64(7)}},
		"Add":                  {n.Add(2).a, Assignment{Column: 3, Op: AssignAdd, Value: int64(2)}},
		"Sub":                  {n.Sub(2).a, Assignment{Column: 3, Op: AssignSub, Value: int64(2)}},
		"SetNull":              {ns.SetNull().a, Assignment{Column: 5, Op: AssignSet}},
		"Set on a Null handle": {ns.Set("x").a, Assignment{Column: 5, Op: AssignSet, Value: "x"}},
		"Set bool":             {b.Set(true).a, Assignment{Column: 6, Op: AssignSet, Value: true}},
	} {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s: %+v, want %+v", name, c.got, c.want)
		}
	}
}

func TestHandleMethodsBuildNodes(t *testing.T) {
	n := NumberCol[ent, int64]{I: 3}
	s := StringCol[ent, string]{I: 4}
	ns := NullStringCol[ent, string]{I: 5}
	cases := []struct {
		name      string
		got, want engine.Node
	}{
		{"EQ", engine.PredNode(Predicate(n.EQ(7))), engine.Node{Op: engine.OpEQ, Col: 3, V: int64(7)}},
		{"In", engine.PredNode(Predicate(n.In(1, 2))), engine.Node{Op: engine.OpIn, Col: 3, V: []any{int64(1), int64(2)}}},
		{"In with no values", engine.PredNode(Predicate(n.In())), engine.Node{Op: engine.OpIn, Col: 3, V: []any{}}},
		{"NotIn", engine.PredNode(Predicate(n.NotIn(1, 2))), engine.Node{Op: engine.OpNotIn, Col: 3, V: []any{int64(1), int64(2)}}},
		{"NotIn with no values", engine.PredNode(Predicate(n.NotIn())), engine.Node{Op: engine.OpNotIn, Col: 3, V: []any{}}},
		{"NEQ", engine.PredNode(Predicate(n.NEQ(7))), engine.Node{Op: engine.OpNEQ, Col: 3, V: int64(7)}},
		{"GT", engine.PredNode(Predicate(n.GT(7))), engine.Node{Op: engine.OpGT, Col: 3, V: int64(7)}},
		{"GTE", engine.PredNode(Predicate(n.GTE(7))), engine.Node{Op: engine.OpGTE, Col: 3, V: int64(7)}},
		{"LT", engine.PredNode(Predicate(n.LT(7))), engine.Node{Op: engine.OpLT, Col: 3, V: int64(7)}},
		{"LTE", engine.PredNode(Predicate(n.LTE(7))), engine.Node{Op: engine.OpLTE, Col: 3, V: int64(7)}},
		{"Col NEQ", engine.PredNode(Predicate(Col[ent, bool]{I: 6}.NEQ(true))), engine.Node{Op: engine.OpNEQ, Col: 6, V: true}},
		{"Col NotIn", engine.PredNode(Predicate(Col[ent, bool]{I: 6}.NotIn(true))), engine.Node{Op: engine.OpNotIn, Col: 6, V: []any{true}}},
		{"Between", engine.PredNode(Predicate(n.Between(1, 9))), engine.Node{Op: engine.OpBetween, Col: 3, V: int64(1), W: int64(9)}},
		{"HasPrefix escapes", engine.PredNode(Predicate(s.HasPrefix(`5%_!\`))), engine.Node{Op: engine.OpLike, Col: 4, V: `5!%!_!!\%`}},
		{"HasSuffix escapes", engine.PredNode(Predicate(s.HasSuffix("!x_"))), engine.Node{Op: engine.OpLike, Col: 4, V: "%!!x!_"}},
		{"Contains escapes", engine.PredNode(Predicate(s.Contains("a%!"))), engine.Node{Op: engine.OpLike, Col: 4, V: "%a!%!!%"}},
		{"Like is raw", engine.PredNode(Predicate(s.Like("a%"))), engine.Node{Op: engine.OpLike, Col: 4, V: "a%"}},
		{"EqualFold", engine.PredNode(Predicate(s.EqualFold("Ab"))), engine.Node{Op: engine.OpFold, Col: 4, V: "Ab"}},
		{"IsNull", engine.PredNode(Predicate(ns.IsNull())), engine.Node{Op: engine.OpIsNull, Col: 5}},
		{"Not", engine.PredNode(Predicate(Not(n.EQ(1)))), engine.Node{Op: engine.OpNot, Kids: []engine.Node{{Op: engine.OpEQ, Col: 3, V: int64(1)}}}},
		{"Or", engine.PredNode(Predicate(Or(n.EQ(1), n.EQ(2)))), engine.Node{Op: engine.OpOr, Kids: []engine.Node{{Op: engine.OpEQ, Col: 3, V: int64(1)}, {Op: engine.OpEQ, Col: 3, V: int64(2)}}}},
		{"ColumnEQ", engine.PredNode(ColumnEQ(2, "t")), engine.Node{Op: engine.OpEQ, Col: 2, V: "t"}},
	}
	for _, c := range cases {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s: got %+v, want %+v", c.name, c.got, c.want)
		}
	}
	if o := n.Desc(); o.t != (OrderTerm{Column: 3, Desc: true}) {
		t.Errorf("Desc: %+v", o.t)
	}
	if o := n.Asc(); o.t != (OrderTerm{Column: 3}) {
		t.Errorf("Asc: %+v", o.t)
	}
	if o := (Col[ent, bool]{I: 6}).Asc(); o.t != (OrderTerm{Column: 6}) {
		t.Errorf("Col Asc: %+v", o.t)
	}
	var c Column[ent] = n
	if got := c.column(nil); got != 3 {
		t.Errorf("column: %d", got)
	}
}
