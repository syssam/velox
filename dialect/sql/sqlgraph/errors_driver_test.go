package sqlgraph

import (
	"context"
	stdsql "database/sql"
	"errors"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

// opaqueError hides the wrapped driver error's message, so only structured
// detection (errors.As on the driver's error type) can classify it. A wrapper
// like this is what an application-level error type or a logging decorator
// looks like to the classifiers.
type opaqueError struct{ err error }

func (e *opaqueError) Error() string { return "operation failed" }
func (e *opaqueError) Unwrap() error { return e.err }

// sqliteConstraintErrors provokes real constraint violations on
// modernc.org/sqlite and returns the driver's errors.
func sqliteConstraintErrors(t *testing.T) (unique, primaryKey, foreignKey, check, other error) {
	t.Helper()
	db, err := stdsql.Open("sqlite", "file:sqlgraph_errors?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	for _, stmt := range []string{
		"CREATE TABLE parents (id INTEGER PRIMARY KEY, email TEXT UNIQUE, age INTEGER CHECK (age >= 0))",
		"CREATE TABLE children (id INTEGER PRIMARY KEY, parent_id INTEGER REFERENCES parents(id))",
		"INSERT INTO parents (id, email, age) VALUES (1, 'a@b.c', 1)",
	} {
		_, err := db.ExecContext(ctx, stmt)
		require.NoError(t, err)
	}
	exec := func(stmt string) error {
		_, err := db.ExecContext(ctx, stmt)
		require.Error(t, err, stmt)
		return err
	}
	return exec("INSERT INTO parents (id, email, age) VALUES (2, 'a@b.c', 1)"),
		exec("INSERT INTO parents (id, email, age) VALUES (1, 'x@y.z', 1)"),
		exec("INSERT INTO children (id, parent_id) VALUES (1, 42)"),
		exec("INSERT INTO parents (id, email, age) VALUES (3, 'q@r.s', -1)"),
		exec("INSERT INTO missing_table VALUES (1)")
}

// TestConstraintErrors_RealDriverTypes classifies error values of the driver
// types velox actually ships with, rather than mocks shaped after interfaces
// no driver implements.
func TestConstraintErrors_RealDriverTypes(t *testing.T) {
	unique, primaryKey, foreignKey, check, other := sqliteConstraintErrors(t)

	type want struct{ unique, fk, check bool }
	tests := []struct {
		name string
		err  error
		want want
	}{
		{"sqlite unique", &opaqueError{unique}, want{unique: true}},
		{"sqlite primary key", &opaqueError{primaryKey}, want{unique: true}},
		{"sqlite foreign key", &opaqueError{foreignKey}, want{fk: true}},
		{"sqlite check", &opaqueError{check}, want{check: true}},
		{"sqlite non-constraint", &opaqueError{other}, want{}},
		{"pq unique", &opaqueError{&pq.Error{Code: "23505"}}, want{unique: true}},
		{"pq foreign key", &opaqueError{&pq.Error{Code: "23503"}}, want{fk: true}},
		{"pq check", &opaqueError{&pq.Error{Code: "23514"}}, want{check: true}},
		// go-sql-driver/mysql exposes the number only as a struct field, and
		// importing the driver into sqlgraph would register it in every
		// program that uses velox, so MySQL is classified by its stable
		// "Error <number>" message prefix.
		{"mysql duplicate", &mysql.MySQLError{Number: 1062, SQLState: [5]byte{'2', '3', '0', '0', '0'}, Message: "Duplicate entry"}, want{unique: true}},
		{"mysql parent row", &mysql.MySQLError{Number: 1451, Message: "Cannot delete or update a parent row"}, want{fk: true}},
		{"mysql child row", &mysql.MySQLError{Number: 1452, Message: "Cannot add or update a child row"}, want{fk: true}},
		{"mysql check", &mysql.MySQLError{Number: 3819, Message: "Check constraint is violated"}, want{check: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want.unique, IsUniqueConstraintError(tt.err), "unique")
			assert.Equal(t, tt.want.fk, IsForeignKeyConstraintError(tt.err), "foreign key")
			assert.Equal(t, tt.want.check, IsCheckConstraintError(tt.err), "check")
			assert.Equal(t, tt.want != want{}, IsConstraintError(tt.err), "constraint")
		})
	}
}

// httpError is an application error that carries an HTTP status as Code() int
// — the same method set as modernc.org/sqlite's *Error — and hides the
// wrapped message.
type httpError struct {
	status int
	err    error
}

func (e *httpError) Error() string { return "request failed" }
func (e *httpError) Code() int     { return e.status }
func (e *httpError) Unwrap() error { return e.err }

// TestConstraintErrors_SQLiteBehindCodeWrapper pins that an application error
// with its own Code() int does not mask the SQLite error it wraps. Matching
// on the method set alone made errors.As stop at the wrapper, read 500 as the
// SQLite result code, and report "not a constraint error".
func TestConstraintErrors_SQLiteBehindCodeWrapper(t *testing.T) {
	unique, _, foreignKey, check, other := sqliteConstraintErrors(t)

	assert.True(t, IsUniqueConstraintError(&httpError{500, unique}), "unique")
	assert.True(t, IsForeignKeyConstraintError(&httpError{409, foreignKey}), "foreign key")
	assert.True(t, IsCheckConstraintError(&httpError{400, check}), "check")
	assert.False(t, IsConstraintError(&httpError{500, other}), "non-constraint")

	// Joined errors are walked like errors.As does.
	joined := errors.Join(&httpError{500, errors.New("unrelated")}, &httpError{500, unique})
	assert.True(t, IsUniqueConstraintError(joined), "unique in a joined tree")

	// An application error whose Code() happens to equal a SQLite constraint
	// code is not a SQLite error.
	assert.False(t, IsUniqueConstraintError(&httpError{2067, nil}), "foreign Code() must not match")
}
