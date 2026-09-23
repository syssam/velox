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
	"sync"
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
	server  *serverCaps
}

// serverCaps caches the capabilities of the server behind a Driver. It is
// a pointer so copies of the Driver (and wrappers embedding it) share one
// probe.
type serverCaps struct {
	mu   sync.Mutex
	done bool
	caps dialect.Capabilities
}

// NewDriver creates a new Driver with the given Conn and dialect.
func NewDriver(d string, c Conn) *Driver {
	return &Driver{dialect: d, Conn: c, server: &serverCaps{}}
}

// ServerCapabilities implements dialect.CapabilityProber. For MySQL, whose
// capabilities depend on the server version (window functions arrived in
// 8.0, MariaDB 10.2), the first call runs SELECT VERSION() and the answer
// is cached for the life of the Driver; a failed probe is returned and
// retried on the next call. Other dialects return their static set without
// a round trip.
func (d *Driver) ServerCapabilities(ctx context.Context) (dialect.Capabilities, error) {
	name := d.Dialect()
	if name != dialect.MySQL {
		return dialect.GetCapabilities(name), nil
	}
	if d.server == nil {
		return d.probeCapabilities(ctx, name)
	}
	d.server.mu.Lock()
	defer d.server.mu.Unlock()
	if d.server.done {
		return d.server.caps, nil
	}
	caps, err := d.probeCapabilities(ctx, name)
	if err != nil {
		return caps, err
	}
	d.server.caps, d.server.done = caps, true
	return caps, nil
}

// probeCapabilities asks the server for its version.
func (d *Driver) probeCapabilities(ctx context.Context, name string) (dialect.Capabilities, error) {
	rows := &Rows{}
	if err := d.Query(ctx, "SELECT VERSION()", []any{}, rows); err != nil {
		return dialect.Capabilities{}, fmt.Errorf("dialect/sql: query server version: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var version string
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return dialect.Capabilities{}, fmt.Errorf("dialect/sql: query server version: %w", err)
		}
		return dialect.Capabilities{}, errors.New("dialect/sql: query server version: no rows")
	}
	if err := rows.Scan(&version); err != nil {
		return dialect.Capabilities{}, fmt.Errorf("dialect/sql: scan server version: %w", err)
	}
	return dialect.VersionCapabilities(name, version), nil
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
//
// The variable is set before each statement run with the returned context,
// and is scoped as follows:
//
//   - Outside a transaction, it is set on a connection reserved for the
//     statement and reset before that connection returns to the pool.
//   - Inside a transaction on Postgres, it is set with set_config(name, value,
//     true) — transaction-local — so it stays in effect for the REST of the
//     transaction, including later statements whose context does not carry it.
//   - Inside a transaction on MySQL, the user variable is reset after each
//     statement, so a later statement in the same transaction does not see it
//     unless its own context carries it.
//
// On either database the value never survives COMMIT or ROLLBACK and never
// reaches another pooled connection. Because the in-transaction behavior
// differs, pass the WithVar context to every statement that depends on the
// variable rather than relying on an earlier statement having set it.
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

// resetStmt is a statement that resets one session variable.
type resetStmt struct {
	query string
	args  []any
}

// maySetVars sets the session variables before executing a query.
//
// Outside a transaction the variables are set on a dedicated connection and
// reset before it goes back to the pool. Inside a transaction they must not
// outlive the transaction either, because its connection returns to the pool
// at COMMIT/ROLLBACK:
//
//   - Postgres uses set_config(name, value, true), which is transaction-local:
//     the value vanishes at COMMIT or ROLLBACK and there is nothing to reset.
//     A plain SET would persist on the pooled connection after COMMIT and leak
//     into its next borrower.
//   - MySQL user variables are always session-scoped, so they are reset by the
//     returned cleanup function, exactly as outside a transaction.
//
// Postgres rejects bind parameters in a SET statement, so set_config is also
// what lets the value travel as a parameter rather than as SQL text.
func (c Conn) maySetVars(ctx context.Context) (ExecQuerier, func() error, error) {
	sv, _ := ctx.Value(ctxVarsKey{}).(sessionVars)
	if len(sv.vars) == 0 {
		return c, nil, nil
	}
	var (
		ex    ExecQuerier  // Underlying ExecQuerier.
		cf    func() error // Close function.
		inTx  bool         // Variables are set on a transaction's connection.
		reset []resetStmt  // Reset variables.
		seen  = make(map[string]struct{}, len(sv.vars))
	)
	switch e := c.ExecQuerier.(type) {
	case *sql.Tx:
		ex, inTx = e, true
	case *sql.DB:
		conn, err := e.Conn(ctx)
		if err != nil {
			return nil, nil, err
		}
		ex, cf = conn, conn.Close
	default:
		return nil, nil, fmt.Errorf("unsupported ExecQuerier type: %T", c.ExecQuerier)
	}
	// resetAndClose resets any already-set session variables and closes the
	// connection (cls is nil inside a transaction). Used both on error mid-loop
	// and as the cleanup function on success.
	resetAndClose := func(reset []resetStmt, cls func() error) error {
		if len(reset) == 0 {
			if cls != nil {
				return cls()
			}
			return nil
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var resetErr error
		for _, r := range reset {
			if _, err := ex.ExecContext(cleanupCtx, r.query, r.args...); err != nil {
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
			return nil, nil, errors.Join(err, resetAndClose(reset, cf))
		}
		// Use parameterized queries to prevent SQL injection on values.
		// The identifier (s.k) is validated by isValidIdentifier() above.
		var err error
		switch c.dialect {
		case dialect.Postgres:
			// PostgreSQL: set_config(<ident>, <value>, <is_local>).
			_, err = ex.ExecContext(ctx, fmt.Sprintf("SELECT set_config($1, $2, %t)", inTx), s.k, s.v)
		case dialect.MySQL:
			// MySQL: SET @<ident> = ? (user variables support parameterization)
			_, err = ex.ExecContext(ctx, fmt.Sprintf("SET @%s = ?", s.k), s.v)
		default:
			// Unknown dialect: use escaped string value as fallback.
			escapedValue := escapeStringValue(s.v, c.dialect)
			_, err = ex.ExecContext(ctx, fmt.Sprintf("SET %s = '%s'", s.k, escapedValue))
		}
		if err != nil {
			// Only variables whose set succeeded are queued for reset:
			// re-running the reset of the one that just failed would
			// usually fail the same way and report the error twice.
			return nil, nil, errors.Join(err, resetAndClose(reset, cf))
		}
		// Queue the reset once per variable, after its first successful set.
		if _, ok := seen[s.k]; !ok {
			switch c.dialect {
			case dialect.Postgres:
				// A transaction-local set_config has nothing to reset. A NULL
				// value makes set_config reset the setting, like RESET, but
				// the name travels as a parameter: RESET <name> is SQL text,
				// and a name with a keyword component (app.user) that
				// set_config accepted would fail to parse, leaving the value
				// on the pooled connection.
				if !inTx {
					reset = append(reset, resetStmt{"SELECT set_config($1, NULL, false)", []any{s.k}})
				}
			case dialect.MySQL:
				reset = append(reset, resetStmt{fmt.Sprintf("SET @%s = NULL", s.k), nil})
			}
			seen[s.k] = struct{}{}
		}
	}
	// If there are variables to reset, run the reset once the statement is
	// done: before the connection returns to the pool or, inside a
	// transaction, before the transaction ends.
	if len(reset) > 0 {
		cls := cf
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
