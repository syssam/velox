package engine

import (
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/syssam/velox/internal/dberr"
	_ "modernc.org/sqlite"
)

// TestPgxTypesMirrored pins what the engine knows of pgx without importing
// it: that a closed transaction ends it, and the shape of pgx's rows' column
// names. dberr's own test pins the rest.
func TestPgxTypesMirrored(t *testing.T) {
	if !ended(fmt.Errorf("x: %w", pgx.ErrTxClosed)) {
		t.Error("pgx's ErrTxClosed is not recognized through a wrap")
	}
	if _, ok := reflect.TypeFor[pgx.Rows]().MethodByName("FieldDescriptions"); !ok {
		t.Error("pgx.Rows has no FieldDescriptions")
	}
	if f, ok := reflect.TypeFor[pgconn.FieldDescription]().FieldByName("Name"); !ok || f.Type.Kind() != reflect.String {
		t.Error("pgconn.FieldDescription has no string Name")
	}
}

// pgxRowsNames is a fake with pgx.Rows's way of naming columns.
type pgxRowsNames struct{ Rows }

func (pgxRowsNames) FieldDescriptions() []pgconn.FieldDescription {
	return []pgconn.FieldDescription{{Name: "id"}, {Name: "number"}}
}

// TestResultColumnsOfPgxRows: rows that name their columns as pgx's do are read
// without a pgx import in the core.
func TestResultColumnsOfPgxRows(t *testing.T) {
	got, err := resultColumns(pgxRowsNames{})
	if err != nil || !slices.Equal(got, []string{"id", "number"}) {
		t.Errorf("columns %v, error %v", got, err)
	}
}

// invInfo is the entity the errors name.
var invInfo = &EntityInfo{Name: "velox.inv"}

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
		{&pgconn.PgError{Code: "23505", ConstraintName: "p_code_uq"}, ConstraintUnique, "p_code_uq"},
		{&pgconn.PgError{Code: "23503", ConstraintName: "c_p_fk"}, ConstraintForeignKey, "c_p_fk"},
		{&pgconn.PgError{Code: "23502", ColumnName: "code"}, ConstraintNotNull, "code"},
		{&pgconn.PgError{Code: "23514", ConstraintName: "p_n_ck"}, ConstraintCheck, "p_n_ck"},
		{myErr(1062, "Duplicate entry 'a' for key 'p.p_code_uq'"), ConstraintUnique, "p_code_uq"},
		{myErr(1452, "Cannot add or update a child row: a foreign key constraint fails (`velox`.`c`, CONSTRAINT `c_p_fk` FOREIGN KEY (`p_id`) REFERENCES `p` (`id`))"), ConstraintForeignKey, "c_p_fk"},
		{myErr(1451, "Cannot delete or update a parent row: a foreign key constraint fails (`velox`.`c`, CONSTRAINT `c_p_fk` FOREIGN KEY (`p_id`) REFERENCES `p` (`id`))"), ConstraintForeignKey, "c_p_fk"},
		{myErr(1048, "Column 'code' cannot be null"), ConstraintNotNull, "code"},
		{myErr(3819, "Check constraint 'p_n_ck' is violated."), ConstraintCheck, "p_n_ck"},
		{myErr(1364, "Field 'code' doesn't have a default value"), ConstraintNotNull, "code"},
		{myErr(4025, "CONSTRAINT `p_n_ck` failed for `velox`.`p`"), ConstraintCheck, "p_n_ck"},
		{fmt.Errorf("velox: commit: %w", &pgconn.PgError{Code: "23505", ConstraintName: "late_uq"}), ConstraintUnique, "late_uq"},
		// the duplicate value is row data and cannot choose the constraint's name
		{myErr(1062, "Duplicate entry 'x' for key 'users.admin_uq' for key 'users.email_uq'"), ConstraintUnique, "email_uq"},
		{myErr(1062, "Duplicate entry '1' for key 'inv.PRIMARY'"), ConstraintUnique, "PRIMARY"},
		{myErr(1062, "Duplicate entry 'a' for key 'name_uq'"), ConstraintUnique, "name_uq"},
		{fmt.Errorf("x: %w", myErr(1062, "Duplicate entry 'a' for key 'p.p_code_uq'")), ConstraintUnique, "p_code_uq"},
		{errors.Join(io.EOF, myErr(1062, "Duplicate entry 'a' for key 'p.p_code_uq'")), ConstraintUnique, "p_code_uq"},
	} {
		got := dbErr(invInfo, c.err)
		if ce, ok := errors.AsType[*ConstraintError](got); !ok || ce.Kind != c.kind || ce.Constraint != c.name || ce.Entity != "velox.inv" {
			t.Errorf("%v: got %#v", c.err, got)
			continue
		}
		if !errors.Is(got, c.err) {
			t.Errorf("%v: the driver's error is not reachable", c.err)
		}
	}
	ce := &ConstraintError{Entity: "billing.Invoice", Kind: ConstraintUnique, Constraint: "p_code_uq", Err: io.EOF}
	if want := "velox: billing.Invoice: unique constraint p_code_uq violated: EOF"; ce.Error() != want {
		t.Errorf("%q", ce.Error())
	}
}

