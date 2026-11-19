package engine

import (
	"errors"
	"testing"
)

// TestInExists: an error from inside an EXISTS names the statement's entity,
// then the targets from the outermost in, once each, and errors.Is still sees
// it.
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
}
