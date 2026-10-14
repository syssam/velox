package velox

import (
	"errors"
	"fmt"
	"reflect"
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
	var pg *pgconn.PgError
	switch {
	case errors.As(err, &pg):
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

func pgKind(pg *pgconn.PgError) (ConstraintKind, string) {
	switch pg.Code {
	case "23505":
		return Unique, pg.ConstraintName
	case "23503":
		return ForeignKey, pg.ConstraintName
	case "23502":
		return NotNull, pg.ColumnName
	case "23514":
		return Check, pg.ConstraintName
	}
	return 0, ""
}

// driverError walks err's tree depth-first, through both Unwrap() error and
// Unwrap() []error, and returns the first error whose type is a pointer to the
// named type pkg.name. Matching the package path and not only the type's name
// keeps a look-alike type in another package from being taken for the driver's.
func driverError(err error, pkg, name string) error {
	if err == nil {
		return nil
	}
	if t := reflect.TypeOf(err); t.Kind() == reflect.Pointer && t.Elem().PkgPath() == pkg && t.Elem().Name() == name {
		return err
	}
	switch u := err.(type) {
	case interface{ Unwrap() error }:
		return driverError(u.Unwrap(), pkg, name)
	case interface{ Unwrap() []error }:
		for _, e := range u.Unwrap() {
			if d := driverError(e, pkg, name); d != nil {
				return d
			}
		}
	}
	return nil
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
		return Unique, name
	case 1451, 1452:
		return ForeignKey, between(msg, "CONSTRAINT `", "`")
	case 1048:
		return NotNull, between(msg, "Column '", "'")
	case 3819:
		return Check, between(msg, "Check constraint '", "'")
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
		return Unique, subject
	case 787: // SQLITE_CONSTRAINT_FOREIGNKEY
		return ForeignKey, subject
	case 1299: // SQLITE_CONSTRAINT_NOTNULL
		return NotNull, subject
	case 275: // SQLITE_CONSTRAINT_CHECK
		return Check, subject
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

// retryable reports a serialization failure or a deadlock, after which
// running the transaction again can succeed.
func retryable(err error) bool {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
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