// TestOtherErrorsAreLeftAlone is the other half of Review Focus 5.
func TestOtherErrorsAreLeftAlone(t *testing.T) {
	for _, err := range []error{io.EOF, &pgconn.PgError{Code: "42601"}, myErr(1064, "You have an error in your SQL syntax"),
		// MySQL's 4025, not MariaDB's
		myErr(4025, "The AUTOEXTEND_SIZE value is out of range")} {
		got := dbErr(invInfo, err)
		if _, ok := errors.AsType[*ConstraintError](got); ok || !errors.Is(got, err) || got.Error() != "velox: velox.inv: "+err.Error() {
			t.Errorf("%v became %v", err, got)
		}
	}
}

// coder is not a driver's type, though it has a Code method.
type coder struct{ code int }

func (coder) Error() string { return "c" }

func (c coder) Code() int { return c.code }

func TestLookAlikeErrorsAreLeftAlone(t *testing.T) {
	for _, c := range []int{2067, 5} {
		err := coder{c}
		if _, ok := errors.AsType[*ConstraintError](dbErr(invInfo, err)); ok || dberr.Retryable(err) {
			t.Errorf("code %d was taken for a driver error", c)
		}
	}
}

// PgError has the shape of pgconn's error and the name, but is not the
// driver's type: driverError checks the package path.
type PgError struct{ Code string }

func (*PgError) Error() string { return "look-alike" }

func TestLookAlikeDriverTypeFromAnotherPackage(t *testing.T) {
	err := &PgError{Code: "23505"}
	got := dbErr(invInfo, err)
	if _, ok := errors.AsType[*ConstraintError](got); ok || dberr.Retryable(err) {
		t.Errorf("a look-alike PgError was taken for the driver's: %v", got)
	}
	if code, ok := dberr.DriverCode(err); ok || code != "" {
		t.Errorf("DriverCode = %q, %v", code, ok)
	}
}

