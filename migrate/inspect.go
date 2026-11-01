package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/ddl"
)

// Inspect reads the tables of db, a database of dialect d, as a Schema:
// their columns, keys, indexes, checks and comments, and the enum types
// their columns use. tables nil reads every table of the connection's
// default schema but velox_migrations; a named table that is not there is
// absent from the result. A table of the default schema, and a foreign
// key's table there, has Schema "". On MySQL a primary key is named
// PRIMARY, the name MySQL gives every one. On SQLite the default schema is
// main, and constraint names and checks come from the table's CREATE TABLE
// text: where it cannot be read, a table has no checks and its foreign keys
// are unnamed; Inspect waits up to 5 minutes for a writer to finish. Inspect
// only reads, on one connection of db that it closes after, never returning
// it to the pool.
func Inspect(ctx context.Context, db *sql.DB, d velox.Dialect, tables []TableRef) (*Schema, error) {
	s, _, err := inspect(ctx, db, d, tables)
	return s, err
}

// Drift reads db's tables and enum types that snapshot names, and reports
// the changes that would bring them to snapshot, and warnings: spellings the
// normaliser cannot match, and what the model cannot hold (a MySQL engine
// other than InnoDB, a column's collation other than the table's on MySQL,
// its type's on PostgreSQL or BINARY on SQLite, an UNLOGGED or TEMPORARY
// PostgreSQL table, an invalid index, a NOT VALID constraint, a GENERATED
// ALWAYS identity, a STRICT SQLite table, its triggers and a constraint's
// ON CONFLICT). It never reads a table snapshot
// does not name, and on SQLite it waits for a writer as Inspect does.
func Drift(ctx context.Context, db *sql.DB, snapshot *Schema) (*DriftReport, error) {
	if snapshot == nil {
		return nil, &Error{Code: "nil", Reason: "Drift needs the snapshot"}
	}
	refs, enums := refsOf(snapshot)
	inspected, notes, err := inspectWith(ctx, db, snapshot.Dialect, refs, enums)
	if err != nil {
		return nil, err
	}
	return drift(inspected, notes, snapshot)
}

// refsOf is the tables and enum types s names, neither nil: nil would read
// every table.
func refsOf(s *Schema) (tables, enums []TableRef) {
	tables = make([]TableRef, 0, len(s.Tables))
	for _, t := range s.Tables {
		tables = append(tables, t.Ref())
	}
	enums = make([]TableRef, 0, len(s.Enums))
	for _, e := range s.Enums {
		enums = append(enums, e.ref())
	}
	return tables, enums
}

// A querier is what inspect reads through: a transaction on its connection.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// eachRow runs query on q and calls scan for each row.
func eachRow(ctx context.Context, q querier, query string, args []any, scan func(*sql.Rows) error) error {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

// parseEnumCheck reads expr, a check as the database prints it, as one that
// holds one of columns to values: col IN ('a', 'b').
func parseEnumCheck(d velox.Dialect, expr string, columns []Column) (col string, values []string, ok bool) {
	// The normal form spells the operator in lower case.
	n := normalizePredicate(d, expr)
	if i := strings.Index(n, " in ("); i > 0 && !strings.Contains(n[:i], "'") {
		n = n[:i] + " IN (" + n[i+len(" in ("):]
	}
	col, values, ok = ddl.ParseEnumCheck(d, n)
	return col, values, ok && slices.ContainsFunc(columns, func(c Column) bool { return c.Name == col })
}

// inspect is Inspect with its notes; Inspect returns its schema alone.
func inspect(ctx context.Context, db *sql.DB, d velox.Dialect, tables []TableRef) (*Schema, []note, error) {
	return inspectWith(ctx, db, d, tables, nil)
}

// inspectWith is inspect, reading the enum types enums names too, as Drift
// does for its snapshot's: a type no column uses any more is still there.
func inspectWith(ctx context.Context, db *sql.DB, d velox.Dialect, tables, enums []TableRef) (*Schema, []note, error) {
	switch d {
	case velox.Postgres, velox.MySQL, velox.SQLite:
	default:
		return nil, nil, &Error{Code: "dialect", Reason: fmt.Sprintf("Inspect was given the unknown dialect %q", d.String())}
	}
	if db == nil {
		return nil, nil, &Error{Code: "nil", Reason: "Inspect needs a database"}
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, nil, wrap(ctx, "inspect", err)
	}
	// The settings change only this connection, which is closed after.
	defer discard(conn)
	var s *Schema
	var notes []note
	switch d {
	case velox.MySQL:
		s, notes, err = inspectMySQLConn(ctx, conn, tables)
	case velox.SQLite:
		s, notes, err = inspectSQLiteConn(ctx, conn, tables)
	default:
		s, notes, err = inspectPostgresConn(ctx, conn, tables, enums)
	}
	if err != nil {
		return nil, nil, wrap(ctx, "inspect", err)
	}
	s.sort()
	return s, notes, nil
}

// inspectPostgresConn is inspectPostgres on conn, in a read-only snapshot.
func inspectPostgresConn(ctx context.Context, conn *sql.Conn, tables, enums []TableRef) (*Schema, []note, error) {
	// PostgreSQL prints a timestamptz literal in the session's zone, and the
	// normaliser reads one without a zone as UTC; it prints a literal's
	// backslashes as written only with standard_conforming_strings on.
	for _, q := range []string{`SET TimeZone = 'UTC'`, `SET standard_conforming_strings = on`} {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			return nil, nil, err
		}
	}
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	return inspectPostgres(ctx, tx, tables, enums)
}

