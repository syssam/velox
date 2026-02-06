package sql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/syssam/velox/dialect"
)

// validIdentifierRe validates SQL identifiers (alphanumeric, underscores, dots for schema.name)
var validIdentifierRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_.]*$`)

// isValidIdentifier checks if the string is a valid SQL identifier.
func isValidIdentifier(s string) bool {
	return s != "" && len(s) <= 128 && validIdentifierRe.MatchString(s)
}

// escapeStringValue escapes a string value for safe use in SQL.
// It escapes both single quotes (by doubling) and backslashes (for MySQL compatibility).
func escapeStringValue(s string) string {
	// Fast path: if no escaping needed, return as-is
	if !strings.ContainsAny(s, `'\`) {
		return s
	}
	// Escape backslashes first, then single quotes
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "'", "''")
	return s
}

// Driver is a dialect.Driver implementation for SQL based databases.
type Driver struct {
	Conn
	dialect string
}

// NewDriver creates a new Driver with the given Conn and dialect.
func NewDriver(dialect string, c Conn) *Driver {
	return &Driver{dialect: dialect, Conn: c}
}

// Open wraps the database/sql.Open method and returns a dialect.Driver that implements the an ent/dialect.Driver interface.
func Open(dialect, source string) (*Driver, error) {
	db, err := sql.Open(dialect, source)
	if err != nil {
		return nil, err
	}
	return NewDriver(dialect, Conn{db, dialect}), nil
}

// OpenDB wraps the given database/sql.DB method with a Driver.
func OpenDB(dialect string, db *sql.DB) *Driver {
	return NewDriver(dialect, Conn{db, dialect})
}

// DB returns the underlying *sql.DB instance.
func (d Driver) DB() *sql.DB {
	return d.ExecQuerier.(*sql.DB)
}

// Dialect implements the dialect.Dialect method.
func (d Driver) Dialect() string {
	// If the underlying driver is wrapped with a telemetry driver.
	for _, name := range []string{dialect.MySQL, dialect.SQLite, dialect.Postgres} {
		if strings.HasPrefix(d.dialect, name) {
			return name
		}
	}
	return d.dialect
}

// Tx starts and returns a transaction.
func (d *Driver) Tx(ctx context.Context) (dialect.Tx, error) {
	return d.BeginTx(ctx, nil)
}

// BeginTx starts a transaction with options.
func (d *Driver) BeginTx(ctx context.Context, opts *TxOptions) (dialect.Tx, error) {
	tx, err := d.DB().BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &Tx{
		Conn: Conn{tx, d.dialect},
		Tx:   tx,
	}, nil
}

// Close closes the underlying connection.
func (d *Driver) Close() error { return d.DB().Close() }

// Tx implements dialect.Tx interface.
type Tx struct {
	Conn
	driver.Tx
}

// ctyVarsKey is the key used for attaching and reading the context variables.
type ctxVarsKey struct{}

// sessionVars holds sessions/transactions variables to set before every statement.
type sessionVars struct {
	vars []struct{ k, v string }
}

// WithVar returns a new context that holds the session variable to be executed before every query.
func WithVar(ctx context.Context, name, value string) context.Context {
	sv, _ := ctx.Value(ctxVarsKey{}).(sessionVars)
	sv.vars = append(sv.vars, struct {
		k, v string
	}{
		k: name,
		v: value,
	})
	return context.WithValue(ctx, ctxVarsKey{}, sv)
}

// VarFromContext returns the session variable value from the context.
func VarFromContext(ctx context.Context, name string) (string, bool) {
	sv, _ := ctx.Value(ctxVarsKey{}).(sessionVars)
	for _, s := range sv.vars {
		if s.k == name {
			return s.v, true
		}
	}
	return "", false
}

// WithIntVar calls WithVar with the string representation of the value.
func WithIntVar(ctx context.Context, name string, value int) context.Context {
	return WithVar(ctx, name, strconv.Itoa(value))
}

// ExecQuerier wraps the standard Exec and Query methods.
type ExecQuerier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// Conn implements dialect.ExecQuerier given ExecQuerier.
type Conn struct {
	ExecQuerier
	dialect string
}

// Exec implements the dialect.Exec method.
func (c Conn) Exec(ctx context.Context, query string, args, v any) (rerr error) {
	argv, ok := args.([]any)
	if !ok {
		return fmt.Errorf("dialect/sql: invalid type %T. expect []any for args", args)
	}
	ex, cf, err := c.maySetVars(ctx)
	if err != nil {
		return fmt.Errorf("dialect/sql: exec: set session vars: %w", err)
	}
	if cf != nil {
		defer func() { rerr = errors.Join(rerr, cf()) }()
	}
	switch v := v.(type) {
	case nil:
		if _, err := ex.ExecContext(ctx, query, argv...); err != nil {
			return fmt.Errorf("dialect/sql: exec: %w", err)
		}
	case *sql.Result:
		res, err := ex.ExecContext(ctx, query, argv...)
		if err != nil {
			return fmt.Errorf("dialect/sql: exec: %w", err)
		}
		*v = res
	default:
		return fmt.Errorf("dialect/sql: invalid type %T. expect *sql.Result", v)
	}
	return nil
}

