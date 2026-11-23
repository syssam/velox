// Package dberr reads the errors of the database drivers velox supports
// (pgx, github.com/go-sql-driver/mysql, modernc.org/sqlite and
// github.com/mattn/go-sqlite3) without importing them: a violated
// constraint, a lock that could not be taken, a retryable failure, a lost
// connection, and the database's own code. A
// driver's error is recognized by its methods, its type's name and package,
// or its text, which tests pin against the drivers.
package dberr

import (
	"fmt"
	"github.com/syssam/velox/veloxtype"
	"reflect"
	"strconv"
	"strings"
)

// PgConnClosed, PgTxClosed and PgTxCommitRollback are the text of pgx's
// ErrConnClosed, ErrTxClosed and ErrTxCommitRollback.
// TestPgxTypesMirrored pins them.
const (
	PgConnClosed       = "conn closed"
	PgTxClosed         = "tx is closed"
	PgTxCommitRollback = "commit unexpectedly resulted in rollback"
)

// Constraint reads a driver error as a violated constraint, or returns nil.
// velox does not import the drivers. A PostgreSQL error is one with a SQLState
// method, whose constraint and column come from its fields by name; MySQL's and
// SQLite's are recognized by type name and read from their text.
func Constraint(entity string, err error) *veloxtype.ConstraintError {
	var kind veloxtype.ConstraintKind
	var name string
	if pg, ok := PgErrorOf(err); ok {
		kind, name = pgKind(pg)
	} else {
		if n, msg, ok := MySQLError(err); ok {
			kind, name = mysqlKind(n, msg)
		} else if code, msg, ok := SQLiteError(err); ok {
			kind, name = sqliteKind(code, msg)
		}
	}
	if kind == 0 {
		return nil
	}
	return &veloxtype.ConstraintError{Entity: entity, Kind: kind, Constraint: name, Err: err}
}

// PgErr is what velox reads of a *pgconn.PgError.
type PgErr struct{ Code, ConstraintName, ColumnName string }

// PgErrorOf reads the first error in err's tree that is not a nil pointer and
// reports a SQLSTATE through a SQLState method, as *pgconn.PgError does in every
// pgx version. A driver's fields of other names read as empty.
func PgErrorOf(err error) (PgErr, bool) {
	var pg PgErr
	found := Walk(err, func(e error) bool {
		s, ok := e.(interface{ SQLState() string })
		if ok {
			pg = PgErr{s.SQLState(), stringField(e, "ConstraintName"), stringField(e, "ColumnName")}
		}
		return ok
	})
	return pg, found
}

// stringField is the string field name of the struct e points to, or "".
func stringField(e error, name string) string {
	v := reflect.Indirect(reflect.ValueOf(e))
	if v.Kind() != reflect.Struct {
		return ""
	}
	sf, ok := v.Type().FieldByName(name)
	if !ok {
		return ""
	}
	f, err := v.FieldByIndexErr(sf.Index) // fails through a nil embedded pointer
	if err != nil || f.Kind() != reflect.String {
		return ""
	}
	return f.String()
}

// PgConnectError reports a *pgconn.ConnectError of any pgx version in err's tree.
func PgConnectError(err error) bool {
	return Walk(err, func(e error) bool {
		t := reflect.TypeOf(e)
		if t.Kind() != reflect.Pointer || t.Elem().Name() != "ConnectError" {
			return false
		}
		path := t.Elem().PkgPath()
		return strings.HasPrefix(path, "github.com/jackc/pgx/") && strings.HasSuffix(path, "/pgconn")
	})
}

// Locked reports a lock that could not be taken without waiting: PostgreSQL's
// lock_not_available (55P03) or MySQL's 3572, NOWAIT's failure.
func Locked(err error) bool {
	if pg, ok := PgErrorOf(err); ok {
		return pg.Code == "55P03"
	}
	n, _, ok := MySQLError(err)
	return ok && n == 3572
}

