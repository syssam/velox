package veloxotel

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/syssam/velox2"
)

// TestErrorType maps errors to error.type without running a statement.
func TestErrorType(t *testing.T) {
	bg := context.Background()
	timedOut, cancel := context.WithDeadlineCause(bg, time.Now().Add(-time.Second), velox.ErrTimeout)
	defer cancel()
	wrap := func(err error) error { return fmt.Errorf("velox: X: %w", err) }
	constraint := func(k velox.ConstraintKind) error {
		return wrap(&velox.ConstraintError{Entity: "X", Kind: k, Err: errors.New("driver")})
	}
	cases := []struct {
		ctx  context.Context
		err  error
		want string
	}{
		{bg, wrap(velox.ErrDenied), "velox.denied"},
		// A driver's typed nil, whose Unwrap panics, is passed over.
		{bg, errors.Join((*pgconn.ConnectError)(nil), wrap(velox.ErrDenied)), "velox.denied"},
		{bg, errors.Join((*pgconn.ConnectError)(nil), constraint(velox.ConstraintUnique)), "constraint.unique"},
		{bg, &velox.PolicyError{Entity: "X", Op: velox.OpSelect}, "velox.denied"},
		{bg, wrap(velox.ErrNoTenant), "velox.no_tenant"},
		{bg, wrap(velox.ErrUnbounded), "velox.unbounded"},
		{bg, wrap(velox.ErrNoWhere), "velox.unbounded"},
		{bg, wrap(velox.ErrLimitTooLarge), "velox.unbounded"},
		{bg, wrap(velox.ErrTooManyRows), "velox.too_many_rows"},
		{bg, wrap(velox.ErrTimeout), "velox.timeout"},
		{timedOut, context.DeadlineExceeded, "velox.timeout"},
		{bg, context.DeadlineExceeded, "context.deadline_exceeded"},
		{bg, wrap(context.Canceled), "context.canceled"},
		{bg, constraint(velox.ConstraintUnique), "constraint.unique"},
		{bg, constraint(velox.ConstraintForeignKey), "constraint.foreign_key"},
		{bg, constraint(velox.ConstraintNotNull), "constraint.not_null"},
		{bg, constraint(velox.ConstraintCheck), "constraint.check"},
		{bg, wrap(velox.ErrTenantMismatch), "_OTHER"},
		{bg, wrap(velox.ErrNotFound), "_OTHER"},
		{bg, errors.New("boom"), "_OTHER"},
		// Where it would be _OTHER, the driver's code, as the conventions
		// prefer.
		{bg, &pgconn.PgError{Code: "40001"}, "40001"},
		{bg, wrap(&pgconn.PgError{Code: "40P01"}), "40P01"},
		{bg, wrap(&velox.ConstraintError{Entity: "X", Kind: velox.ConstraintUnique, Err: &pgconn.PgError{Code: "23505"}}), "constraint.unique"},
	}
	for _, c := range cases {
		if got := errorType(c.ctx, c.err); got != c.want {
			t.Errorf("errorType(%v) = %q, want %q", c.err, got, c.want)
		}
	}
}

func TestDBSystem(t *testing.T) {
	for d, want := range map[velox.Dialect]string{
		velox.Postgres: "postgresql",
		velox.MySQL:    "mysql",
		velox.SQLite:   "sqlite",
	} {
		if got := dbSystem(d); got != want {
			t.Errorf("dbSystem(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestCollection(t *testing.T) {
	st := &velox.Statement{Entity: &velox.EntityInfo{Name: "billing.Invoice", Table: "invoices"}}
	if got := collection(st); got != "invoices" {
		t.Errorf("collection = %q", got)
	}
	st.Entity = &velox.EntityInfo{Name: "billing.Invoice", Table: "invoices", Schema: "billing"}
	if got := collection(st); got != "billing.invoices" {
		t.Errorf("collection with a schema = %q", got)
	}
}

func TestOperation(t *testing.T) {
	for op, want := range map[velox.Op]string{
		velox.OpSelect: "SELECT",
		velox.OpInsert: "INSERT",
		velox.OpUpdate: "UPDATE",
		velox.OpDelete: "DELETE",
	} {
		if got := operation(&velox.Statement{Op: op}); got != want {
			t.Errorf("operation(%v) = %q, want %q", op, got, want)
		}
	}
}
