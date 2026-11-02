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
	m := &Mutation[hooked]{mu: engine.MutationState{St: &st}}
	if m.Op() != OpUpdate {
		t.Errorf("Op %v", m.Op())
	}
	if v, ok := hookedTitle.ValueIn(m); !ok || v != "t" {
		t.Errorf("Title.ValueIn = %v, %v", v, ok)
	}
	if v, ok := hookedTag.ValueIn(m); ok || v != "" {
		t.Errorf("ValueIn of a column assigned by Add = %v, %v; want nothing", v, ok)
	}
	m.Set(hookedTitle.Set("u"), hookedTag.Set("a"))
	if want := []Assignment{{Column: 2, Op: AssignSet, Value: "u"}, {Column: 1, Op: AssignSet, Value: "a"}}; !reflect.DeepEqual(st.Set, want) {
		t.Errorf("set %+v, want %+v", st.Set, want)
	}
	if set[0].Value != "t" {
		t.Error("Set changed the query's own assignments")
	}
	m.Where(hookedTitle.EQ("u"))
	if m.mu.Err != nil || len(st.Where) != 1 {
		t.Fatalf("Where: err %v, %d conditions", m.mu.Err, len(st.Where))
	}
	m.Where(Pred[hooked]{})
	if m.mu.Err == nil || !strings.Contains(m.mu.Err.Error(), "zero Predicate") || len(st.Where) != 1 {
		t.Errorf("a zero condition: err %v, %d conditions", m.mu.Err, len(st.Where))
	}
	bad := errors.New("unresolved")
	m = &Mutation[hooked]{mu: engine.MutationState{St: &st}}
	m.Where(Pred[hooked](engine.NewPredicate(engine.Node{Op: engine.OpInvalid, V: bad})))
	if !errors.Is(m.mu.Err, bad) || len(st.Where) != 1 {
		t.Errorf("an invalid condition: err %v, %d conditions", m.mu.Err, len(st.Where))
	}
	del := Statement{Op: OpDelete, Entity: info}
	m = &Mutation[hooked]{mu: engine.MutationState{St: &del}}
	m.Set(hookedTag.Set("x"))
	if m.mu.Err == nil || del.Set != nil {
		t.Errorf("Set on a delete: err %v, set %+v", m.mu.Err, del.Set)
	}
}
