package velox

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// constraint reads a driver error as a violated constraint, or returns nil.
// PostgreSQL's errors are read from pgx's fields. velox does not import the
// MySQL and SQLite drivers, so their errors are recognized by type name and
// read from their text.
func constraint(entity string, err error) *ConstraintError {
	var kind ConstraintKind
	var name string
	switch pg := pgError(err); {
	case pg != nil:
		kind, name = pgKind(pg)
	default:
		if n, msg, ok := mysqlError(err); ok {
			kind, name = mysqlKind(n, msg)
		} else if code, msg, ok := sqliteError(err); ok {
			kind, name = sqliteKind(code, msg)
		}
	}
	if kind == 0 {
		return nil
	}
	return &ConstraintError{Entity: entity, Kind: kind, Constraint: name, Err: err}
}

// pgError returns the first *pgconn.PgError in err's tree that is not a nil
// pointer, or nil.
func pgError(err error) *pgconn.PgError {
	pg, _ := as[*pgconn.PgError](err)
	return pg
}

// locked reports a lock that could not be taken without waiting: PostgreSQL's
// lock_not_available (55P03) or MySQL's 3572, NOWAIT's failure.
func locked(err error) bool {
	if pg := pgError(err); pg != nil {
		return pg.Code == "55P03"
	}
	n, _, ok := mysqlError(err)
	return ok && n == 3572
}

func pgKind(pg *pgconn.PgError) (ConstraintKind, string) {
	switch pg.Code {
	case "23505":
		return ConstraintUnique, pg.ConstraintName
	case "23503":
		return ConstraintForeignKey, pg.ConstraintName
	case "23502":
		return ConstraintNotNull, pg.ColumnName
	case "23514":
		return ConstraintCheck, pg.ConstraintName
	}
	return 0, ""
}

// walk calls f on err and on each error it wraps, depth-first through both
// Unwrap() error and Unwrap() []error, in errors.As's order, until f returns
// true, and reports whether it did. A nil pointer is skipped and not
// unwrapped, and the walk goes on past it: it is no driver's error, and its
// methods (Error, Unwrap, Timeout) could panic on it.
func walk(err error, f func(error) bool) bool {
	if err == nil {
		return false
	}
	if v := reflect.ValueOf(err); v.Kind() == reflect.Pointer && v.IsNil() {
		return false
	}
	if f(err) {
		return true
	}
	switch u := err.(type) {
	case interface{ Unwrap() error }:
		return walk(u.Unwrap(), f)
	case interface{ Unwrap() []error }:
		for _, e := range u.Unwrap() {
			if walk(e, f) {
				return true
			}
		}
	}
	return false
}

// is is errors.Is over walk: a nil pointer in err's tree is passed over.
func is(err, target error) bool {
	canEq := target != nil && reflect.TypeOf(target).Comparable()
	return walk(err, func(e error) bool {
		if canEq && e == target {
			return true
		}
		x, ok := e.(interface{ Is(error) bool })
		return ok && x.Is(target)
	})
}

// as is errors.As over walk: it returns the first error in err's tree of
// type T, or that an As method gives as T, which is not a nil pointer.
func as[T error](err error) (T, bool) {
	var found T
	ok := walk(err, func(e error) bool {
		if t, ok := e.(T); ok {
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

// driverError returns the first error in err's tree (see walk) whose type is a
// pointer to the named type pkg.name. Matching the package path and not only
// the type's name keeps a look-alike type in another package from being taken
// for the driver's.
func driverError(err error, pkg, name string) error {
	var found error
	walk(err, func(e error) bool {
		if t := reflect.TypeOf(e); t.Kind() == reflect.Pointer && t.Elem().PkgPath() == pkg && t.Elem().Name() == name {
			found = e
			return true
		}
		return false
	})
	return found
}

// mysqlError reads a go-sql-driver/mysql error, whose text is
// "Error 1062 (23000): Duplicate entry 'a' for key 'p.p_code_uq'".
func mysqlError(err error) (int, string, bool) {
	e := driverError(err, "github.com/go-sql-driver/mysql", "MySQLError")
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

func mysqlKind(n int, msg string) (ConstraintKind, string) {
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
		return ConstraintUnique, name
	case 1451, 1452:
		return ConstraintForeignKey, between(msg, "CONSTRAINT `", "`")
	case 1048:
		return ConstraintNotNull, between(msg, "Column '", "'")
	case 3819:
		return ConstraintCheck, between(msg, "Check constraint '", "'")
	case 1364: // strict mode: "Field 'code' doesn't have a default value"
		return ConstraintNotNull, between(msg, "Field '", "'")
	case 4025:
		// MariaDB's ER_CONSTRAINT_FAILED, "CONSTRAINT `p_n_ck` failed for
		// `velox`.`p`". MySQL 8.4's 4025 is ER_INNODB_AUTOEXTEND_SIZE_OUT_OF_RANGE,
		// so only a message of MariaDB's form is a CHECK.
		if name := between(msg, "CONSTRAINT `", "` failed for "); name != "" {
			return ConstraintCheck, name
		}
	}
	return 0, ""
}

// sqliteError reads a modernc.org/sqlite error, whose Code is SQLite's
// extended result code and whose text ends with the constraint's subject:
// "constraint failed: UNIQUE constraint failed: p.code (2067)".
func sqliteError(err error) (int, string, bool) {
	e := driverError(err, "modernc.org/sqlite", "Error")
	if c, ok := e.(interface{ Code() int }); ok {
		return c.Code(), e.Error(), true
	}
	return 0, "", false
}

func sqliteKind(code int, msg string) (ConstraintKind, string) {
	subject := ""
	if i := strings.LastIndex(msg, "failed: "); i >= 0 {
		subject = msg[i+len("failed: "):]
		if j := strings.LastIndex(subject, " ("); j >= 0 {
			subject = subject[:j]
		}
	}
	if code == 787 {
		subject = "" // "FOREIGN KEY constraint failed" names nothing
	}
	switch code {
	case 2067, 1555: // SQLITE_CONSTRAINT_UNIQUE, SQLITE_CONSTRAINT_PRIMARYKEY
		return ConstraintUnique, subject
	case 787: // SQLITE_CONSTRAINT_FOREIGNKEY
		return ConstraintForeignKey, subject
	case 1299: // SQLITE_CONSTRAINT_NOTNULL
		if i := strings.LastIndexByte(subject, '.'); i >= 0 {
			subject = subject[i+1:] // the column, without its table
		}
		return ConstraintNotNull, subject
	case 275: // SQLITE_CONSTRAINT_CHECK
		return ConstraintCheck, subject
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
// through database/sql), github.com/go-sql-driver/mysql and
// modernc.org/sqlite; another driver's error has no code here.
func DriverCode(err error) (string, bool) {
	if pg := pgError(err); pg != nil {
		return pg.Code, pg.Code != ""
	}
	if n, _, ok := mysqlError(err); ok {
		return strconv.Itoa(n), true
	}
	if code, _, ok := sqliteError(err); ok {
		return strconv.Itoa(code), true
	}
	return "", false
}

// retryable reports a serialization failure or a deadlock, after which
// running the transaction again can succeed.
func retryable(err error) bool {
	if pg := pgError(err); pg != nil {
		return pg.Code == "40001" || pg.Code == "40P01"
	}
	if n, _, ok := mysqlError(err); ok {
		return n == 1213 || n == 1205
	}
	if code, _, ok := sqliteError(err); ok {
		return code&0xff == 5 || code&0xff == 6 // SQLITE_BUSY, SQLITE_LOCKED and their extended codes
	}
	return false
}