// inspectMySQLConn is inspectMySQL on conn, in a consistent read-only
// snapshot, the session in UTC: MySQL prints a timestamp's default in the
// session's zone.
func inspectMySQLConn(ctx context.Context, conn *sql.Conn, tables []TableRef) (*Schema, []note, error) {
	for _, q := range []string{`SET time_zone = '+00:00'`, `START TRANSACTION WITH CONSISTENT SNAPSHOT, READ ONLY`} {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			return nil, nil, err
		}
	}
	defer func() { _, _ = conn.ExecContext(ctx, `ROLLBACK`) }() // and the connection is discarded
	return inspectMySQL(ctx, conn, tables)
}

// inspectSQLiteConn is inspectSQLite on conn, in one read transaction. It
// waits for a writer as long as Up waits for the lock by default: a writer
// committing keeps every reader out.
func inspectSQLiteConn(ctx context.Context, conn *sql.Conn, tables []TableRef) (*Schema, []note, error) {
	if _, err := conn.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout = %d", waitMillis(defaultLockWait))); err != nil {
		return nil, nil, err
	}
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	return inspectSQLite(ctx, tx, tables)
}

var (
	// pgCasts is one or more casts as PostgreSQL prints them after a
	// literal: ::text, ::character varying(10), ::billing.mood, ::"Mood".
	pgCasts = regexp.MustCompile(`^(?:::(?:"(?:[^"]|"")+"|[a-z_][a-z0-9_$]*)(?:\.(?:"(?:[^"]|"")+"|[a-z_][a-z0-9_$]*))?(?: [a-z]+)*(?:\([0-9, ]+\))?(?: [a-z]+)*(?:\[\])*)+$`)
	// pgNumber is a number as a quoted literal holds it.
	pgNumber = regexp.MustCompile(`^-?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)
)

// pgNumeric are the numeric types a quoted number is cast to.
var pgNumeric = []string{"smallint", "integer", "bigint", "numeric", "real", "double precision"}

// pgClock are the time types whose literal PostgreSQL prints in a form of
// its own, a timestamptz in the session's zone: the cast is kept.
var pgClock = []string{"timestamp with time zone", "timestamp without time zone", "time without time zone"}

// normalizeDefault is a default as the database prints it, in the model's
// spelling: casts dropped from literals, a quoted number cast to a numeric
// type unquoted, nextval('s'::regclass) as nextval('s'), NULL as no
// default. A timestamp or time literal keeps its cast, for drift to compare
// by value, since PostgreSQL prints the value in its own form.
func normalizeDefault(d velox.Dialect, s string) string {
	if d != velox.Postgres {
		return s
	}
	if inner, ok := strings.CutPrefix(s, "nextval("); ok {
		if lit, ok := strings.CutSuffix(inner, "::regclass)"); ok && literalEnd(lit) == len(lit) {
			return "nextval(" + lit + ")"
		}
		return s
	}
	if rest, ok := strings.CutPrefix(s, "NULL"); ok && (rest == "" || pgCasts.MatchString(rest)) {
		return "" // DEFAULT NULL is no default
	}
	end := literalEnd(s)
	if end <= 0 || end == len(s) || !pgCasts.MatchString(s[end:]) {
		return s
	}
	lit := s[:end]
	typ := s[end:]
	typ = typ[strings.LastIndex(typ, "::")+2:]
	if i := strings.IndexByte(typ, '('); i >= 0 {
		typ = typ[:i]
	}
	switch v := lit[1 : len(lit)-1]; {
	case pgNumber.MatchString(v) && slices.Contains(pgNumeric, typ):
		return v
	case slices.Contains(pgClock, typ):
		return s // PostgreSQL prints its own form of the value: drift compares by value
	}
	return lit
}

// literalEnd is the end of the '…' literal s starts with, or -1 when s does
// not start with a closed one.
func literalEnd(s string) int {
	if !strings.HasPrefix(s, "'") {
		return -1
	}
	for i := 1; i < len(s); i++ {
		if s[i] != '\'' {
			continue
		}
		if i+1 < len(s) && s[i+1] == '\'' {
			i++
			continue
		}
		return i + 1
	}
	return -1
}
