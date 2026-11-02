package engine

import (
	"errors"
	"testing"
)

// TestInExists: an error from inside an EXISTS names the statement's entity,
// then the targets from the outermost in, once each, and errors.Is still sees
// it. A cycle names the statement's entity already, and passes unchanged.
func TestInExists(t *testing.T) {
	sc := &Scoper{Entity: "a"}
	inner, outer := &EntityInfo{Name: "b"}, &EntityInfo{Name: "c"}
	boom := errors.New("velox: b: boom")
	err := sc.inExists(inner, boom)
	if want := "velox: a: in the EXISTS on b: boom"; err.Error() != want {
		t.Errorf("got %v, want %s", err, want)
	}
	err = sc.inExists(outer, err)
	if want := "velox: a: in the EXISTS on c, then on b: boom"; err.Error() != want {
		t.Errorf("got %v, want %s", err, want)
	}
	if !errors.Is(err, boom) {
		t.Errorf("%v does not wrap the error", err)
	}
	cycle := &cycleError{entity: "a", policy: "b"}
	if got := sc.inExists(outer, cycle); got != error(cycle) {
		t.Errorf("a cycle became %v", got)
	}
	if want := "velox: a: the Read policy of b reaches its own Read through an EXISTS; such a policy cannot be applied"; cycle.Error() != want {
		t.Errorf("got %v, want %s", cycle, want)
	}
}
