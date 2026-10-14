package velox

import (
	"bytes"
	"os"
	"reflect"
	"testing"

	"github.com/syssam/velox2/internal/handlegen"
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
		got, want node
	}{
		{"EQ", n.EQ(7).n, node{op: opEQ, col: 3, v: int64(7)}},
		{"In", n.In(1, 2).n, node{op: opIn, col: 3, v: []any{int64(1), int64(2)}}},
		{"Between", n.Between(1, 9).n, node{op: opBetween, col: 3, v: int64(1), w: int64(9)}},
		{"HasPrefix escapes", s.HasPrefix(`5%_\`).n, node{op: opLike, col: 4, v: `5\%\_\\%`}},
		{"HasSuffix", s.HasSuffix("x").n, node{op: opLike, col: 4, v: "%x"}},
		{"Contains", s.Contains("x").n, node{op: opLike, col: 4, v: "%x%"}},
		{"Like is raw", s.Like("a%").n, node{op: opLike, col: 4, v: "a%"}},
		{"EqualFold", s.EqualFold("Ab").n, node{op: opFold, col: 4, v: "Ab"}},
		{"IsNull", ns.IsNull().n, node{op: opIsNull, col: 5}},
		{"Not", Not(n.EQ(1)).n, node{op: opNot, kids: []node{{op: opEQ, col: 3, v: int64(1)}}}},
		{"Or", Or(n.EQ(1), n.EQ(2)).n, node{op: opOr, kids: []node{{op: opEQ, col: 3, v: int64(1)}, {op: opEQ, col: 3, v: int64(2)}}}},
		{"ColumnEQ", ColumnEQ(2, "t").n, node{op: opEQ, col: 2, v: "t"}},
	}
	for _, c := range cases {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s: got %+v, want %+v", c.name, c.got, c.want)
		}
	}
	if o := n.Desc(); o.t != (OrderTerm{Column: 3, Desc: true}) {
		t.Errorf("Desc: %+v", o.t)
	}
	var c Column[ent] = n
	if got := c.column(nil); got != 3 {
		t.Errorf("column: %d", got)
	}
}

func TestHandlesFileIsCurrent(t *testing.T) {
	want, err := handlegen.Generate()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("handles.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Error("handles.go is out of date: run go generate .")
	}
}
