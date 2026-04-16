package sql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"log"
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

// escapeStringValue escapes a string value for safe use in SQL SET statements.
// For PostgreSQL (standard_conforming_strings=on, default since 9.1), only single
// quotes need doubling. For MySQL, backslashes also need escaping.
// Note: This assumes standard_conforming_strings=on (PostgreSQL default since 9.1).
func escapeStringValue(s, dialectName string) string {
	// Fast path: if no escaping needed, return as-is
	if !strings.ContainsAny(s, `'\`) {
		return s
	}
	// For MySQL, escape backslashes first (before quote doubling).
	// PostgreSQL treats backslashes as literal with standard_conforming_strings=on.
	if dialectName == dialect.MySQL {
		s = strings.ReplaceAll(s, `\`, `\\`)
	}
	s = strings.ReplaceAll(s, "'", "''")
	return s
}

// Driver is a dialect.Driver implementation for SQL based databases.
type Driver struct {
	Conn
	dialect string
}

// NewDriver creates a new Driver with the given Conn and dialect.
func NewDriver(d string, c Conn) *Driver {
	return &Driver{dialect: d, Conn: c}
}

// Open wraps the database/sql.Open method and returns a dialect.Driver that implements the an ent/dialect.Driver interface.
func Open(d, source string) (*Driver, error) {
	db, err := sql.Open(d, source)
	if err != nil {
		return nil, err
	}
	return NewDriver(d, Conn{db, d}), nil
}

// OpenDB wraps the given database/sql.DB method with a Driver.
func OpenDB(d string, db *sql.DB) *Driver {
	return NewDriver(d, Conn{db, d})
}

// DB returns the underlying *sql.DB instance.
// Panics if the Driver was not created via Open or OpenDB (i.e., the
// underlying ExecQuerier is not a *sql.DB).
func (d Driver) DB() *sql.DB {
	db, ok := d.ExecQuerier.(*sql.DB)
	if !ok {
		panic(fmt.Sprintf("dialect/sql: Driver.DB() called but underlying connection is %T, not *sql.DB", d.ExecQuerier))
	}
	return db
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
		// Debug: log the failing SQL query
		log.Printf("[SQL-ERROR] query=%s args=%v err=%v", query, argv, err)
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
	// resetVars resets any already-set session variables and closes the connection.
	// Used both on error mid-loop and as the cleanup function on success.
	resetAndClose := func(reset []string, cls func() error) error {
		if len(reset) == 0 {
			if cls != nil {
				return cls()
			}
			return nil
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var resetErr error
		for _, q := range reset {
			if _, err := ex.ExecContext(cleanupCtx, q); err != nil {
				resetErr = errors.Join(resetErr, err)
			}
		}
		if cls != nil {
			return errors.Join(resetErr, cls())
		}
		return resetErr
	}
	for _, s := range sv.vars {
		// Validate the variable name to prevent SQL injection
		if !isValidIdentifier(s.k) {
			err := fmt.Errorf("invalid session variable name: %q", s.k)
			if cf != nil {
				err = errors.Join(err, resetAndClose(reset, cf))
			}
			return nil, nil, err
		}
		if _, ok := seen[s.k]; !ok {
			switch c.dialect {
			case dialect.Postgres:
				reset = append(reset, fmt.Sprintf("RESET %s", s.k))
			case dialect.MySQL:
				reset = append(reset, fmt.Sprintf("SET @%s = NULL", s.k))
			}
			seen[s.k] = struct{}{}
		}
		// Use parameterized queries to prevent SQL injection on values.
		// The identifier (s.k) is validated by isValidIdentifier() above.
		switch c.dialect {
		case dialect.Postgres:
			// PostgreSQL: SET <ident> TO $1
			if _, err := ex.ExecContext(ctx, fmt.Sprintf("SET %s TO $1", s.k), s.v); err != nil {
				if cf != nil {
					err = errors.Join(err, resetAndClose(reset, cf))
				}
				return nil, nil, err
			}
		case dialect.MySQL:
			// MySQL: SET @<ident> = ? (user variables support parameterization)
			if _, err := ex.ExecContext(ctx, fmt.Sprintf("SET @%s = ?", s.k), s.v); err != nil {
				if cf != nil {
					err = errors.Join(err, resetAndClose(reset, cf))
				}
				return nil, nil, err
			}
		default:
			// Unknown dialect: use escaped string value as fallback.
			escapedValue := escapeStringValue(s.v, c.dialect)
			if _, err := ex.ExecContext(ctx, fmt.Sprintf("SET %s = '%s'", s.k, escapedValue)); err != nil {
				if cf != nil {
					err = errors.Join(err, resetAndClose(reset, cf))
				}
				return nil, nil, err
			}
		}
	}
	// If there are variables to reset, and we need to return the
	// connection to the pool, we need to clean up the variables.
	if cls := cf; cf != nil && len(reset) > 0 {
		cf = func() error {
			return resetAndClose(reset, cls)
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
