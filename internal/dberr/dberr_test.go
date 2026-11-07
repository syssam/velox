package dberr

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/syssam/velox/veloxtype"
	_ "modernc.org/sqlite"
)

func TestSQLiteKind(t *testing.T) {
	for _, c := range []struct {
		code int
		msg  string
		kind veloxtype.ConstraintKind
		name string
	}{
		{787, "constraint failed: FOREIGN KEY constraint failed (787)", veloxtype.ConstraintForeignKey, ""},
		{2067, "constraint failed: UNIQUE constraint failed: p.code (2067)", veloxtype.ConstraintUnique, "p.code"},
		{1299, "constraint failed: NOT NULL constraint failed: p.code (1299)", veloxtype.ConstraintNotNull, "code"},
	} {
		if kind, name := sqliteKind(c.code, c.msg); kind != c.kind || name != c.name {
			t.Errorf("sqliteKind(%d, %q) = %v, %q", c.code, c.msg, kind, name)
		}
	}
	if got := (&veloxtype.ConstraintError{Entity: "e", Kind: veloxtype.ConstraintForeignKey, Err: io.EOF}).Error(); got != "velox: e: foreign key constraint (unnamed) violated: EOF" {
		t.Error(got)
	}
}

// FuzzConstraint: whatever MySQL or SQLite error number and text a driver
// reports, the classifiers are deterministic and name a constraint that the
// text contains (the empty name too); and classifying a MySQL error through
// dbErr never panics, keeps the driver's error reachable, and agrees with
// mysqlKind on the text the driver gives (the message after "Error N
// (state): ") - a constraint error exactly when mysqlKind names a kind, and
// ErrLocked exactly for 3572.
func FuzzConstraint(f *testing.F) {
	for _, c := range []struct {
		code int
		msg  string
	}{
		{1062, "Duplicate entry 'a' for key 'p.p_code_uq'"},
		{1452, "Cannot add or update a child row: a foreign key constraint fails (`velox`.`c`, CONSTRAINT `c_p_fk` FOREIGN KEY (`p_id`) REFERENCES `p` (`id`))"},
		{1451, "Cannot delete or update a parent row: a foreign key constraint fails (`velox`.`c`, CONSTRAINT `c_p_fk` FOREIGN KEY (`p_id`) REFERENCES `p` (`id`))"},
		{1048, "Column 'code' cannot be null"},
		{3819, "Check constraint 'p_n_ck' is violated."},
		{1364, "Field 'code' doesn't have a default value"},
		{4025, "CONSTRAINT `p_n_ck` failed for `velox`.`p`"},
		{4025, "The AUTOEXTEND_SIZE value is out of range"},
		{1062, "Duplicate entry 'x' for key 'users.admin_uq' for key 'users.email_uq'"},
		{1062, "Duplicate entry '1' for key 'inv.PRIMARY'"},
		{1062, "Duplicate entry 'a' for key 'name_uq'"},
		{1064, "You have an error in your SQL syntax"},
		{1213, "Deadlock found when trying to get lock"},
		{3572, "Statement aborted because lock(s) could not be acquired immediately and NOWAIT is set."},
		{787, "constraint failed: FOREIGN KEY constraint failed (787)"},
		{2067, "constraint failed: UNIQUE constraint failed: p.code (2067)"},
		{1555, "constraint failed: PRIMARY KEY constraint failed: p.id (1555)"},
		{1299, "constraint failed: NOT NULL constraint failed: p.code (1299)"},
		{275, "constraint failed: CHECK constraint failed: p_n_ck (275)"},
		{0, ""},
		{-1, "for key '"},
		{1 << 20, "failed: ("},
	} {
		f.Add(c.code, c.msg)
	}
	f.Fuzz(func(t *testing.T, code int, msg string) {
		mk, mn := mysqlKind(code, msg)
		if mk2, mn2 := mysqlKind(code, msg); mk2 != mk || mn2 != mn {
			t.Fatalf("mysqlKind(%d, %q) is not deterministic: %v %q, then %v %q", code, msg, mk, mn, mk2, mn2)
		}
		if !strings.Contains(msg, mn) {
			t.Fatalf("mysqlKind(%d, %q) names %q, which the message does not contain", code, msg, mn)
		}
		sk, sn := sqliteKind(code, msg)
		if sk2, sn2 := sqliteKind(code, msg); sk2 != sk || sn2 != sn {
			t.Fatalf("sqliteKind(%d, %q) is not deterministic: %v %q, then %v %q", code, msg, sk, sn, sk2, sn2)
		}
		if !strings.Contains(msg, sn) {
			t.Fatalf("sqliteKind(%d, %q) names %q, which the message does not contain", code, msg, sn)
		}

		err := &mysql.MySQLError{Number: uint16(code), SQLState: [5]byte{'2', '3', '0', '0', '0'}, Message: msg}
		ce := Constraint("velox.inv", err)
		kind, name := mysqlKind(int(uint16(code)), err.Error())
		if (ce != nil) != (kind != 0) || ce != nil && (ce.Kind != kind || ce.Constraint != name || ce.Entity != "velox.inv" || !errors.Is(ce, err)) {
			t.Fatalf("code %d, %q: Constraint gave %#v, mysqlKind %v %q", code, msg, ce, kind, name)
		}
		if Locked(err) != (uint16(code) == 3572) {
			t.Fatalf("code %d, %q: Locked = %v", code, msg, Locked(err))
		}
	})
}

