package migrate

import (
	"testing"
)

// TestReviewLine: a review line holds review and destructive findings only,
// each distinct one once, on one line.
func TestReviewLine(t *testing.T) {
	fs := []Finding{
		{Code: "widen-type", Class: Safe, Change: 0, Message: "widens"},
		{Code: "rename-column", Class: Review, Change: 1, Message: "renames"},
		{Code: "drop-column", Class: Destructive, Change: 1, Message: "drops\r\nit"},
		{Code: "rename-column", Class: Review, Change: 2, Message: "renames"},
	}
	r := &renderer{findings: reviewed(fs, 3), taken: make([]bool, 3)}
	if got := r.review([]int{0}, nil); got != "" {
		t.Errorf("a safe finding: %q", got)
	}
	if got, want := r.review([]int{1, 2}, nil), "rename-column: renames; drop-column: drops  it"; got != want {
		t.Errorf("review %q, want %q", got, want)
	}
	if got := r.review([]int{1}, nil); got != "" {
		t.Errorf("a second statement: %q", got)
	}
}
