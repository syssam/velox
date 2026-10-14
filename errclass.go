package velox

import (
	"errors"
	"fmt"
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

// mysqlError reads a go-sql-driver/mysql error, whose text is
// "Error 1062 (23000): Duplicate entry 'a' for key 'p.p_code_uq'".
func mysqlError(err error) (int, string, bool) {
	for e := err; e != nil; e = errors.Unwrap(e) {
		if fmt.Sprintf("%T", e) != "*mysql.MySQLError" {
			continue
		}
		msg := e.Error()
		var n int
		if _, scanErr := fmt.Sscanf(msg, "Error %d", &n); scanErr == nil {
			return n, msg, true
		}
	}
	return 0, "", false
}

func mysqlKind(n int, msg string) (ConstraintKind, string) {
	switch n {
	case 1062:
		name := between(msg, "for key '", "'")
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
	var c interface{ Code() int }
	if errors.As(err, &c) && fmt.Sprintf("%T", c) == "*sqlite.Error" {
		return c.Code(), err.Error(), true
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