// TestPgxTypesMirrored pins what dberr knows of pgx without importing it, so
// that a pgx upgrade that changes one of them fails here: a sentinel's text
// and the fields read from a PgError.
func TestPgxTypesMirrored(t *testing.T) {
	if got := pgconn.ErrConnClosed.Error(); got != PgConnClosed {
		t.Errorf("pgconn.ErrConnClosed is %q, PgConnClosed %q", got, PgConnClosed)
	}
	if got := pgx.ErrTxClosed.Error(); got != PgTxClosed {
		t.Errorf("pgx.ErrTxClosed is %q, PgTxClosed %q", got, PgTxClosed)
	}
	if !DriverSentinel(fmt.Errorf("x: %w", pgconn.ErrConnClosed), PgConnClosed) || !DriverSentinel(fmt.Errorf("x: %w", pgx.ErrTxClosed), PgTxClosed) {
		t.Error("pgx's sentinels are not recognized through a wrap")
	}
	if !PgConnectError(fmt.Errorf("x: %w", &pgconn.ConnectError{})) {
		t.Error("PgConnectError does not recognize *pgconn.ConnectError")
	}
	pg, ok := PgErrorOf(fmt.Errorf("x: %w", &pgconn.PgError{Code: "23502", ConstraintName: "c", ColumnName: "k"}))
	if want := (PgErr{Code: "23502", ConstraintName: "c", ColumnName: "k"}); !ok || pg != want {
		t.Errorf("PgErrorOf read %+v, %v; want %+v", pg, ok, want)
	}
}

func myErr(n uint16, msg string) error {
	return &mysql.MySQLError{Number: n, SQLState: [5]byte{'2', '3', '0', '0', '0'}, Message: msg}
}

// coder is not a driver's type, though it has a Code method.
type coder struct{ code int }

func (coder) Error() string { return "c" }

func (c coder) Code() int { return c.code }

// TestIsNilTarget: is with a nil target matches as errors.Is does, a nil err
// only.
func TestIsNilTarget(t *testing.T) {
	for _, err := range []error{nil, errors.New("x"), (*pgconn.PgError)(nil), fmt.Errorf("x: %w", io.EOF)} {
		if got, want := Is(err, nil), errors.Is(err, nil); got != want {
			t.Errorf("Is(%#v, nil) = %v, errors.Is says %v", err, got, want)
		}
	}
}

// otherPgError stands for the PgError of another pgx major version, or of another
// PostgreSQL driver: what it shares with pgx's is the SQLState method.
type otherPgError struct {
	ConstraintName string
	ColumnName     int // not a string: read as empty
}

func (*otherPgError) Error() string { return "other" }

func (*otherPgError) SQLState() string { return "23505" }

func TestPgErrorOfReadsSQLState(t *testing.T) {
	err := fmt.Errorf("x: %w", &otherPgError{ConstraintName: "c", ColumnName: 1})
	pg, ok := PgErrorOf(err)
	if !ok || pg != (PgErr{Code: "23505", ConstraintName: "c"}) {
		t.Errorf("PgErrorOf = %+v, %v", pg, ok)
	}
	if ce := Constraint("e", err); ce == nil || ce.Kind != veloxtype.ConstraintUnique || ce.Constraint != "c" {
		t.Errorf("Constraint = %+v", ce)
	}
	var nilPg *otherPgError
	if _, ok := PgErrorOf(fmt.Errorf("x: %w", nilPg)); ok {
		t.Error("PgErrorOf read a nil pointer")
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
		if got := Retryable(err); got != want {
			t.Errorf("Retryable(%v) = %v, want %v", err, got, want)
		}
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
// *veloxtype.ConstraintError and errors.Join, and not in a look-alike type.
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
			{"constraint", &veloxtype.ConstraintError{Entity: "velox.inv", Kind: veloxtype.ConstraintUnique, Err: c.err}},
			{"joined", errors.Join(io.EOF, c.err)},
		} {
			if got, ok := DriverCode(w.err); got != c.want || !ok {
				t.Errorf("%s, %s: DriverCode = %q, %v; want %q, true", c.name, w.name, got, ok, c.want)
			}
		}
	}
	for _, err := range []error{io.EOF, io.ErrUnexpectedEOF, nil, coder{2067}, fmt.Errorf("x: %w", coder{5})} {
		if got, ok := DriverCode(err); got != "" || ok {
			t.Errorf("DriverCode(%v) = %q, %v; want none", err, got, ok)
		}
	}
}

type embeddedPgFields struct{ ConstraintName string }

// nilEmbeddedPgError reaches its fields through a nil embedded pointer.
type nilEmbeddedPgError struct{ *embeddedPgFields }

func (nilEmbeddedPgError) Error() string { return "embedded" }

func (nilEmbeddedPgError) SQLState() string { return "23505" }

func TestPgErrorOfNilEmbedded(t *testing.T) {
	pg, ok := PgErrorOf(nilEmbeddedPgError{})
	if !ok || pg != (PgErr{Code: "23505"}) {
		t.Errorf("PgErrorOf = %+v, %v", pg, ok)
	}
}
