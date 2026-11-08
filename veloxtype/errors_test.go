package veloxtype_test

import (
	"errors"
	"testing"

	"github.com/syssam/velox/veloxtype"
)

// TestConstraintKindString: each kind's name, and "unknown" for any other.
func TestConstraintKindString(t *testing.T) {
	for k, want := range map[veloxtype.ConstraintKind]string{
		veloxtype.ConstraintUnique:     "unique",
		veloxtype.ConstraintForeignKey: "foreign key",
		veloxtype.ConstraintNotNull:    "not null",
		veloxtype.ConstraintCheck:      "check",
		0:                              "unknown",
		99:                             "unknown",
	} {
		if got := k.String(); got != want {
			t.Errorf("%d: %q, want %q", k, got, want)
		}
	}
}

// TestErrorTexts: PolicyError's text with and without its cause, and
// EdgeError's, which unwraps to its cause.
func TestErrorTexts(t *testing.T) {
	cause := errors.New("no role")
	if got := (&veloxtype.PolicyError{Entity: "gen.Post", Op: veloxtype.OpSelect}).Error(); got != "velox: gen.Post: select denied by policy" {
		t.Errorf("PolicyError: %q", got)
	}
	if got := (&veloxtype.PolicyError{Entity: "gen.Post", Op: veloxtype.OpSelect, Err: cause}).Error(); got != "velox: gen.Post: select denied by policy: no role" {
		t.Errorf("PolicyError with Err: %q", got)
	}
	e := &veloxtype.EdgeError{Entity: "gen.Post", Edge: "tags", Keys: []any{int64(3)}, Err: cause}
	if got := e.Error(); got != "velox: gen.Post: edge tags: no role" {
		t.Errorf("EdgeError: %q", got)
	}
	if !errors.Is(e, cause) {
		t.Error("EdgeError does not unwrap to its Err")
	}
}
