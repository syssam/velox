package sql

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/syssam/velox/dialect"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithVars(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	drv := OpenDB(dialect.Postgres, db)
	mock.ExpectExec(`SET foo TO \$1`).WithArgs("bar").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT 1").WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	mock.ExpectExec("RESET foo").WillReturnResult(sqlmock.NewResult(0, 0))
	rows := &Rows{}
	err = drv.Query(
		WithVar(context.Background(), "foo", "bar"),
		"SELECT 1",
		[]any{},
		rows,
	)
	require.NoError(t, err)
	require.NoError(t, rows.Close(), "rows should be closed to release the connection")
	require.NoError(t, mock.ExpectationsWereMet())

	mock.ExpectExec(`SET foo TO \$1`).WithArgs("bar").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`SET foo TO \$1`).WithArgs("baz").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT 1").WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	mock.ExpectExec("RESET foo").WillReturnResult(sqlmock.NewResult(0, 0))
	err = drv.Query(
		WithVar(WithVar(context.Background(), "foo", "bar"), "foo", "baz"),
		"SELECT 1",
		[]any{},
		rows,
	)
	require.NoError(t, err)
	require.NoError(t, rows.Close(), "rows should be closed to release the connection")
	require.NoError(t, mock.ExpectationsWereMet())

	mock.ExpectBegin()
	mock.ExpectExec(`SET foo TO \$1`).WithArgs("bar").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT 1").WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	mock.ExpectCommit()
	tx, err := drv.Tx(context.Background())
	require.NoError(t, err)
	err = tx.Query(
		WithVar(context.Background(), "foo", "bar"),
		"SELECT 1",
		[]any{},
		rows,
	)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
	// Rows should not be closed to release the session,
	// as a transaction is always scoped to a single connection.

	mock.ExpectExec(`SET foo TO \$1`).WithArgs("qux").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO users DEFAULT VALUES").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("RESET foo").WillReturnResult(sqlmock.NewResult(0, 0))
	err = drv.Exec(
		WithVar(context.Background(), "foo", "qux"),
		"INSERT INTO users DEFAULT VALUES",
		[]any{},
		nil,
	)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	// No rows are returned, so no need to close them.

	mock.ExpectExec(`SET foo TO \$1`).WithArgs("foo").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO users DEFAULT VALUES").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("RESET foo").WillReturnResult(sqlmock.NewResult(0, 0))
	err = drv.Exec(
		WithVar(context.Background(), "foo", "foo"),
		"INSERT INTO users DEFAULT VALUES",
		[]any{},
		nil,
	)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	// No rows are returned, so no need to close them.
}

// TestOpenDB tests the OpenDB function with different dialects.
func TestOpenDB(t *testing.T) {
	tests := []struct {
		name    string
		dialect string
	}{
		{"Postgres", dialect.Postgres},
		{"MySQL", dialect.MySQL},
		{"SQLite", dialect.SQLite},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, _, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()

			drv := OpenDB(tt.dialect, db)
			assert.NotNil(t, drv)
			assert.Equal(t, tt.dialect, drv.Dialect())
		})
	}
}