// Query implements the dialect.Query method.
func (c Conn) Query(ctx context.Context, query string, args, v any) error {
	vr, ok := v.(*Rows)
	if !ok {
		return fmt.Errorf("dialect/sql: invalid type %T. expect *sql.Rows", v)
	}
	argv, ok := args.([]any)
	if !ok {
		return fmt.Errorf("dialect/sql: invalid type %T. expect []any for args", args)
	}
	ex, cf, err := c.maySetVars(ctx)
	if err != nil {
		return fmt.Errorf("dialect/sql: query: set session vars: %w", err)
	}
	rows, err := ex.QueryContext(ctx, query, argv...)
	if err != nil {
		if cf != nil {
			err = errors.Join(err, cf())
		}
		return fmt.Errorf("dialect/sql: query: %w", err)
	}
	*vr = Rows{rows}
	if cf != nil {
		vr.ColumnScanner = rowsWithCloser{rows, cf}
	}
	return nil
}

// maySetVars sets the session variables before executing a query.
func (c Conn) maySetVars(ctx context.Context) (ExecQuerier, func() error, error) {
	sv, _ := ctx.Value(ctxVarsKey{}).(sessionVars)
	if len(sv.vars) == 0 {
		return c, nil, nil
	}
	var (
		ex    ExecQuerier  // Underlying ExecQuerier.
		cf    func() error // Close function.
		reset []string     // Reset variables.
		seen  = make(map[string]struct{}, len(sv.vars))
	)
	switch e := c.ExecQuerier.(type) {
	case *sql.Tx:
		ex = e
	case *sql.DB:
		conn, err := e.Conn(ctx)
		if err != nil {
			return nil, nil, err
		}
		ex, cf = conn, conn.Close
	default:
		return nil, nil, fmt.Errorf("unsupported ExecQuerier type: %T", c.ExecQuerier)
	}
	for _, s := range sv.vars {
		// Validate the variable name to prevent SQL injection
		if !isValidIdentifier(s.k) {
			if cf != nil {
				_ = cf()
			}
			return nil, nil, fmt.Errorf("invalid session variable name: %q", s.k)
		}
		if _, ok := seen[s.k]; !ok {
			switch c.dialect {
			case dialect.Postgres:
				reset = append(reset, fmt.Sprintf("RESET %s", s.k))
			case dialect.MySQL:
				reset = append(reset, fmt.Sprintf("SET %s = NULL", s.k))
			}
			seen[s.k] = struct{}{}
		}
		// Escape the value to prevent SQL injection
		escapedValue := escapeStringValue(s.v)
		if _, err := ex.ExecContext(ctx, fmt.Sprintf("SET %s = '%s'", s.k, escapedValue)); err != nil {
			if cf != nil {
				err = errors.Join(err, cf())
			}
			return nil, nil, err
		}
	}
	// If there are variables to reset, and we need to return the
	// connection to the pool, we need to clean up the variables.
	// Use a background context with timeout for cleanup to ensure
	// it completes even if the original context was canceled.
	if cls := cf; cf != nil && len(reset) > 0 {
		cf = func() error {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			for _, q := range reset {
				if _, err := ex.ExecContext(cleanupCtx, q); err != nil {
					return errors.Join(err, cls())
				}
			}
			return cls()
		}
	}
	return ex, cf, nil
}

var _ dialect.Driver = (*Driver)(nil)

type (
	// Rows wraps the sql.Rows to avoid locks copy.
	Rows struct{ ColumnScanner }
	// Result is an alias to sql.Result.
	Result = sql.Result
	// NullBool is an alias to sql.NullBool.
	NullBool = sql.NullBool
	// NullInt64 is an alias to sql.NullInt64.
	NullInt64 = sql.NullInt64
	// NullString is an alias to sql.NullString.
	NullString = sql.NullString
	// NullFloat64 is an alias to sql.NullFloat64.
	NullFloat64 = sql.NullFloat64
	// NullTime represents a time.Time that may be null.
	NullTime = sql.NullTime
	// TxOptions holds the transaction options to be used in DB.BeginTx.
	TxOptions = sql.TxOptions
)

// NullScanner implements the sql.Scanner interface such that it
// can be used as a scan destination, similar to the types above.
type NullScanner struct {
	S     sql.Scanner
	Valid bool // Valid is true if the Scan value is not NULL.
}

// Scan implements the Scanner interface.
func (n *NullScanner) Scan(value any) error {
	n.Valid = value != nil
	if n.Valid {
		return n.S.Scan(value)
	}
	return nil
}

// ColumnScanner is the interface that wraps the standard
// sql.Rows methods used for scanning database rows.
type ColumnScanner interface {
	Close() error
	ColumnTypes() ([]*sql.ColumnType, error)
	Columns() ([]string, error)
	Err() error
	Next() bool
	NextResultSet() bool
	Scan(dest ...any) error
}

// rowsWithCloser wraps the ColumnScanner interface with a custom Close hook.
type rowsWithCloser struct {
	ColumnScanner
	closer func() error
}

// Close closes the underlying ColumnScanner and calls the custom closer.
func (r rowsWithCloser) Close() error {
	err := r.ColumnScanner.Close()
	return errors.Join(err, r.closer())
}
