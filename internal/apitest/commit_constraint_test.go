package apitest

import (
	"context"
	"errors"
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
