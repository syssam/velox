package velox

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/syssam/velox/internal/engine"
)

func TestMutation(t *testing.T) {
	info, err := loadDescriptor(hookedDescriptor())
	if err != nil {
		t.Fatal(err)
	}
	set := []Assignment{{Column: 2, Op: AssignSet, Value: "t"}, {Column: 1, Op: AssignAdd, Value: "z"}}
	st := Statement{Op: OpUpdate, Entity: info, Set: set[:2:2]}
	m := &Mutation{mu: engine.MutationState{St: &st}}
	if m.Op() != OpUpdate {
		t.Errorf("Op %v", m.Op())
	}
	if v, ok := m.Value("title"); !ok || v != "t" {
		t.Errorf("Value(title) = %v, %v", v, ok)
	}
	if v, ok := m.Value("tag"); ok || v != nil {
		t.Errorf("Value of a column assigned by Add = %v, %v; want nothing", v, ok)
	}
	if err := m.Set("title", "u"); err != nil {
		t.Fatal(err)
	}
	if err := m.Set("tag", "a"); err != nil {
		t.Fatal(err)
	}
	if want := []Assignment{{Column: 2, Op: AssignSet, Value: "u"}, {Column: 1, Op: AssignSet, Value: "a"}}; !reflect.DeepEqual(st.Set, want) {
		t.Errorf("set %+v, want %+v", st.Set, want)
	}
	if set[0].Value != "t" {
		t.Error("Set changed the query's own assignments")
	}
	m.Where(C("title").EQ("u"))
	if m.mu.Err != nil || len(st.Where) != 1 {
		t.Fatalf("Where: err %v, %d conditions", m.mu.Err, len(st.Where))
	}
	if cs := m.Conds(); len(cs) != 1 {
		t.Errorf("Conds: %d conditions", len(cs))
	}
	m.Where(Cond{})
	if m.mu.Err == nil || !strings.Contains(m.mu.Err.Error(), "zero Predicate") || len(st.Where) != 1 {
		t.Errorf("a zero condition: err %v, %d conditions", m.mu.Err, len(st.Where))
	}
	bad := errors.New("unresolved")
	m = &Mutation{mu: engine.MutationState{St: &st}}
	m.Where(Cond(engine.NewPredicate(engine.Node{Op: engine.OpInvalid, V: bad})))
	if !errors.Is(m.mu.Err, bad) || len(st.Where) != 1 {
		t.Errorf("an invalid condition: err %v, %d conditions", m.mu.Err, len(st.Where))
	}
	m = &Mutation{mu: engine.MutationState{St: &st}}
	if err := m.Set("title", 5); err == nil || m.mu.Err == nil {
		t.Errorf("Set of another kind: err %v, recorded %v", err, m.mu.Err)
	}
	del := Statement{Op: OpDelete, Entity: info}
	m = &Mutation{mu: engine.MutationState{St: &del}}
	if err := m.Set("tag", "x"); err == nil || m.mu.Err == nil || del.Set != nil {
		t.Errorf("Set on a delete: err %v, set %+v", err, del.Set)
	}
	ins := &Mutation{mu: engine.MutationState{Op: OpInsert, In: info}}
	ins.Where(C("title").EQ("u"))
	if !errors.Is(ins.mu.Err, engine.ErrInsertMutation) || ins.Conds() != nil {
		t.Errorf("Where on an insert: err %v", ins.mu.Err)
	}
}
