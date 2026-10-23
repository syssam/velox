package migrate

import (
	"slices"
	"testing"
)

// TestReviewsLeaveOutSafe: a review line holds review and destructive
// findings only.
func TestReviewsLeaveOutSafe(t *testing.T) {
	got := reviews([]Finding{
		{Code: "widen-type", Class: Safe, Change: 0, Message: "widens"},
		{Code: "rename-column", Class: Review, Change: 1, Message: "renames"},
		{Code: "drop-column", Class: Destructive, Change: 1, Message: "drops"},
	}, 2)
	if want := []string{"", "rename-column: renames; drop-column: drops"}; !slices.Equal(got, want) {
		t.Errorf("reviews %q, want %q", got, want)
	}
}
