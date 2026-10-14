package velox

import (
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
)

func myErr(n uint16, msg string) error {
	return &mysql.MySQLError{Number: n, SQLState: [5]byte{'2', '3', '0', '0', '0'}, Message: msg}
}

// TestConstraintErrors is half of Review Focus 5.
func TestConstraintErrors(t *testing.T) {
	for _, c := range []struct {
		err  error
		kind ConstraintKind
		name string
	}{
		{&pgconn.PgError{Code: "23505", ConstraintName: "p_code_uq"}, Unique, "p_code_uq"},
		{&pgconn.PgError{Code: "23503", ConstraintName: "c_p_fk"}, ForeignKey, "c_p_fk"},
		{&pgconn.PgError{Code: "23502", ColumnName: "code"}, NotNull, "code"},
		{&pgconn.PgError{Code: "23514", ConstraintName: "p_n_ck"}, Check, "p_n_ck"},
		{myErr(1062, "Duplicate entry 'a' for key 'p.p_code_uq'"), Unique, "p_code_uq"},
		{myErr(1452, "Cannot add or update a child row: a foreign key constraint fails (`velox`.`c`, CONSTRAINT `c_p_fk` FOREIGN KEY (`p_id`) REFERENCES `p` (`id`))"), ForeignKey, "c_p_fk"},
		{myErr(1451, "Cannot delete or update a parent row: a foreign key constraint fails (`velox`.`c`, CONSTRAINT `c_p_fk` FOREIGN KEY (`p_id`) REFERENCES `p` (`id`))"), ForeignKey, "c_p_fk"},
		{myErr(1048, "Column 'code' cannot be null"), NotNull, "code"},
		{myErr(3819, "Check constraint 'p_n_ck' is violated."), Check, "p_n_ck"},
		{fmt.Errorf("velox: commit: %w", &pgconn.PgError{Code: "23505", ConstraintName: "late_uq"}), Unique, "late_uq"},
		// the duplicate value is row data and cannot choose the constraint's name
		{myErr(1062, "Duplicate entry 'x' for key 'users.admin_uq' for key 'users.email_uq'"), Unique, "email_uq"},
		{myErr(1062, "Duplicate entry '1' for key 'inv.PRIMARY'"), Unique, "PRIMARY"},
		{myErr(1062, "Duplicate entry 'a' for key 'name_uq'"), Unique, "name_uq"},
		{fmt.Errorf("x: %w", myErr(1062, "Duplicate entry 'a' for key 'p.p_code_uq'")), Unique, "p_code_uq"},
		{errors.Join(io.EOF, myErr(1062, "Duplicate entry 'a' for key 'p.p_code_uq'")), Unique, "p_code_uq"},
	} {
		got := dbErr(invInfo, c.err)
		var ce *ConstraintError
		if !errors.As(got, &ce) || ce.Kind != c.kind || ce.Constraint != c.name || ce.Entity != "velox.inv" {
			t.Errorf("%v: got %#v", c.err, got)
			continue
		}
		if !errors.Is(got, c.err) {
			t.Errorf("%v: the driver's error is not reachable", c.err)
		}
	}
	ce := &ConstraintError{Entity: "billing.Invoice", Kind: Unique, Constraint: "p_code_uq", Err: io.EOF}
	if want := "velox: billing.Invoice: unique constraint p_code_uq violated: EOF"; ce.Error() != want {
		t.Errorf("%q", ce.Error())
	}
}

// TestOtherErrorsAreLeftAlone is the other half of Review Focus 5.
func TestOtherErrorsAreLeftAlone(t *testing.T) {
	for _, err := range []error{io.EOF, &pgconn.PgError{Code: "42601"}, myErr(1064, "You have an error in your SQL syntax")} {
		got := dbErr(invInfo, err)
		var ce *ConstraintError
		if errors.As(got, &ce) || !errors.Is(got, err) || got.Error() != "velox: velox.inv: "+err.Error() {
			t.Errorf("%v became %v", err, got)
		}
	}
}

func TestRetryable(t *testing.T) {
	for err, want := range map[error]bool{
		errors.Join(io.EOF, myErr(1213, "Deadlock found")):              true,
		&pgconn.PgError{Code: "40001"}:                                  true,
		&pgconn.PgError{Code: "40P01"}:                                  true,
		&pgconn.PgError{Code: "23505"}:                                  false,
		myErr(1213, "Deadlock found when trying to get lock"):           true,
		myErr(1205, "Lock wait timeout exceeded"):                       true,
		myErr(1062, "Duplicate entry"):                                  false,
		fmt.Errorf("velox: commit: %w", &pgconn.PgError{Code: "40001"}): true,
		io.EOF: false,
	} {
		if got := retryable(err); got != want {
			t.Errorf("retryable(%v) = %v, want %v", err, got, want)
		}
	}
}

// coder is not a driver's type, though it has a Code method.
type coder struct{ code int }

func (coder) Error() string { return "c" }
func (c coder) Code() int   { return c.code }

func TestLookAlikeErrorsAreLeftAlone(t *testing.T) {
	for _, c := range []int{2067, 5} {
		err := coder{c}
		var ce *ConstraintError
		if errors.As(dbErr(invInfo, err), &ce) || retryable(err) {
			t.Errorf("code %d was taken for a driver error", c)
		}
	}
}

func TestSQLiteKind(t *testing.T) {
	for _, c := range []struct {
		code int
		msg  string
		kind ConstraintKind
		name string
	}{
		{787, "constraint failed: FOREIGN KEY constraint failed (787)", ForeignKey, ""},
		{2067, "constraint failed: UNIQUE constraint failed: p.code (2067)", Unique, "p.code"},
		{1299, "constraint failed: NOT NULL constraint failed: p.code (1299)", NotNull, "p.code"},
	} {
		if kind, name := sqliteKind(c.code, c.msg); kind != c.kind || name != c.name {
			t.Errorf("sqliteKind(%d, %q) = %v, %q", c.code, c.msg, kind, name)
		}
	}
	if got := (&ConstraintError{Entity: "e", Kind: ForeignKey, Err: io.EOF}).Error(); got != "velox: e: foreign key constraint (unnamed) violated: EOF" {
		t.Error(got)
	}
}

func TestConstraintErrorIsNotWrappedTwice(t *testing.T) {
	once := dbErr(invInfo, &pgconn.PgError{Code: "23505", ConstraintName: "uq"})
	if twice := dbErr(invInfo, once); twice != once {
		t.Errorf("%v became %v", once, twice)
	}
	wrapped := fmt.Errorf("velox: commit: %w", once)
	if got := dbErr(invInfo, wrapped); got != wrapped {
		t.Errorf("%v became %v", wrapped, got)
	}
}
