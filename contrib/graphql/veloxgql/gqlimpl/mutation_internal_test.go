package gqlimpl

import (
	"errors"
	"testing"

	"github.com/syssam/velox2/contrib/graphql/veloxgql"
	"github.com/syssam/velox2/contrib/graphql/veloxgql/internal/shared"
)

// codeOf is the code of err's *shared.Error, or "".
func codeOf(err error) string {
	var e *shared.Error
	if !errors.As(err, &e) {
		return ""
	}
	return e.Code
}

// TestMatched: a write that matched fewer rows than its distinct IDs fails
// as CheckExists does; one that matched them all, or more, or whose count is
// unknown, does not fail as NOT_FOUND.
func TestMatched(t *testing.T) {
	for _, c := range []struct {
		got  int64
		n    int
		want string
	}{
		{2, 2, ""},
		{0, 0, ""},
		{0, 1, "veloxgql: CreateUserInput.postIDs: no Post has this ID"},
		{1, 3, "veloxgql: CreateUserInput.postIDs: 2 of the 3 IDs name no Post"},
	} {
		err := matched(c.got, c.n, "CreateUserInput.postIDs", "Post")
		switch {
		case c.want == "" && err != nil:
			t.Errorf("matched(%d, %d) = %v, want nil", c.got, c.n, err)
		case c.want != "" && (err == nil || err.Error() != c.want || codeOf(err) != veloxgql.CodeNotFound):
			t.Errorf("matched(%d, %d) = %v (code %q), want %q, NOT_FOUND", c.got, c.n, err, codeOf(err), c.want)
		}
	}
	if err := matched(3, 2, "at", "Post"); err == nil || codeOf(err) != "" {
		t.Errorf("matched(3, 2) = %v (code %q), want an internal error", err, codeOf(err))
	}
	// -1, a count the driver did not report, is not a short count.
	if err := matched(-1, 1, "at", "Post"); err == nil || err.Error() != "veloxgql: at: row count unknown" || codeOf(err) != "" {
		t.Errorf("matched(-1, 1) = %v (code %q), want the internal error \"veloxgql: at: row count unknown\"", err, codeOf(err))
	}
}