func pgKind(pg PgErr) (veloxtype.ConstraintKind, string) {
	switch pg.Code {
	case "23505":
		return veloxtype.ConstraintUnique, pg.ConstraintName
	case "23503":
		return veloxtype.ConstraintForeignKey, pg.ConstraintName
	case "23502":
		return veloxtype.ConstraintNotNull, pg.ColumnName
	case "23514":
		return veloxtype.ConstraintCheck, pg.ConstraintName
	}
	return 0, ""
}

// Walk calls f on err and on each error it wraps, depth-first through both
// Unwrap() error and Unwrap() []error, in errors.As's order, until f returns
// true, and reports whether it did. A nil pointer is skipped and not
// unwrapped, and the walk goes on past it: it is no driver's error, and its
// methods (Error, Unwrap, Timeout) could panic on it.
func Walk(err error, f func(error) bool) bool {
	if err == nil {
		return false
	}
	if v := reflect.ValueOf(err); v.Kind() == reflect.Pointer && v.IsNil() {
		return false
	}
	if f(err) {
		return true
	}
	switch u := err.(type) { //nolint:errorlint // Walk is the unwrapping errors.Is does.
	case interface{ Unwrap() error }:
		return Walk(u.Unwrap(), f)
	case interface{ Unwrap() []error }:
		for _, e := range u.Unwrap() {
			if Walk(e, f) {
				return true
			}
		}
	}
	return false
}

// Is is errors.Is over walk: a nil pointer in err's tree is passed over. A
// nil target matches a nil err only, as with errors.Is.
func Is(err, target error) bool {
	if target == nil {
		return err == nil
	}
	canEq := reflect.TypeOf(target).Comparable()
	return Walk(err, func(e error) bool {
		if canEq && e == target { //nolint:errorlint // e is one error of the tree Walk unwraps.
			return true
		}
		x, ok := e.(interface{ Is(error) bool })
		return ok && x.Is(target)
	})
}

// DriverError returns the first error in err's tree (see walk) whose type is a
// pointer to the named type pkg.name. Matching the package path and not only
// the type's name keeps a look-alike type in another package from being taken
// for the driver's.
func DriverError(err error, pkg, name string) error {
	var found error
	Walk(err, func(e error) bool {
		if t := reflect.TypeOf(e); t.Kind() == reflect.Pointer && t.Elem().PkgPath() == pkg && t.Elem().Name() == name {
			found = e
			return true
		}
		return false
	})
	return found
}

// DriverSentinel reports whether err's tree holds a driver's errors.New with
// this text: the sentinel of a package velox does not import, recognized by its
// type and text.
func DriverSentinel(err error, text string) bool {
	return Walk(err, func(e error) bool {
		t := reflect.TypeOf(e)
		return t.Kind() == reflect.Pointer && t.Elem().PkgPath() == "errors" && t.Elem().Name() == "errorString" && e.Error() == text
	})
}

// MySQLError reads a go-sql-driver/mysql error, whose text is
// "Error 1062 (23000): Duplicate entry 'a' for key 'p.p_code_uq'".
func MySQLError(err error) (int, string, bool) {
	e := DriverError(err, "github.com/go-sql-driver/mysql", "MySQLError")
	if e == nil {
		return 0, "", false
	}
	msg := e.Error()
	var n int
	if _, scanErr := fmt.Sscanf(msg, "Error %d", &n); scanErr != nil {
		return 0, "", false
	}
	return n, msg, true
}

func mysqlKind(n int, msg string) (veloxtype.ConstraintKind, string) {
	switch n {
	case 1062:
		// The key is the message's suffix. The duplicate value comes before
		// it and can contain "for key '" itself, so read from the last one.
		name := ""
		if i := strings.LastIndex(msg, "for key '"); i >= 0 {
			name = between(msg[i:], "for key '", "'")
		}
		if i := strings.LastIndexByte(name, '.'); i >= 0 {
			name = name[i+1:] // MySQL 8 prefixes the table
		}
		return veloxtype.ConstraintUnique, name
	case 1451, 1452:
		return veloxtype.ConstraintForeignKey, between(msg, "CONSTRAINT `", "`")
	case 1048:
		return veloxtype.ConstraintNotNull, between(msg, "Column '", "'")
	case 3819:
		return veloxtype.ConstraintCheck, between(msg, "Check constraint '", "'")
	case 1364: // strict mode: "Field 'code' doesn't have a default value"
		return veloxtype.ConstraintNotNull, between(msg, "Field '", "'")
	case 4025:
		// MariaDB's ER_CONSTRAINT_FAILED, "CONSTRAINT `p_n_ck` failed for
		// `velox`.`p`". MySQL 8.4's 4025 is ER_INNODB_AUTOEXTEND_SIZE_OUT_OF_RANGE,
		// so only a message of MariaDB's form is a CHECK.
		if name := between(msg, "CONSTRAINT `", "` failed for "); name != "" {
			return veloxtype.ConstraintCheck, name
		}
	}
	return 0, ""
}

