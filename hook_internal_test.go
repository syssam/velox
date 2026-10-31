package velox

import (
	"slices"
	"testing"
	"time"

	"github.com/syssam/velox/internal/engine"
)

// TestValueInRecordsAnotherType: a value of another type gives no value and
// records an error naming the column and both types; the first one recorded
// wins, as for every misuse.
func TestValueInRecordsAnotherType(t *testing.T) {
	info, err := loadDesc(hnoteDesc())
	if err != nil {
		t.Fatal(err)
	}
	st := Statement{Op: OpUpdate, Entity: info, Set: []Assignment{{Column: 4, Op: AssignSet, Value: "x"}, {Column: 1, Op: AssignSet, Value: 5}}}
	m := &Mutation[hnote]{mu: engine.MutationState{St: &st}}
	if v, ok := hn.Due.ValueIn(m); v != nil || ok {
		t.Errorf("Due: %v, %v", v, ok)
	}
	const first = `column "due" is assigned a value of type string, not int64`
	if m.mu.Err == nil || m.mu.Err.Error() != first {
		t.Fatalf("recorded %v, want %q", m.mu.Err, first)
	}
	if v, ok := hn.Title.ValueIn(m); v != "" || ok {
		t.Errorf("Title: %q, %v", v, ok)
	}
	if m.mu.Err.Error() != first {
		t.Errorf("the second replaced the first: %v", m.mu.Err)
	}
	m = &Mutation[hnote]{mu: engine.MutationState{St: &st}}
	hn.Title.ValueIn(m)
	if want := `column "title" is assigned a value of type int, not string`; m.mu.Err == nil || m.mu.Err.Error() != want {
		t.Errorf("recorded %v, want %q", m.mu.Err, want)
	}
}

type hstatus string

// TestValueInEveryKind: each handle kind's ValueIn, over what its own Set
// and SetNull assign.
func TestValueInEveryKind(t *testing.T) {
	type doc struct{ A int }
	at := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	var (
		b  = Col[hnote, bool]{I: 0}
		o  = OrderedCol[hnote, time.Time]{I: 1}
		n  = NumberCol[hnote, int64]{I: 2}
		s  = StringCol[hnote, hstatus]{I: 3}
		j  = JSONCol[hnote, doc]{I: 4}
		nb = NullCol[hnote, bool]{I: 5}
		no = NullOrderedCol[hnote, time.Time]{I: 6}
		nn = NullNumberCol[hnote, int64]{I: 7}
		ns = NullStringCol[hnote, hstatus]{I: 8}
		nj = NullJSONCol[hnote, []string]{I: 9}
		nk = NullJSONCol[hnote, doc]{I: 10}
	)
	var set []Assignment
	for _, x := range []Setter[hnote]{b.Set(true), o.Set(at), n.Set(3), s.Set("open"), j.Set(doc{A: 1}),
		nb.Set(true), no.Set(at), nn.Set(4), ns.SetNull(), nj.Set([]string{"a"}), nk.SetNull()} {
		set = append(set, x.a)
	}
	m := &Mutation[hnote]{mu: engine.MutationState{St: &Statement{Op: OpUpdate, Set: set}}}
	if v, ok := b.ValueIn(m); !v || !ok {
		t.Errorf("Col: %v, %v", v, ok)
	}
	if v, ok := o.ValueIn(m); !v.Equal(at) || !ok {
		t.Errorf("OrderedCol: %v, %v", v, ok)
	}
	if v, ok := n.ValueIn(m); v != 3 || !ok {
		t.Errorf("NumberCol: %v, %v", v, ok)
	}
	if v, ok := s.ValueIn(m); v != "open" || !ok {
		t.Errorf("StringCol of an enum: %v, %v", v, ok)
	}
	if v, ok := j.ValueIn(m); v != (doc{A: 1}) || !ok {
		t.Errorf("JSONCol: %v, %v", v, ok)
	}
	if v, ok := nb.ValueIn(m); v == nil || !*v || !ok {
		t.Errorf("NullCol: %v, %v", v, ok)
	}
	if v, ok := no.ValueIn(m); v == nil || !v.Equal(at) || !ok {
		t.Errorf("NullOrderedCol: %v, %v", v, ok)
	}
	if v, ok := nn.ValueIn(m); v == nil || *v != 4 || !ok {
		t.Errorf("NullNumberCol: %v, %v", v, ok)
	}
	if v, ok := ns.ValueIn(m); v != nil || !ok {
		t.Errorf("NullStringCol after SetNull: %v, %v", v, ok)
	}
	if v, ok := nj.ValueIn(m); v == nil || !slices.Equal(*v, []string{"a"}) || !ok {
		t.Errorf("NullJSONCol: %v, %v", v, ok)
	}
	if v, ok := nk.ValueIn(m); v != nil || !ok {
		t.Errorf("NullJSONCol after SetNull: %v, %v", v, ok)
	}
	if m.mu.Err != nil {
		t.Errorf("recorded %v", m.mu.Err)
	}
	// Unassigned, and an Add.
	m = &Mutation[hnote]{mu: engine.MutationState{St: &Statement{Op: OpUpdate, Set: []Assignment{n.Add(1).a, nn.Add(1).a}}}}
	if v, ok := n.ValueIn(m); v != 0 || ok {
		t.Errorf("NumberCol after Add: %v, %v", v, ok)
	}
	if v, ok := nn.ValueIn(m); v != nil || ok {
		t.Errorf("NullNumberCol after Add: %v, %v", v, ok)
	}
	if v, ok := j.ValueIn(m); v != (doc{}) || ok {
		t.Errorf("JSONCol unassigned: %v, %v", v, ok)
	}
	if v, ok := nj.ValueIn(m); v != nil || ok {
		t.Errorf("NullJSONCol unassigned: %v, %v", v, ok)
	}
	// On an insert, each kind records the misuse.
	for _, f := range []func(*Mutation[hnote]){
		func(m *Mutation[hnote]) { b.ValueIn(m) }, func(m *Mutation[hnote]) { o.ValueIn(m) },
		func(m *Mutation[hnote]) { n.ValueIn(m) }, func(m *Mutation[hnote]) { s.ValueIn(m) },
		func(m *Mutation[hnote]) { j.ValueIn(m) }, func(m *Mutation[hnote]) { nb.ValueIn(m) },
		func(m *Mutation[hnote]) { no.ValueIn(m) }, func(m *Mutation[hnote]) { nn.ValueIn(m) },
		func(m *Mutation[hnote]) { ns.ValueIn(m) }, func(m *Mutation[hnote]) { nj.ValueIn(m) },
	} {
		m := &Mutation[hnote]{mu: engine.MutationState{Op: OpInsert}}
		f(m)
		if m.mu.Err != engine.ErrInsertMutation {
			t.Errorf("on an insert recorded %v", m.mu.Err)
		}
	}
}
