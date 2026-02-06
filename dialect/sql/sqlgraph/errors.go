package sqlgraph

import (
	"errors"
	"strings"
)

// IsConstraintError returns true if the error resulted from a database constraint violation.
func IsConstraintError(err error) bool {
	var e *ConstraintError
	return errors.As(err, &e) ||
		IsUniqueConstraintError(err) ||
		IsForeignKeyConstraintError(err) ||
		IsCheckConstraintError(err)
}

// errorCoder is an interface for database errors that provide error codes.
// Implemented by: pq.Error, pgx, mysql.MySQLError, modernc.org/sqlite, etc.
type errorCoder interface {
	Code() string
}

// errorNumberer is an interface for database errors that provide numeric error codes.
// Implemented by: mysql.MySQLError (Number field via method).
type errorNumberer interface {
	Number() uint16
}

// sqlStateError is an interface for errors that provide SQLSTATE codes.
// Implemented by: pq.Error, pgx, and some MySQL drivers.
type sqlStateError interface {
	SQLState() string
}

// PostgreSQL SQLSTATE codes for constraint violations (Class 23).
const (
	pgUniqueViolation     = "23505"
	pgForeignKeyViolation = "23503"
	pgCheckViolation      = "23514"
)

// MySQL error numbers for constraint violations.
const (
	mysqlDuplicateEntry         = 1062
	mysqlForeignKeyParent       = 1451 // Cannot delete or update a parent row
	mysqlForeignKeyChild        = 1452 // Cannot add or update a child row
	mysqlCheckConstraintViolate = 3819
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

	// Check for PostgreSQL pq.Error code
	if e, ok := asError[errorCoder](err); ok {
		if e.Code() == pgUniqueViolation {
			return true
		}
	}

	// Check for MySQL error number
	if e, ok := asError[errorNumberer](err); ok {
		if e.Number() == mysqlDuplicateEntry {
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

	// Check for PostgreSQL pq.Error code
	if e, ok := asError[errorCoder](err); ok {
		if e.Code() == pgForeignKeyViolation {
			return true
		}
	}

	// Check for MySQL error number
	if e, ok := asError[errorNumberer](err); ok {
		num := e.Number()
		if num == mysqlForeignKeyParent || num == mysqlForeignKeyChild {
			return true
		}
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

	// Check for PostgreSQL pq.Error code
	if e, ok := asError[errorCoder](err); ok {
		if e.Code() == pgCheckViolation {
			return true
		}
	}

	// Check for MySQL error number
	if e, ok := asError[errorNumberer](err); ok {
		if e.Number() == mysqlCheckConstraintViolate {
			return true
		}
	}

	// Fallback to string matching for drivers that don't implement interfaces
	return containsAny(err.Error(),
		"Error 3819",                // MySQL
		"violates check constraint", // Postgres
		"CHECK constraint failed",   // SQLite
	)
}

// asError attempts to extract an error implementing interface T from the error chain.
func asError[T any](err error) (T, bool) {
	var target T
	for err != nil {
		if e, ok := err.(T); ok {
			return e, true
		}
		err = errors.Unwrap(err)
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
