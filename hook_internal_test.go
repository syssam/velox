package velox

import (
	"strings"
	"testing"

	"github.com/syssam/velox/internal/engine"
)

// TestMutationValueRecordsUnknownName: a name the entity has no column of
// gives no value and records the misuse; the first one recorded wins, as
// for every misuse.
func TestMutationValueRecordsUnknownName(t *testing.T) {
	info, err := loadDescriptor(hnoteDescriptor())
	if err != nil {
		t.Fatal(err)
	}
	st := Statement{Op: OpUpdate, Entity: info, Set: []Assignment{{Column: 1, Op: AssignSet, Value: "t"}}}
	m := &Mutation{mu: engine.MutationState{St: &st}}
	if v, ok := m.Value("nope"); v != nil || ok {
		t.Errorf("nope: %v, %v", v, ok)
	}
	const first = `Value("nope"): no column "nope"`
	if m.mu.Err == nil || m.mu.Err.Error() != first {
		t.Fatalf("recorded %v, want %q", m.mu.Err, first)
	}
	if v, ok := m.Value("Title"); v != nil || ok {
		t.Errorf("a Go field name: %v, %v", v, ok)
	}
	if m.mu.Err.Error() != first {
		t.Errorf("the second replaced the first: %v", m.mu.Err)
	}
}

// TestMutationValue: Value returns what an update assigns with Set, the
// value and not a pointer for a nullable column, nil for NULL, and nothing
// for a column assigned with Add or not at all. On an insert it is a
// misuse.
func TestMutationValue(t *testing.T) {
	info, err := loadDescriptor(hnoteDescriptor())
	if err != nil {
		t.Fatal(err)
	}
	due := int64(7)
	st := Statement{Op: OpUpdate, Entity: info, Set: []Assignment{
		{Column: 1, Op: AssignSet, Value: "t"},
		{Column: 3, Op: AssignAdd, Value: int64(1)},
		{Column: 4, Op: AssignSet, Value: &due}, // as Save assigns a nullable field
	}}
	m := &Mutation{mu: engine.MutationState{St: &st}}
	if v, ok := m.Value("title"); v != "t" || !ok {
		t.Errorf("title: %v, %v", v, ok)
	}
	if v, ok := m.Value("n"); v != nil || ok {
		t.Errorf("n, assigned by Add: %v, %v", v, ok)
	}
	if v, ok := m.Value("note"); v != nil || ok {
		t.Errorf("note, unassigned: %v, %v", v, ok)
	}
	if v, ok := m.Value("due"); v != int64(7) || !ok {
		t.Errorf("due, a pointer: %v (%T), %v", v, v, ok)
	}
	if err := m.Set("due", nil); err != nil {
		t.Fatal(err)
	}
	if v, ok := m.Value("due"); v != nil || !ok {
		t.Errorf("due after Set nil: %v, %v", v, ok)
	}
	var nilDue *int64
	st.Set = []Assignment{{Column: 4, Op: AssignSet, Value: nilDue}}
	if v, ok := m.Value("due"); v != nil || !ok {
		t.Errorf("due, a nil pointer: %v, %v", v, ok)
	}
	if m.mu.Err != nil {
		t.Errorf("recorded %v", m.mu.Err)
	}

	ins := &Mutation{mu: engine.MutationState{Op: OpInsert, In: info}}
	if v, ok := ins.Value("title"); v != nil || ok {
		t.Errorf("on an insert: %v, %v", v, ok)
	}
	if ins.mu.Err == nil || !strings.Contains(ins.mu.Err.Error(), `Value("title") on an insert`) {
		t.Errorf("on an insert recorded %v", ins.mu.Err)
	}
}
