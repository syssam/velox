package veloxtype_test

import (
	"errors"
	"fmt"

	"github.com/syssam/velox/veloxtype"
)

// An application reads the error's fields, in the same form on every dialect.
// It writes velox.ConstraintError, which is this type.
func ExampleConstraintError() {
	err := fmt.Errorf("create invoice: %w", &veloxtype.ConstraintError{
		Entity:     "billing.Invoice",
		Kind:       veloxtype.ConstraintUnique,
		Constraint: "invoices_number_key",
		Err:        errors.New("duplicate key"),
	})

	var ce *veloxtype.ConstraintError
	if errors.As(err, &ce) {
		fmt.Println(ce.Kind, ce.Constraint)
	}
	// Output: unique invoices_number_key
}

// A denial matches ErrDenied, and still reaches the policy's own error.
func ExamplePolicyError() {
	notOwner := errors.New("not the owner")
	var err error = &veloxtype.PolicyError{Entity: "billing.Invoice", Op: veloxtype.OpUpdate, Err: notOwner}

	fmt.Println(errors.Is(err, veloxtype.ErrDenied), errors.Is(err, notOwner))
	fmt.Println(err)
	// Output:
	// true true
	// velox: billing.Invoice: update denied by policy: not the owner
}

// errors.As finds the column a validator refused, and errors.Is its reason.
func ExampleValidationError() {
	tooLong := errors.New("too long")
	err := fmt.Errorf("save: %w", &veloxtype.ValidationError{Entity: "gen.User", Field: "Name", Err: tooLong})

	var ve *veloxtype.ValidationError
	if errors.As(err, &ve) {
		fmt.Println(ve.Field, errors.Is(err, tooLong))
	}
	// Output: Name true
}
