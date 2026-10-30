package engine

import (
	"context"
	"slices"
	"testing"
)

// TestMutateSetClonesOnce: the first assignment a hook appends copies the
// statement's Set, whose array may be the query's, once; later ones, an
// append or a replacement, write the copy in place.
func TestMutateSetClonesOnce(t *testing.T) {
	query := make([]Assignment, 1, 4) // spare capacity an append would write
	query[0] = Assignment{Column: 1, Op: AssignSet, Value: "a"}
	st := &Statement{Op: OpUpdate}
	mu := MutationState{St: st}
	run := func() {
		st.Set, mu.ownSet = query, false
		MutateSet(&mu, Assignment{Column: 2, Op: AssignSet, Value: "b"}) // append: the one copy
		MutateSet(&mu, Assignment{Column: 2, Op: AssignSet, Value: "c"}) // in place
		MutateSet(&mu, Assignment{Column: 1, Op: AssignSet, Value: "d"}) // in place
	}
	if allocs := testing.AllocsPerRun(100, run); allocs != 1 {
		t.Errorf("%.0f allocations, want the one copy", allocs)
	}
	want := []Assignment{{Column: 1, Op: AssignSet, Value: "d"}, {Column: 2, Op: AssignSet, Value: "c"}}
	if !slices.Equal(st.Set, want) {
		t.Errorf("Set %v, want %v", st.Set, want)
	}
	if query[0].Value != "a" || query[:2][1] != (Assignment{}) {
		t.Errorf("the query's array was written: %v", query[:2])
	}
}

// TestMutationSetClonesOnce: a hook's Set copies the statement's assignments
// once, on its first change, and writes in place after.
func TestMutationSetClonesOnce(t *testing.T) {
	orig := []Assignment{{Column: 1, Op: AssignSet, Value: "a"}, {Column: 2, Op: AssignSet, Value: "b"}}
	want := slices.Clone(orig)
	set := func(st *Statement) {
		mu := &MutationState{St: st}
		for i := range 3 {
			MutateSet(mu, Assignment{Column: orig[0].Column, Op: AssignSet, Value: i})
		}
	}
	st := &Statement{Op: OpUpdate, Set: orig}
	set(st)
	if !slices.Equal(orig, want) || st.Set[0].Value != 2 || len(st.Set) != 2 {
		t.Errorf("orig %v, Set %v", orig, st.Set)
	}
	n := testing.AllocsPerRun(100, func() {
		st := Statement{Op: OpUpdate, Set: orig}
		set(&st)
	})
	if n != 1 {
		t.Errorf("three Sets make %.0f allocations, want 1", n)
	}
}

// TestBidiDeleteKeepsTheStatement: the core sends a bidirectional delete's
// two-direction condition in a copy, so the hooks' statement keeps the Where
// they gave, and a second render gives the same SQL.
func TestBidiDeleteKeepsTheStatement(t *testing.T) {
	in := &EntityInfo{Name: "links", Table: "links", Columns: []ColumnInfo{{Name: "a"}, {Name: "b"}}, PrimaryKey: []int{0, 1}, Tenant: -1}
	SetInfoBidi(in, true)
	st := &Statement{Op: OpDelete, Entity: in, Where: []Predicate{ColumnEQ(0, int64(1))}}
	db := &DB{D: Postgres, Cfg: NewConfig(nil)}
	const want = `DELETE FROM "links" WHERE ("a" = $1 OR "b" = $2)`
	for i := range 2 {
		mu := &MutationState{St: st, Dry: true}
		if err := writeCore(context.Background(), db, mu); err != nil {
			t.Fatal(err)
		}
		if mu.SQL != want {
			t.Errorf("render %d: %s, want %s", i, mu.SQL, want)
		}
		if len(st.Where) != 1 || PredNode(st.Where[0]).Op != OpEQ || PredNode(st.Where[0]).Col != 0 {
			t.Fatalf("render %d: the statement's Where became %+v", i, st.Where)
		}
	}
}