// SQLiteError reads a SQLite error of modernc.org/sqlite or
// github.com/mattn/go-sqlite3 as SQLite's extended result code and the
// error's text, which ends with the constraint's subject. modernc's
// *Error has a Code method and the text "constraint failed: UNIQUE
// constraint failed: p.code (2067)". mattn's Error is a struct, used by
// value, whose ExtendedCode field holds the code and whose text is "UNIQUE
// constraint failed: p.code".
func SQLiteError(err error) (int, string, bool) {
	e := DriverError(err, "modernc.org/sqlite", "Error")
	if c, ok := e.(interface{ Code() int }); ok {
		return c.Code(), e.Error(), true
	}
	return codeFields(err, "github.com/mattn/go-sqlite3", "Error")
}

// codeFields reads the first error in err's tree whose type is the struct
// pkg.name, or a pointer to it, with integer fields ExtendedCode and Code:
// it returns ExtendedCode, or Code when ExtendedCode is 0, and the text.
func codeFields(err error, pkg, name string) (int, string, bool) {
	var code int
	var msg string
	found := Walk(err, func(e error) bool {
		v := reflect.ValueOf(e)
		if v.Kind() == reflect.Pointer {
			if v.IsNil() {
				return false
			}
			v = v.Elem()
		}
		t := v.Type()
		if t.Kind() != reflect.Struct || t.PkgPath() != pkg || t.Name() != name {
			return false
		}
		ext, base := v.FieldByName("ExtendedCode"), v.FieldByName("Code")
		if !ext.IsValid() || !base.IsValid() || !ext.CanInt() || !base.CanInt() {
			return false
		}
		code = int(ext.Int())
		if code == 0 {
			code = int(base.Int())
		}
		msg = e.Error()
		return true
	})
	return code, msg, found
}

func sqliteKind(code int, msg string) (veloxtype.ConstraintKind, string) {
	subject := ""
	if _, after, ok := strings.CutLast(msg, "failed: "); ok {
		subject = after
		if j := strings.LastIndex(subject, " ("); j >= 0 {
			subject = subject[:j]
		}
	}
	if code == 787 {
		subject = "" // "FOREIGN KEY constraint failed" names nothing
	}
	switch code {
	case 2067, 1555: // SQLITE_CONSTRAINT_UNIQUE, SQLITE_CONSTRAINT_PRIMARYKEY
		return veloxtype.ConstraintUnique, subject
	case 787: // SQLITE_CONSTRAINT_FOREIGNKEY
		return veloxtype.ConstraintForeignKey, subject
	case 1299: // SQLITE_CONSTRAINT_NOTNULL
		if i := strings.LastIndexByte(subject, '.'); i >= 0 {
			subject = subject[i+1:] // the column, without its table
		}
		return veloxtype.ConstraintNotNull, subject
	case 275: // SQLITE_CONSTRAINT_CHECK
		return veloxtype.ConstraintCheck, subject
	}
	return 0, ""
}

// between returns the text of s after the first from and before the next to.
func between(s, from, to string) string {
	i := strings.Index(s, from)
	if i < 0 {
		return ""
	}
	s = s[i+len(from):]
	j := strings.Index(s, to)
	if j < 0 {
		return ""
	}
	return s[:j]
}

