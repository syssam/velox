package engine

import "testing"

// TestEdgeKeyID: a key that is not comparable, a byte slice, is told apart
// by its bytes; another is itself.
func TestEdgeKeyID(t *testing.T) {
	if keyID([]byte("ab")) != keyID([]byte("ab")) || keyID([]byte("ab")) == keyID([]byte("ac")) {
		t.Error("byte keys")
	}
	if keyID(int64(1)) != any(int64(1)) {
		t.Error("an int64 key")
	}
	if got := missing([]any{[]byte("a"), []byte("b")}, []any{[]byte("b")}); len(got) != 1 || string(got[0].([]byte)) != "a" {
		t.Errorf("missing: %v", got)
	}
	if e := EdgeOf(Assignment{Column: 1, Op: AssignSet}); e != nil {
		t.Errorf("EdgeOf a column's assignment: %v", e)
	}
}
