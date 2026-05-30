// run_velox_errmap_test.go uses the "run_velox" prefix so the architecture
// guard (TestBrainHasNoORMImports) permits its velox import: it pins that
// classifyVeloxErr maps the KNOWN pagination validation sentinel to
// ErrValidation while an arbitrary unexpected error maps to ErrInternal (NOT
// ErrValidation) — the false-Pass vector this fix closes.
package runner

import (
	"errors"
	"fmt"
	"testing"

	"github.com/syssam/velox/contrib/graphql/gqlrelay"

	"velox.test/parity/model"
)

func TestClassifyVeloxErr_NilIsOK(t *testing.T) {
	if got := classifyVeloxErr(nil); got != model.ErrOK {
		t.Fatalf("classifyVeloxErr(nil) = %q, want %q", got, model.ErrOK)
	}
}

func TestClassifyVeloxErr_PaginationSentinelIsValidation(t *testing.T) {
	// The velox pagination validation sentinel must map to ErrValidation, even
	// when wrapped, so the paginate_validation_first_and_last case still passes.
	cases := []error{
		gqlrelay.ErrInvalidPagination,
		fmt.Errorf("paginate: %w", gqlrelay.ErrInvalidPagination),
	}
	for _, err := range cases {
		if got := classifyVeloxErr(err); got != model.ErrValidation {
			t.Fatalf("classifyVeloxErr(%v) = %q, want %q", err, got, model.ErrValidation)
		}
	}
}

func TestClassifyVeloxErr_UnexpectedIsInternal(t *testing.T) {
	// An arbitrary, unexpected error must NOT be relabeled ErrValidation — that
	// is the false-Pass vector. It must map to ErrInternal.
	if got := classifyVeloxErr(errors.New("boom")); got != model.ErrInternal {
		t.Fatalf("classifyVeloxErr(boom) = %q, want %q (must not be ErrValidation)", got, model.ErrInternal)
	}
}