// DriverCode returns the database's code for err, as text, when err or an
// error it wraps came from the database: PostgreSQL's SQLSTATE ("23505"),
// MySQL's error number ("1062"), or SQLite's extended result code
// ("2067"). It looks through wrapping and errors.Join, as errors.As does.
// When an errors.Join holds database errors of more than one driver, which
// is unusual, PostgreSQL's wins, then MySQL's, then SQLite's, whatever their
// order in the join; within one driver the first in errors.As's order wins.
//
// The code is for logs, traces and metrics, and for telling one database
// error from another, such as a serialization failure ("40001") from a
// deadlock ("40P01"). It names the database's internals, so it is not for a
// message an end user sees. velox reads the errors of pgx (natively or
// through database/sql), github.com/go-sql-driver/mysql, modernc.org/sqlite
// and github.com/mattn/go-sqlite3; another driver's error has no code here.
func DriverCode(err error) (string, bool) {
	if pg, ok := PgErrorOf(err); ok {
		return pg.Code, pg.Code != ""
	}
	if n, _, ok := MySQLError(err); ok {
		return strconv.Itoa(n), true
	}
	if code, _, ok := SQLiteError(err); ok {
		return strconv.Itoa(code), true
	}
	return "", false
}

// Retryable reports a serialization failure or a deadlock, after which
// running the transaction again can succeed.
func Retryable(err error) bool {
	if pg, ok := PgErrorOf(err); ok {
		return pg.Code == "40001" || pg.Code == "40P01"
	}
	if n, _, ok := MySQLError(err); ok {
		return n == 1213 || n == 1205
	}
	if code, _, ok := SQLiteError(err); ok {
		return code&0xff == 5 || code&0xff == 6 // SQLITE_BUSY, SQLITE_LOCKED and their extended codes
	}
	return false
}

// RolledBack reports an error of COMMIT that says the transaction rolled
// back, so nothing of it committed. Any other error of COMMIT leaves the
// outcome unknown: the database may have committed and the answer been lost.
//
//   - PostgreSQL: pgx's ErrTxCommitRollback, natively and through
//     database/sql, the server's answer ROLLBACK to the COMMIT of an aborted
//     transaction; an integrity violation (class 23), a deferred
//     constraint's; a transaction rollback (class 40: 40000, 40001, 40002,
//     40P01), but not 40003, statement_completion_unknown.
//   - MySQL: a deadlock (1213), which rolls the whole transaction back, and
//     3101, a rollback the server decided during COMMIT (Group
//     Replication). A lock wait timeout (1205) rolls back only a statement
//     unless innodb_rollback_on_timeout, and 1180, an engine's error during
//     COMMIT, does not say what was kept: both are unknown.
//   - SQLite: SQLITE_BUSY and SQLITE_LOCKED, after which COMMIT has not
//     happened and both drivers roll back, and a constraint (a deferred
//     foreign key, 787). Other errors, such as an I/O error, are unknown.
//
// A violated constraint that Constraint reads is a rollback on every
// dialect.
func RolledBack(err error) bool {
	if DriverSentinel(err, PgTxCommitRollback) || Constraint("", err) != nil {
		return true
	}
	if pg, ok := PgErrorOf(err); ok {
		return strings.HasPrefix(pg.Code, "23") || pg.Code == "40000" || pg.Code == "40001" || pg.Code == "40002" || pg.Code == "40P01"
	}
	if n, _, ok := MySQLError(err); ok {
		return n == 1213 || n == 3101
	}
	if code, _, ok := SQLiteError(err); ok {
		return code&0xff == 5 || code&0xff == 6 || code&0xff == 19 // SQLITE_BUSY, SQLITE_LOCKED, SQLITE_CONSTRAINT
	}
	return false
}

// As is errors.As over walk: it returns the first error in err's tree of
// type T, or that an As method gives as T, which is not a nil pointer.
func As[T error](err error) (T, bool) {
	var found T
	ok := Walk(err, func(e error) bool {
		if t, ok := e.(T); ok { //nolint:errorlint // e is one error of the tree Walk unwraps.
			found = t
			return true
		}
		x, ok := e.(interface{ As(any) bool })
		if !ok {
			return false
		}
		var t T
		if !x.As(&t) {
			return false
		}
		if v := reflect.ValueOf(t); !v.IsValid() || v.Kind() == reflect.Pointer && v.IsNil() {
			return false
		}
		found = t
		return true
	})
	return found, ok
}