// TestTypedNilDriverErrors: a nil pointer of a driver's or the network's
// error type, bare, wrapped or joined, is no driver error, and reading it
// does not panic. The test reads with is and as: errors.Is and errors.As
// would call Unwrap on a nil *net.OpError or *pgconn.ConnectError, and panic.
func TestTypedNilDriverErrors(t *testing.T) {
	// go-sql-driver/mysql's ErrInvalidConn is an *errors.errorString.
	nilErrorString := reflect.Zero(reflect.TypeOf(errors.New("invalid connection"))).Interface().(error)
	for _, c := range []struct {
		name string
		err  error
	}{
		{"pgx", (*pgconn.PgError)(nil)},
		{"mysql wrapped", fmt.Errorf("x: %w", (*mysql.MySQLError)(nil))},
		{"pgx joined", errors.Join(errors.New("x"), (*pgconn.PgError)(nil))},
		{"pgx connect", (*pgconn.ConnectError)(nil)},
		{"pgx connect wrapped", fmt.Errorf("x: %w", (*pgconn.ConnectError)(nil))},
		{"net", (*net.OpError)(nil)},
		{"net joined", errors.Join(errors.New("x"), (*net.OpError)(nil))},
		{"invalid conn", nilErrorString},
		{"invalid conn wrapped", fmt.Errorf("x: %w", nilErrorString)},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := dbErr(invInfo, c.err)
			if _, ok := dberr.As[*ConstraintError](got); ok || dberr.Is(got, ErrLocked) {
				t.Errorf("dbErr = %v", got)
			}
			if code, ok := dberr.DriverCode(c.err); code != "" || ok {
				t.Errorf("DriverCode = %q, %v; want none", code, ok)
			}
			if dberr.Retryable(c.err) || failoverable(c.err) {
				t.Errorf("retryable %v, failoverable %v", dberr.Retryable(c.err), failoverable(c.err))
			}
		})
	}
	// A nil pointer is passed over, and the walk goes on to the error after
	// it, for pgx as for the other drivers.
	for _, c := range []struct {
		name     string
		err      error
		code     string
		retry    bool
		failover bool
	}{
		{"pgx", errors.Join((*pgconn.PgError)(nil), &pgconn.PgError{Code: "40001"}), "40001", true, false},
		{"mysql", errors.Join((*mysql.MySQLError)(nil), myErr(1213, "Deadlock found")), "1213", true, false},
		{"net", errors.Join((*net.OpError)(nil), &net.OpError{Op: "dial", Err: errors.New("refused")}), "", false, true},
		{"connect", errors.Join((*pgconn.ConnectError)(nil), io.ErrUnexpectedEOF), "", false, true},
	} {
		code, _ := dberr.DriverCode(c.err)
		if code != c.code || dberr.Retryable(c.err) != c.retry || failoverable(c.err) != c.failover {
			t.Errorf("%s: DriverCode %q, retryable %v, failoverable %v; want %q, %v, %v",
				c.name, code, dberr.Retryable(c.err), failoverable(c.err), c.code, c.retry, c.failover)
		}
	}
}

// TestErrLocked: PostgreSQL's 55P03 and MySQL's 3572 match ErrLocked, with
// the driver's error still reachable; a serialization failure and a lock
// wait timeout do not.
func TestErrLocked(t *testing.T) {
	for _, err := range []error{
		&pgconn.PgError{Code: "55P03"},
		myErr(3572, "Statement aborted because lock(s) could not be acquired immediately and NOWAIT is set."),
	} {
		got := dbErr(invInfo, err)
		if !errors.Is(got, ErrLocked) {
			t.Errorf("%v: %v does not match ErrLocked", err, got)
		}
		_, isPg := errors.AsType[*pgconn.PgError](got)
		_, isMy := errors.AsType[*mysql.MySQLError](got)
		if !isPg && !isMy {
			t.Errorf("%v: the driver's error is not reachable", got)
		}
		if want := "velox: velox.inv: lock not available: "; !strings.HasPrefix(got.Error(), want) {
			t.Errorf("%q does not start %q", got.Error(), want)
		}
		if dberr.Retryable(got) || failoverable(got) {
			t.Errorf("%v: retryable %v, failoverable %v; want neither", got, dberr.Retryable(got), failoverable(got))
		}
	}
	for _, err := range []error{&pgconn.PgError{Code: "40001"}, myErr(1205, "Lock wait timeout exceeded")} {
		if got := dbErr(invInfo, err); errors.Is(got, ErrLocked) {
			t.Errorf("%v matches ErrLocked", got)
		}
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
