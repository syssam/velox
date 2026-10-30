package engine

import (
	"database/sql"
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
	_ "modernc.org/sqlite"
)

func TestSQLiteKind(t *testing.T) {
	for _, c := range []struct {
		code int
		msg  string
		kind ConstraintKind
		name string
	}{
		{787, "constraint failed: FOREIGN KEY constraint failed (787)", ConstraintForeignKey, ""},
		{2067, "constraint failed: UNIQUE constraint failed: p.code (2067)", ConstraintUnique, "p.code"},
		{1299, "constraint failed: NOT NULL constraint failed: p.code (1299)", ConstraintNotNull, "code"},
	} {
		if kind, name := SqliteKind(c.code, c.msg); kind != c.kind || name != c.name {
			t.Errorf("sqliteKind(%d, %q) = %v, %q", c.code, c.msg, kind, name)
		}
	}
	if got := (&ConstraintError{Entity: "e", Kind: ConstraintForeignKey, Err: io.EOF}).Error(); got != "velox: e: foreign key constraint (unnamed) violated: EOF" {
		t.Error(got)
	}
}

// TestIsNilTarget: is with a nil target matches as errors.Is does, a nil err
// only.
func TestIsNilTarget(t *testing.T) {
	for _, err := range []error{nil, errors.New("x"), (*pgconn.PgError)(nil), fmt.Errorf("x: %w", io.EOF)} {
		if got, want := Is(err, nil), errors.Is(err, nil); got != want {
			t.Errorf("is(%#v, nil) = %v, errors.Is says %v", err, got, want)
		}
	}
}

