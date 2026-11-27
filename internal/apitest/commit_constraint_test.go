package apitest

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
)

// TestCommitConstraint: a constraint checked at commit (a deferred one) is a
// *ConstraintError, as one a statement violates is, with no entity, as the
// commit is the transaction's and not one statement's. The driver's error is
// still reachable.
func TestCommitConstraint(t *testing.T) {
	pg := &pgconn.PgError{Code: "23503", ConstraintName: "items_invoice_fk"}
	db := openFake(velox.Postgres, &fakeconn.Exec{CommitErr: pg})
	err := db.Tx(context.Background(), func(context.Context, *velox.DB) error { return nil })
	ce, ok := errors.AsType[*velox.ConstraintError](err)
	if !ok {
		t.Fatalf("%v: not a *ConstraintError", err)
	}
	if ce.Kind != velox.ConstraintForeignKey || ce.Constraint != "items_invoice_fk" || ce.Entity != "" {
		t.Errorf("%#v, want a foreign key items_invoice_fk with no entity", ce)
	}
	if !errors.Is(err, pg) {
		t.Errorf("%v: the driver's error is not reachable", err)
	}
	const want = "velox: commit: foreign key constraint items_invoice_fk violated: "
	if got := err.Error(); len(got) < len(want) || got[:len(want)] != want {
		t.Errorf("message %q, want it to start %q", got, want)
	}
}

// TestAsConstraintError: a driver's constraint error that velox did not wrap,
// as raw SQL returns, gives its kind and constraint name, and the function
// agrees with IsConstraintError; a velox-made one and other errors are as
// errors.As and IsConstraintError report them.
func TestAsConstraintError(t *testing.T) {
	pg := &pgconn.PgError{Code: "23505", ConstraintName: "invoices_number_key"}
	raw := fmt.Errorf("exec: %w", pg)
	if _, ok := errors.AsType[*velox.ConstraintError](raw); ok {
		t.Fatal("errors.As finds a ConstraintError in a raw driver error")
	}
	ce, ok := velox.AsConstraintError(raw)
	if !ok || ce.Kind != velox.ConstraintUnique || ce.Constraint != "invoices_number_key" || !errors.Is(ce, pg) {
		t.Errorf("raw: %#v, %v", ce, ok)
	}
	made := &velox.ConstraintError{Entity: "Invoice", Kind: velox.ConstraintUnique, Constraint: "k"}
	if got, ok := velox.AsConstraintError(fmt.Errorf("wrap: %w", made)); !ok || got != made {
		t.Errorf("made: %v, %v", got, ok)
	}
	for _, err := range []error{raw, made, errors.New("other"), nil, &pgconn.PgError{Code: "40001"}} {
		if _, ok := velox.AsConstraintError(err); ok != velox.IsConstraintError(err) {
			t.Errorf("%v: AsConstraintError %v, IsConstraintError %v", err, ok, velox.IsConstraintError(err))
		}
	}
	if ce, ok := velox.AsConstraintError(nil); ce != nil || ok {
		t.Errorf("nil: %v, %v", ce, ok)
	}
}