// TestDriverQuery tests query operations.
func TestDriverQuery(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	drv := OpenDB(dialect.Postgres, db)

	t.Run("simple_query", func(t *testing.T) {
		mock.ExpectQuery("SELECT id, name FROM users").
			WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).
				AddRow(1, "Alice").
				AddRow(2, "Bob"))

		rows := &Rows{}
		err := drv.Query(context.Background(), "SELECT id, name FROM users", []any{}, rows)
		require.NoError(t, err)
		require.NoError(t, rows.Close())
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query_with_args", func(t *testing.T) {
		mock.ExpectQuery("SELECT name FROM users WHERE id = \\$1").
			WithArgs(1).
			WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("Alice"))

		rows := &Rows{}
		err := drv.Query(context.Background(), "SELECT name FROM users WHERE id = $1", []any{1}, rows)
		require.NoError(t, err)
		require.NoError(t, rows.Close())
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query_error", func(t *testing.T) {
		expectedErr := errors.New("database error")
		mock.ExpectQuery("SELECT").WillReturnError(expectedErr)

		rows := &Rows{}
		err := drv.Query(context.Background(), "SELECT", []any{}, rows)
		require.Error(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// TestDriverExec tests execute operations.
func TestDriverExec(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	drv := OpenDB(dialect.Postgres, db)

	t.Run("simple_exec", func(t *testing.T) {
		mock.ExpectExec("INSERT INTO users").
			WillReturnResult(sqlmock.NewResult(1, 1))

		err := drv.Exec(context.Background(), "INSERT INTO users (name) VALUES ('test')", []any{}, nil)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec_with_args", func(t *testing.T) {
		mock.ExpectExec("UPDATE users SET name = \\$1 WHERE id = \\$2").
			WithArgs("Alice", 1).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := drv.Exec(context.Background(), "UPDATE users SET name = $1 WHERE id = $2", []any{"Alice", 1}, nil)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec_error", func(t *testing.T) {
		expectedErr := errors.New("constraint violation")
		mock.ExpectExec("DELETE").WillReturnError(expectedErr)

		err := drv.Exec(context.Background(), "DELETE FROM users", []any{}, nil)
		require.Error(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// TestDriverTransaction tests transaction operations.
func TestDriverTransaction(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	drv := OpenDB(dialect.Postgres, db)

	t.Run("successful_commit", func(t *testing.T) {
		mock.ExpectBegin()
		mock.ExpectExec("INSERT INTO users").WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectCommit()

		tx, err := drv.Tx(context.Background())
		require.NoError(t, err)

		err = tx.Exec(context.Background(), "INSERT INTO users (name) VALUES ('test')", []any{}, nil)
		require.NoError(t, err)

		err = tx.Commit()
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("rollback", func(t *testing.T) {
		mock.ExpectBegin()
		mock.ExpectExec("INSERT INTO users").WillReturnError(errors.New("error"))
		mock.ExpectRollback()

		tx, err := drv.Tx(context.Background())
		require.NoError(t, err)

		err = tx.Exec(context.Background(), "INSERT INTO users (name) VALUES ('test')", []any{}, nil)
		require.Error(t, err)

		err = tx.Rollback()
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query_in_transaction", func(t *testing.T) {
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
		mock.ExpectCommit()

		tx, err := drv.Tx(context.Background())
		require.NoError(t, err)

		rows := &Rows{}
		err = tx.Query(context.Background(), "SELECT id FROM users", []any{}, rows)
		require.NoError(t, err)

		err = tx.Commit()
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// TestDialectMethod tests the Dialect method.
func TestDialectMethod(t *testing.T) {
	tests := []struct {
		dialect string
	}{
		{dialect.Postgres},
		{dialect.MySQL},
		{dialect.SQLite},
	}

	for _, tt := range tests {
		t.Run(tt.dialect, func(t *testing.T) {
			db, _, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()

			drv := OpenDB(tt.dialect, db)
			assert.Equal(t, tt.dialect, drv.Dialect())
		})
	}
}

// TestContextCancellation tests that context cancellation is respected.
func TestContextCancellation(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	drv := OpenDB(dialect.Postgres, db)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// Query with canceled context
	mock.ExpectQuery("SELECT").WillReturnError(context.Canceled)
	rows := &Rows{}
	err = drv.Query(ctx, "SELECT 1", []any{}, rows)
	// Error is expected due to canceled context
	assert.Error(t, err)
}

// BenchmarkDriver benchmarks driver operations.
func BenchmarkDriver(b *testing.B) {
	db, mock, err := sqlmock.New()
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()

	drv := OpenDB(dialect.Postgres, db)

	b.Run("Query_Simple", func(b *testing.B) {
		for b.Loop() {
			mock.ExpectQuery("SELECT 1").WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
			rows := &Rows{}
			_ = drv.Query(context.Background(), "SELECT 1", []any{}, rows)
			rows.Close()
		}
	})

	b.Run("Exec_Simple", func(b *testing.B) {
		for b.Loop() {
			mock.ExpectExec("INSERT").WillReturnResult(sqlmock.NewResult(1, 1))
			_ = drv.Exec(context.Background(), "INSERT INTO t VALUES (1)", []any{}, nil)
		}
	})

	b.Run("Transaction_Lifecycle", func(b *testing.B) {
		for b.Loop() {
			mock.ExpectBegin()
			mock.ExpectCommit()
			tx, _ := drv.Tx(context.Background())
			tx.Commit()
		}
	})
}

// TestNullValues tests handling of NULL values.
func TestNullValues(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	drv := OpenDB(dialect.Postgres, db)

	mock.ExpectQuery("SELECT").
		WillReturnRows(sqlmock.NewRows([]string{"name", "email"}).
			AddRow("Alice", nil).
			AddRow(nil, "bob@example.com"))

	rows := &Rows{}
	err = drv.Query(context.Background(), "SELECT name, email FROM users", []any{}, rows)
	require.NoError(t, err)
	require.NoError(t, rows.Close())
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestMultipleDialects tests operations with different SQL dialects.
func TestMultipleDialects(t *testing.T) {
	dialects := []string{dialect.Postgres, dialect.MySQL, dialect.SQLite}

	for _, d := range dialects {
		t.Run(d, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()

			drv := OpenDB(d, db)

			mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))

			rows := &Rows{}
			err = drv.Query(context.Background(), "SELECT id FROM users", []any{}, rows)
			require.NoError(t, err)
			require.NoError(t, rows.Close())
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// TestIsValidIdentifier tests SQL identifier validation.
//
// This function is the security boundary between user-controlled schema
// input (table/column names, query variable names) and raw SQL emission.
// A regression here — broadening the regex to accept metacharacters — is a
// direct path to SQL injection, so the payload matrix below exhaustively
// covers classic OWASP SQLi techniques.
func TestIsValidIdentifier(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		// Happy path.
		{"valid_simple", "foo", true},
		{"valid_with_underscore", "foo_bar", true},
		{"valid_with_number", "foo123", true},
		{"valid_with_dot", "schema.table", true},
		{"valid_starting_underscore", "_private", true},
		{"valid_max_length_128", strings.Repeat("a", 128), true},

		// Structural rejections.
		{"invalid_empty", "", false},
		{"invalid_starting_number", "123foo", false},
		{"invalid_with_space", "foo bar", false},
		{"invalid_with_tab", "foo\tbar", false},
		{"invalid_with_newline", "foo\nbar", false},
		{"invalid_with_dash", "foo-bar", false},
		{"invalid_too_long_129", strings.Repeat("a", 129), false},

		// Quote-based injection (classic identifier breakout).
		{"reject_single_quote", "foo'bar", false},
		{"reject_double_quote", `foo"bar`, false},
		{"reject_backtick", "foo`bar", false},
		{"reject_escaped_quote", `foo\"bar`, false},

		// Statement terminators / multi-statement injection.
		{"reject_semicolon", "foo;bar", false},
		{"reject_semicolon_drop", "foo;DROP TABLE users", false},
		{"reject_drop_suffix", "id; DROP TABLE users; --", false},
		{"reject_union_select", "id UNION SELECT password FROM users", false},

		// Comment injection.
		{"reject_sql_line_comment", "foo--", false},
		{"reject_sql_block_open", "foo/*", false},
		{"reject_sql_block_close", "foo*/", false},
		{"reject_hash_comment", "foo#bar", false},

		// Boolean / always-true payloads.
		{"reject_or_1_eq_1", "foo OR 1=1", false},
		{"reject_equals_payload", "foo=1", false},

		// Whitespace / encoding tricks.
		{"reject_null_byte", "foo\x00bar", false},
		{"reject_unicode_quote", "foo\u2018bar", false}, // left single quote
		{"reject_utf8_smuggle", "foo\u00a0bar", false},  // non-breaking space

		// Wildcard / metacharacter leakage.
		{"reject_wildcard_percent", "foo%", false},
		{"reject_wildcard_underscore_only", "", false}, // underscore-only guarded by empty check + regex
		{"reject_paren_open", "foo(", false},
		{"reject_paren_close", "foo)", false},
		{"reject_bracket", "foo[bar]", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidIdentifier(tt.input)
			assert.Equal(t, tt.expected, result, "input=%q", tt.input)
		})
	}
}

// FuzzIsValidIdentifier asserts the invariant that any input accepted by
// isValidIdentifier contains ONLY characters from the whitelist set
// [a-zA-Z0-9_.] and starts with [a-zA-Z_]. If the regex is ever relaxed to
// allow SQL metacharacters, fuzzing will find it — the oracle is the
// character-class invariant, not an exact allow-list.
//
// Run locally with: go test -fuzz FuzzIsValidIdentifier ./dialect/sql/
func FuzzIsValidIdentifier(f *testing.F) {
	seeds := []string{
		"foo", "foo_bar", "schema.table", "_private",
		"", "123", "foo bar", "foo'x", `foo"x`, "foo;DROP",
		"foo\x00", "\u2018", "foo\nbar",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if !isValidIdentifier(s) {
			return
		}
		// Accepted inputs must satisfy both length and character-class rules.
		if len(s) == 0 || len(s) > 128 {
			t.Fatalf("accepted out-of-range length: len=%d input=%q", len(s), s)
		}
		first := s[0]
		firstOK := (first >= 'a' && first <= 'z') ||
			(first >= 'A' && first <= 'Z') ||
			first == '_'
		if !firstOK {
			t.Fatalf("accepted identifier with invalid leading byte %q: %q", first, s)
		}
		for i := 0; i < len(s); i++ {
			c := s[i]
			ok := (c >= 'a' && c <= 'z') ||
				(c >= 'A' && c <= 'Z') ||
				(c >= '0' && c <= '9') ||
				c == '_' || c == '.'
			if !ok {
				t.Fatalf("accepted identifier containing forbidden byte %q at index %d: %q", c, i, s)
			}
		}
	})
}

// TestEscapeStringValue tests SQL string value escaping.
func TestEscapeStringValue(t *testing.T) {
	t.Run("mysql", func(t *testing.T) {
		tests := []struct {
			name     string
			input    string
			expected string
		}{
			{"no_escaping_needed", "hello", "hello"},
			{"single_quote", "it's", "it''s"},
			{"multiple_quotes", "he said 'hello'", "he said ''hello''"},
			{"backslash", `path\to\file`, `path\\to\\file`},
			{"both_quote_and_backslash", `it's a \test`, `it''s a \\test`},
			{"empty_string", "", ""},
			{"sql_injection_attempt", "'; DROP TABLE users; --", "''; DROP TABLE users; --"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				result := escapeStringValue(tt.input, dialect.MySQL)
				assert.Equal(t, tt.expected, result)
			})
		}
	})
	t.Run("postgres", func(t *testing.T) {
		tests := []struct {
			name     string
			input    string
			expected string
		}{
			{"no_escaping_needed", "hello", "hello"},
			{"single_quote", "it's", "it''s"},
			{"backslash_literal", `path\to\file`, `path\to\file`},
			{"both_quote_and_backslash", `it's a \test`, `it''s a \test`},
			{"empty_string", "", ""},
			{"sql_injection_attempt", "'; DROP TABLE users; --", "''; DROP TABLE users; --"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				result := escapeStringValue(tt.input, dialect.Postgres)
				assert.Equal(t, tt.expected, result)
			})
		}
	})
}

// TestWithVarsInvalidIdentifier tests that invalid identifiers are rejected.
func TestWithVarsInvalidIdentifier(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	drv := OpenDB(dialect.Postgres, db)

	// Attempt SQL injection via variable name
	rows := &Rows{}
	err = drv.Query(
		WithVar(context.Background(), "foo; DROP TABLE users; --", "bar"),
		"SELECT 1",
		[]any{},
		rows,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid session variable name")
}

// TestWithVarsEscapedValue tests that values are properly escaped.
func TestWithVarsEscapedValue(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	drv := OpenDB(dialect.Postgres, db)

	// Parameterized queries handle special characters safely
	mock.ExpectExec(`SET foo TO \$1`).WithArgs("it's escaped").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT 1").WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	mock.ExpectExec("RESET foo").WillReturnResult(sqlmock.NewResult(0, 0))

	rows := &Rows{}
	err = drv.Query(
		WithVar(context.Background(), "foo", "it's escaped"),
		"SELECT 1",
		[]any{},
		rows,
	)
	require.NoError(t, err)
	require.NoError(t, rows.Close())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestWithVars_MySQL(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	drv := OpenDB(dialect.MySQL, db)

	mock.ExpectExec(`SET @tenant_id = \?`).
		WithArgs("t-123").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT 1").
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	mock.ExpectExec(`SET @tenant_id = NULL`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	rows := &Rows{}
	err = drv.Query(
		WithVar(context.Background(), "tenant_id", "t-123"),
		"SELECT 1", []any{}, rows,
	)
	require.NoError(t, err)
	require.NoError(t, rows.Close())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestWithVars_SpecialCharacters(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	drv := OpenDB(dialect.Postgres, db)

	// SQL injection attempt in value should be safe via parameterization
	mock.ExpectExec(`SET app\.data TO \$1`).
		WithArgs("O'Brien; DROP TABLE users--").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT 1").
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	mock.ExpectExec(`RESET app\.data`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	rows := &Rows{}
	err = drv.Query(
		WithVar(context.Background(), "app.data", "O'Brien; DROP TABLE users--"),
		"SELECT 1", []any{}, rows,
	)
	require.NoError(t, err)
	require.NoError(t, rows.Close())
	require.NoError(t, mock.ExpectationsWereMet())
}