// TestPgxTypesMirrored pins what the core knows of pgx without importing it, so
// that a pgx upgrade that changes one of them fails here: a sentinel's text, the
// fields read from a PgError, and the type names.
func TestPgxTypesMirrored(t *testing.T) {
	if got := pgconn.ErrConnClosed.Error(); got != PgConnClosed {
		t.Errorf("pgconn.ErrConnClosed is %q, pgConnClosed %q", got, PgConnClosed)
	}
	if got := pgx.ErrTxClosed.Error(); got != pgTxClosed {
		t.Errorf("pgx.ErrTxClosed is %q, pgTxClosed %q", got, pgTxClosed)
	}
	if !DriverSentinel(fmt.Errorf("x: %w", pgconn.ErrConnClosed), PgConnClosed) || !Ended(fmt.Errorf("x: %w", pgx.ErrTxClosed)) {
		t.Error("pgx's sentinels are not recognized through a wrap")
	}
	if !PgConnectError(fmt.Errorf("x: %w", &pgconn.ConnectError{})) {
		t.Error("PgConnectError does not recognize *pgconn.ConnectError")
	}
	pg, ok := PgErrorOf(fmt.Errorf("x: %w", &pgconn.PgError{Code: "23502", ConstraintName: "c", ColumnName: "k"}))
	if want := (pgErr{Code: "23502", ConstraintName: "c", ColumnName: "k"}); !ok || pg != want {
		t.Errorf("pgError read %+v, %v; want %+v", pg, ok, want)
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

// otherPgError stands for the PgError of another pgx major version, or of another
// PostgreSQL driver: what it shares with pgx's is the SQLState method.
type otherPgError struct {
	ConstraintName string
	ColumnName     int // not a string: read as empty
}

func (*otherPgError) Error() string    { return "other" }
func (*otherPgError) SQLState() string { return "23505" }

func TestPgErrorOfReadsSQLState(t *testing.T) {
	err := fmt.Errorf("x: %w", &otherPgError{ConstraintName: "c", ColumnName: 1})
	pg, ok := PgErrorOf(err)
	if !ok || pg != (pgErr{Code: "23505", ConstraintName: "c"}) {
		t.Errorf("PgErrorOf = %+v, %v", pg, ok)
	}
	if ce := Constraint("e", err); ce == nil || ce.Kind != ConstraintUnique || ce.Constraint != "c" {
		t.Errorf("Constraint = %+v", ce)
	}
	var nilPg *otherPgError
	if _, ok := PgErrorOf(fmt.Errorf("x: %w", nilPg)); ok {
		t.Error("PgErrorOf read a nil pointer")
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
		got := DBErr(invInfo, c.err)
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
	for _, err := range []error{io.EOF, &pgconn.PgError{Code: "42601"}, myErr(1064, "You have an error in your SQL syntax"),
		// MySQL's 4025, not MariaDB's
		myErr(4025, "The AUTOEXTEND_SIZE value is out of range")} {
		got := DBErr(invInfo, err)
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
		if got := Retryable(err); got != want {
			t.Errorf("retryable(%v) = %v, want %v", err, got, want)
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
		var ce *ConstraintError
		if errors.As(DBErr(invInfo, err), &ce) || Retryable(err) {
			t.Errorf("code %d was taken for a driver error", c)
		}
	}
}

// PgError has the shape of pgconn's error and the name, but is not the
// driver's type: DriverError checks the package path.
type PgError struct{ Code string }

func (*PgError) Error() string { return "look-alike" }

func TestLookAlikeDriverTypeFromAnotherPackage(t *testing.T) {
	err := &PgError{Code: "23505"}
	var ce *ConstraintError
	got := DBErr(invInfo, err)
	if errors.As(got, &ce) || Retryable(err) {
		t.Errorf("a look-alike PgError was taken for the driver's: %v", got)
	}
	if code, ok := DriverCode(err); ok || code != "" {
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
			got := DBErr(invInfo, c.err)
			if _, ok := As[*ConstraintError](got); ok || Is(got, ErrLocked) {
				t.Errorf("dbErr = %v", got)
			}
			if code, ok := DriverCode(c.err); code != "" || ok {
				t.Errorf("DriverCode = %q, %v; want none", code, ok)
			}
			if Retryable(c.err) || Failoverable(c.err) {
				t.Errorf("retryable %v, failoverable %v", Retryable(c.err), Failoverable(c.err))
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
		code, _ := DriverCode(c.err)
		if code != c.code || Retryable(c.err) != c.retry || Failoverable(c.err) != c.failover {
			t.Errorf("%s: DriverCode %q, retryable %v, failoverable %v; want %q, %v, %v",
				c.name, code, Retryable(c.err), Failoverable(c.err), c.code, c.retry, c.failover)
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
		got := DBErr(invInfo, err)
		if !errors.Is(got, ErrLocked) {
			t.Errorf("%v: %v does not match ErrLocked", err, got)
		}
		var pg *pgconn.PgError
		var my *mysql.MySQLError
		if !errors.As(got, &pg) && !errors.As(got, &my) {
			t.Errorf("%v: the driver's error is not reachable", got)
		}
		if want := "velox: velox.inv: lock not available: "; !strings.HasPrefix(got.Error(), want) {
			t.Errorf("%q does not start %q", got.Error(), want)
		}
		if Retryable(got) || Failoverable(got) {
			t.Errorf("%v: retryable %v, failoverable %v; want neither", got, Retryable(got), Failoverable(got))
		}
	}
	for _, err := range []error{&pgconn.PgError{Code: "40001"}, myErr(1205, "Lock wait timeout exceeded")} {
		if got := DBErr(invInfo, err); errors.Is(got, ErrLocked) {
			t.Errorf("%v matches ErrLocked", got)
		}
	}
}

func TestConstraintErrorIsNotWrappedTwice(t *testing.T) {
	once := DBErr(invInfo, &pgconn.PgError{Code: "23505", ConstraintName: "uq"})
	if twice := DBErr(invInfo, once); twice != once {
		t.Errorf("%v became %v", once, twice)
	}
	wrapped := fmt.Errorf("velox: commit: %w", once)
	if got := DBErr(invInfo, wrapped); got != wrapped {
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

// FuzzConstraint: whatever MySQL or SQLite error number and text a driver
// reports, the classifiers are deterministic and name a constraint that the
// text contains (the empty name too); and classifying a MySQL error through
// dbErr never panics, keeps the driver's error reachable, and agrees with
// MysqlKind on the text the driver gives (the message after "Error N
// (state): ") - a constraint error exactly when MysqlKind names a kind, and
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
		mk, mn := MysqlKind(code, msg)
		if mk2, mn2 := MysqlKind(code, msg); mk2 != mk || mn2 != mn {
			t.Fatalf("mysqlKind(%d, %q) is not deterministic: %v %q, then %v %q", code, msg, mk, mn, mk2, mn2)
		}
		if !strings.Contains(msg, mn) {
			t.Fatalf("mysqlKind(%d, %q) names %q, which the message does not contain", code, msg, mn)
		}
		sk, sn := SqliteKind(code, msg)
		if sk2, sn2 := SqliteKind(code, msg); sk2 != sk || sn2 != sn {
			t.Fatalf("sqliteKind(%d, %q) is not deterministic: %v %q, then %v %q", code, msg, sk, sn, sk2, sn2)
		}
		if !strings.Contains(msg, sn) {
			t.Fatalf("sqliteKind(%d, %q) names %q, which the message does not contain", code, msg, sn)
		}

		err := myErr(uint16(code), msg)
		got := DBErr(invInfo, err)
		if !errors.Is(got, err) {
			t.Fatalf("code %d, %q: the driver's error is not reachable from %v", code, msg, got)
		}
		kind, name := MysqlKind(int(uint16(code)), err.Error())
		var ce *ConstraintError
		if errors.As(got, &ce) != (kind != 0) || ce != nil && (ce.Kind != kind || ce.Constraint != name || ce.Entity != "velox.inv") {
			t.Fatalf("code %d, %q: dbErr gave %#v, mysqlKind %v %q", code, msg, got, kind, name)
		}
		if errors.Is(got, ErrLocked) != (uint16(code) == 3572) {
			t.Fatalf("code %d, %q: ErrLocked = %v", code, msg, errors.Is(got, ErrLocked))
		}
	})
}
