package sqlgraph

import (
	"errors"
	"reflect"
	"strings"
)

// IsConstraintError returns true if the error resulted from a database constraint violation.
func IsConstraintError(err error) bool {
	var target *ConstraintError
	return errors.As(err, &target) ||
		IsUniqueConstraintError(err) ||
		IsForeignKeyConstraintError(err) ||
		IsCheckConstraintError(err)
}

// sqlStateError is implemented by errors that expose a SQLSTATE code:
// *pq.Error (lib/pq) and *pgconn.PgError (pgx).
type sqlStateError interface {
	SQLState() string
}

// sqliteError is implemented by *sqlite.Error (modernc.org/sqlite), whose
// Code returns the extended result code (the driver enables extended codes
// on every connection).
//
// The method set alone is not specific enough: an application error that
// carries an HTTP status as Code() int matches it too, and errors.As stops at
// the first match, so such a wrapper around a SQLite error would hide the
// driver's code. sqliteCode therefore also requires the concrete type to be
// declared in the modernc.org/sqlite module.
type sqliteError interface {
	Code() int
}

// sqlitePkgPath is the import path of modernc.org/sqlite. The driver is not
// imported: that would link and register it in every program using velox.
const sqlitePkgPath = "modernc.org/sqlite"

// sqliteCode returns the extended result code of the first modernc.org/sqlite
// error in err's tree, walking Unwrap() error and Unwrap() []error the way
// errors.As does.
func sqliteCode(err error) (int, bool) {
	stack := []error{err}
	for len(stack) > 0 {
		e := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if e == nil {
			continue
		}
		if se, ok := e.(sqliteError); ok && isSQLiteDriverType(e) {
			return se.Code(), true
		}
		switch u := e.(type) {
		case interface{ Unwrap() error }:
			stack = append(stack, u.Unwrap())
		case interface{ Unwrap() []error }:
			// Push in reverse so the first branch is examined first.
			errs := u.Unwrap()
			for i := len(errs) - 1; i >= 0; i-- {
				stack = append(stack, errs[i])
			}
		}
	}
	return 0, false
}

// isSQLiteDriverType reports whether err's concrete type is declared in the
// modernc.org/sqlite module (the package or one of its sub-packages).
func isSQLiteDriverType(err error) bool {
	t := reflect.TypeOf(err)
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	p := t.PkgPath()
	return p == sqlitePkgPath || strings.HasPrefix(p, sqlitePkgPath+"/")
}

// go-sql-driver/mysql's *MySQLError exposes its error number only as a struct
// field, so it matches no interface. Importing the driver to errors.As on the
// concrete type would link and register it in every program that uses velox,
// so MySQL errors are classified by their stable "Error <number>" message
// prefix below.

// PostgreSQL SQLSTATE codes for constraint violations (Class 23).
const (
	pgUniqueViolation     = "23505"
	pgForeignKeyViolation = "23503"
	pgCheckViolation      = "23514"
)

// SQLite extended result codes for constraint violations
// (SQLITE_CONSTRAINT | n<<8).
const (
	sqliteConstraintCheck      = 275  // SQLITE_CONSTRAINT_CHECK
	sqliteConstraintForeignKey = 787  // SQLITE_CONSTRAINT_FOREIGNKEY
	sqliteConstraintPrimaryKey = 1555 // SQLITE_CONSTRAINT_PRIMARYKEY
	sqliteConstraintUnique     = 2067 // SQLITE_CONSTRAINT_UNIQUE
)

// IsUniqueConstraintError reports if the error resulted from a DB uniqueness constraint violation.
// e.g. duplicate value in unique index.
func IsUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}

	// Check for SQLSTATE code (PostgreSQL, pgx)
	if e, ok := asError[sqlStateError](err); ok {
		if e.SQLState() == pgUniqueViolation {
			return true
		}
	}

	// Check for SQLite extended result code (modernc.org/sqlite)
	if c, ok := sqliteCode(err); ok {
		if c == sqliteConstraintUnique || c == sqliteConstraintPrimaryKey {
			return true
		}
	}

	// Fallback to string matching for drivers that don't implement interfaces
	return containsAny(err.Error(),
		"Error 1062",                 // MySQL (string fallback)
		"violates unique constraint", // Postgres (string fallback)
		"UNIQUE constraint failed",   // SQLite
	)
}

// IsForeignKeyConstraintError reports if the error resulted from a database foreign-key constraint violation.
// e.g. parent row does not exist.
func IsForeignKeyConstraintError(err error) bool {
	if err == nil {
		return false
	}

	// Check for SQLSTATE code (PostgreSQL, pgx)
	if e, ok := asError[sqlStateError](err); ok {
		if e.SQLState() == pgForeignKeyViolation {
			return true
		}
	}

	// Check for SQLite extended result code (modernc.org/sqlite)
	if c, ok := sqliteCode(err); ok && c == sqliteConstraintForeignKey {
		return true
	}

	// Fallback to string matching for drivers that don't implement interfaces
	return containsAny(err.Error(),
		"Error 1451",                      // MySQL (Cannot delete or update a parent row)
		"Error 1452",                      // MySQL (Cannot add or update a child row)
		"violates foreign key constraint", // Postgres
		"FOREIGN KEY constraint failed",   // SQLite
	)
}

// IsCheckConstraintError reports if the error resulted from a database check constraint violation.
// e.g. a value does not satisfy a check condition.
func IsCheckConstraintError(err error) bool {
	if err == nil {
		return false
	}

	// Check for SQLSTATE code (PostgreSQL, pgx)
	if e, ok := asError[sqlStateError](err); ok {
		if e.SQLState() == pgCheckViolation {
			return true
		}
	}

	// Check for SQLite extended result code (modernc.org/sqlite)
	if c, ok := sqliteCode(err); ok && c == sqliteConstraintCheck {
		return true
	}

	// Fallback to string matching for drivers that don't implement interfaces
	return containsAny(err.Error(),
		"Error 3819",                // MySQL
		"violates check constraint", // Postgres
		"CHECK constraint failed",   // SQLite
	)
}

// asError attempts to extract an error implementing interface T from the error chain.
// Uses errors.As to correctly handle both single-error and multi-error (errors.Join) chains.
func asError[T any](err error) (T, bool) {
	var target T
	if errors.As(err, &target) {
		return target, true
	}
	return target, false
}

// containsAny returns true if s contains any of the substrings.
func containsAny(s string, substrings ...string) bool {
	for _, sub := range substrings {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
