package engine

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// TestColRefLink: a ColRef resolves to a link with no join, and fails with
// its owner's or its target's error, or on a column out of range.
func TestColRefLink(t *testing.T) {
	owner := &EntityInfo{Name: "x.Owner", Columns: make([]ColumnInfo, 2)}
	target := &EntityInfo{Name: "x.Target", Columns: make([]ColumnInfo, 3)}
	ok := func() (*EntityInfo, error) { return owner, nil }
	tg := func() (*EntityInfo, error) { return target, nil }
	boom := errors.New("boom")
	bad := func() (*EntityInfo, error) { return nil, boom }

	l, err := ColRef{Owner: ok, Target: tg, OwnerCol: 1, TargetCol: 2}.Link()
	if err != nil || l.Target != target || l.OwnerCol != 1 || l.TargetCol != 2 || l.Join != nil {
		t.Errorf("Link: %+v, %v", l, err)
	}
	if got, err := (ColRef{Owner: ok}).owner(); got != owner || err != nil {
		t.Errorf("owner: %v, %v", got, err)
	}
	for name, tc := range map[string]struct {
		r    ColRef
		want string
	}{
		"owner fails":     {ColRef{Owner: bad, Target: tg}, "boom"},
		"target fails":    {ColRef{Owner: ok, Target: bad}, "boom"},
		"owner column":    {ColRef{Owner: ok, Target: tg, OwnerCol: 2}, "x.Owner: has no column 2"},
		"negative owner":  {ColRef{Owner: ok, Target: tg, OwnerCol: -1}, "x.Owner: has no column -1"},
		"target column":   {ColRef{Owner: ok, Target: tg, TargetCol: 3}, "x.Target: has no column 3"},
		"negative target": {ColRef{Owner: ok, Target: tg, TargetCol: -2}, "x.Target: has no column -2"},
	} {
		if _, err := tc.r.Link(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", name, err, tc.want)
		}
	}
}

// TestSwapExists: on a bidirectional join entity, a ColRef's owner column,
// lazy or resolved, is swapped, and a relation's EXISTS is kept.
func TestSwapExists(t *testing.T) {
	rel := Node{Op: OpExists, Col: 0, V: Link{OwnerCol: 0}}
	if got := swapCols(rel); !reflect.DeepEqual(got, rel) {
		t.Errorf("a relation's EXISTS changed: %+v", got)
	}
	lazy := swapCols(Node{Op: OpExists, Col: -1, V: ColRef{OwnerCol: 0, TargetCol: 4}})
	if r := lazy.V.(ColRef); r.OwnerCol != 1 || r.TargetCol != 4 {
		t.Errorf("lazy: %+v", r)
	}
	resolved := swapCols(Node{Op: OpExists, Col: -1, V: Link{OwnerCol: 1, TargetCol: 4}})
	if l := resolved.V.(Link); l.OwnerCol != 0 || l.TargetCol != 4 {
		t.Errorf("resolved: %+v", l)
	}
	other := swapCols(Node{Op: OpExists, Col: -1, V: Link{OwnerCol: 2}})
	if l := other.V.(Link); l.OwnerCol != 2 {
		t.Errorf("column 2 swapped: %+v", l)
	}
	if got := swapCols(Node{Op: OpExists, Col: -1, V: "neither"}); got.V != "neither" {
		t.Errorf("an unknown correlation changed: %+v", got)
	}
}

// TestUntypedAllow: AndPredicates and OrPredicates treat an Allow as
// velox.And and velox.Or do.
func TestUntypedAllow(t *testing.T) {
	allow := NewPredicate(Node{Op: OpAllow})
	a, b := ColumnEQ(0, 1), ColumnEQ(1, 2)
	if got := AndPredicates(allow, allow).n; got.Op != OpAllow {
		t.Errorf("And of Allows: %+v", got)
	}
	if got := AndPredicates(allow, a).n; !reflect.DeepEqual(got, a.n) {
		t.Errorf("And(Allow, a): %+v", got)
	}
	if got := AndPredicates(a, allow, b).n; got.Op != OpAnd || len(got.Kids) != 2 {
		t.Errorf("And(a, Allow, b): %+v", got)
	}
	if got := AndPredicates(a, b).n; got.Op != OpAnd || len(got.Kids) != 2 {
		t.Errorf("And(a, b): %+v", got)
	}
	if got := OrPredicates(a, allow).n; got.Op != OpAllow {
		t.Errorf("Or(a, Allow): %+v", got)
	}
	if got := OrPredicates(Predicate{}, allow).n; got.Op != OpOr {
		t.Errorf("Or(zero, Allow) keeps the zero: %+v", got)
	}
	if got := NotPredicate(a).n; got.Op != OpNot || !reflect.DeepEqual(got.Kids[0], a.n) {
		t.Errorf("Not: %+v", got)
	}
}

// TestCheckTagsSubqueries: the untyped check refuses a subquery made on
// another entity's column, checks its kids against its target, and checks
// a Through's kids against its join entity.
func TestCheckTagsSubqueries(t *testing.T) {
	in := &EntityInfo{Name: "x.In", Columns: make([]ColumnInfo, 2)}
	other := &EntityInfo{Name: "x.Other", Columns: make([]ColumnInfo, 2)}
	target := &EntityInfo{Name: "x.Target", Columns: make([]ColumnInfo, 1)}
	get := func(e *EntityInfo) func() (*EntityInfo, error) { return func() (*EntityInfo, error) { return e, nil } }
	sub := func(owner *EntityInfo, kids ...Node) Node {
		return Node{Op: OpExists, Col: -1, V: ColRef{Owner: get(owner), Target: get(target)}, Kids: kids}
	}
	for name, tc := range map[string]struct {
		n    Node
		want string // "" for none
	}{
		"subquery":            {sub(in, ColumnEQ(0, 1).n), ""},
		"another relation":    {Node{Op: OpExists, V: RelRef{Owner: get(other)}}, "x.In: a condition on a relation of x.Other"},
		"another owner":       {sub(other), "x.In: a condition on a column of x.Other"},
		"a kid out of range":  {sub(in, ColumnEQ(1, 1).n), "x.Target: a statement names column 1"},
		"invalid":             {Node{Op: OpInvalid, V: errors.New("bad")}, "bad"},
		"through":             {Node{Op: OpThrough, Col: 1, V: target, Kids: []Node{ColumnEQ(0, 1).n}}, ""},
		"through, bad kid":    {Node{Op: OpThrough, Col: 1, V: target, Kids: []Node{ColumnEQ(5, 1).n}}, "x.Target: a statement names column 5"},
		"through, bad column": {Node{Op: OpThrough, Col: 7, V: target}, "x.In: a statement names column 7"},
		"json path":           {JSONNode(1, JSONPath{Kind: JSONKindText, Keys: []string{"a"}}, Node{Op: OpEQ, V: "x"}), ""},
	} {
		err := checkTags(tc.n, in)
		if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
			t.Errorf("%s: %v, want %q", name, err, tc.want)
		}
	}
}
