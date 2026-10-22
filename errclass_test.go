package velox

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	_ "modernc.org/sqlite"
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
		{&pgconn.PgError{Code: "23505", ConstraintName: "p_code_uq"}, ConstraintUnique, "p_code_uq"},
		{&pgconn.PgError{Code: "23503", ConstraintName: "c_p_fk"}, ConstraintForeignKey, "c_p_fk"},
		{&pgconn.PgError{Code: "23502", ColumnName: "code"}, ConstraintNotNull, "code"},
		{&pgconn.PgError{Code: "23514", ConstraintName: "p_n_ck"}, ConstraintCheck, "p_n_ck"},
		{myErr(1062, "Duplicate entry 'a' for key 'p.p_code_uq'"), ConstraintUnique, "p_code_uq"},
		{myErr(1452, "Cannot add or update a child row: a foreign key constraint fails (`velox`.`c`, CONSTRAINT `c_p_fk` FOREIGN KEY (`p_id`) REFERENCES `p` (`id`))"), ConstraintForeignKey, "c_p_fk"},
		{myErr(1451, "Cannot delete or update a parent row: a foreign key constraint fails (`velox`.`c`, CONSTRAINT `c_p_fk` FOREIGN KEY (`p_id`) REFERENCES `p` (`id`))"), ConstraintForeignKey, "c_p_fk"},
		{myErr(1048, "Column 'code' cannot be null"), ConstraintNotNull, "code"},
		{myErr(3819, "Check constraint 'p_n_ck' is violated."), ConstraintCheck, "p_n_ck"},
		{fmt.Errorf("velox: commit: %w", &pgconn.PgError{Code: "23505", ConstraintName: "late_uq"}), ConstraintUnique, "late_uq"},
		// the duplicate value is row data and cannot choose the constraint's name
		{myErr(1062, "Duplicate entry 'x' for key 'users.admin_uq' for key 'users.email_uq'"), ConstraintUnique, "email_uq"},
		{myErr(1062, "Duplicate entry '1' for key 'inv.PRIMARY'"), ConstraintUnique, "PRIMARY"},
		{myErr(1062, "Duplicate entry 'a' for key 'name_uq'"), ConstraintUnique, "name_uq"},
		{fmt.Errorf("x: %w", myErr(1062, "Duplicate entry 'a' for key 'p.p_code_uq'")), ConstraintUnique, "p_code_uq"},
		{errors.Join(io.EOF, myErr(1062, "Duplicate entry 'a' for key 'p.p_code_uq'")), ConstraintUnique, "p_code_uq"},
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
	ce := &ConstraintError{Entity: "billing.Invoice", Kind: ConstraintUnique, Constraint: "p_code_uq", Err: io.EOF}
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
		{787, "constraint failed: FOREIGN KEY constraint failed (787)", ConstraintForeignKey, ""},
		{2067, "constraint failed: UNIQUE constraint failed: p.code (2067)", ConstraintUnique, "p.code"},
		{1299, "constraint failed: NOT NULL constraint failed: p.code (1299)", ConstraintNotNull, "p.code"},
	} {
		if kind, name := sqliteKind(c.code, c.msg); kind != c.kind || name != c.name {
			t.Errorf("sqliteKind(%d, %q) = %v, %q", c.code, c.msg, kind, name)
		}
	}
	if got := (&ConstraintError{Entity: "e", Kind: ConstraintForeignKey, Err: io.EOF}).Error(); got != "velox: e: foreign key constraint (unnamed) violated: EOF" {
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

// sqliteUnique returns modernc.org/sqlite's error for a UNIQUE violation on
// an in-memory database.
func sqliteUnique(t *testing.T) error {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1) // one connection: one in-memory database
	for _, q := range []string{"CREATE TABLE p (code TEXT UNIQUE)", "INSERT INTO p VALUES ('a')"} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	_, err = db.Exec("INSERT INTO p VALUES ('a')")
	if err == nil {
		t.Fatal("the duplicate insert succeeded")
	}
	return err
}

// TestDriverCode: the database's code is found under velox's wrapping, a
// *ConstraintError and errors.Join, and not in a look-alike type.
func TestDriverCode(t *testing.T) {
	for _, c := range []struct {
		name string
		err  error
		want string
	}{
		{"PostgreSQL", &pgconn.PgError{Code: "40001"}, "40001"},
		{"MySQL", myErr(1062, "Duplicate entry 'a' for key 'p.p_code_uq'"), "1062"},
		{"SQLite", sqliteUnique(t), "2067"},
	} {
		for _, w := range []struct {
			name string
			err  error
		}{
			{"bare", c.err},
			{"commit", fmt.Errorf("velox: commit: %w", c.err)},
			{"constraint", &ConstraintError{Entity: "velox.inv", Kind: ConstraintUnique, Err: c.err}},
			{"joined", errors.Join(io.EOF, c.err)},
		} {
			if got, ok := DriverCode(w.err); got != c.want || !ok {
				t.Errorf("%s, %s: DriverCode = %q, %v; want %q, true", c.name, w.name, got, ok, c.want)
			}
		}
	}
	for _, err := range []error{io.EOF, ErrDenied, nil, coder{2067}, fmt.Errorf("x: %w", coder{5})} {
		if got, ok := DriverCode(err); got != "" || ok {
			t.Errorf("DriverCode(%v) = %q, %v; want none", err, got, ok)
		}
	}
}
