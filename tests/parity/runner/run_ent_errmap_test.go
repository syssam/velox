// run_ent_errmap_test.go uses the "run_ent" prefix so the architecture guard
// (TestBrainHasNoORMImports) permits its ent import: it pins that classifyEntErr
// maps ent's KNOWN pagination validation error (a *gqlerror.Error, as returned
// by validateFirstLast in ent/gql_pagination.go) to ErrValidation while an
// arbitrary unexpected error maps to ErrInternal (NOT ErrValidation).
package runner

import (
	"errors"
	"fmt"
	"testing"

	"github.com/vektah/gqlparser/v2/gqlerror"

	"velox.test/parity/model"
)

func TestClassifyEntErr_NilIsOK(t *testing.T) {
	if got := classifyEntErr(nil); got != model.ErrOK {
		t.Fatalf("classifyEntErr(nil) = %q, want %q", got, model.ErrOK)
	}
}

func TestClassifyEntErr_PaginationValidationIsValidation(t *testing.T) {
	// ent's validateFirstLast returns a *gqlerror.Error (the first+last case
	// carries no errcode), so the classifier detects the typed error. It must
	// map to ErrValidation, even when wrapped, so the
	// paginate_validation_first_and_last case still passes.
	firstAndLast := &gqlerror.Error{
		Message: "Passing both `first` and `last` to paginate a connection is not supported.",
	}
	cases := []error{
		firstAndLast,
		fmt.Errorf("paginate: %w", firstAndLast),
	}
	for _, err := range cases {
		if got := classifyEntErr(err); got != model.ErrValidation {
			t.Fatalf("classifyEntErr(%v) = %q, want %q", err, got, model.ErrValidation)
		}
	}
}

func TestClassifyEntErr_UnexpectedIsInternal(t *testing.T) {
	// An arbitrary, unexpected error must NOT be relabeled ErrValidation — that
	// is the false-Pass vector. It must map to ErrInternal.
	if got := classifyEntErr(errors.New("boom")); got != model.ErrInternal {
		t.Fatalf("classifyEntErr(boom) = %q, want %q (must not be ErrValidation)", got, model.ErrInternal)
	}
}
