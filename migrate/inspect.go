package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/syssam/velox2"
)

// Inspect reads the tables of db, a database of dialect d, as a Schema:
// their columns, keys, indexes, checks and comments, and the enum types
// their columns use. tables nil reads every table of the connection's
// default schema but velox_migrations; a named table that is not there is
// absent from the result. A table of the default schema, and a foreign
// key's table there, has Schema "". Inspect only reads, on one connection
// of db that it closes after, never returning it to the pool.
func Inspect(ctx context.Context, db *sql.DB, d velox.Dialect, tables []TableRef) (*Schema, error) {
	s, _, err := inspect(ctx, db, d, tables)
	return s, err
}

// Drift reads db's tables and enum types that snapshot names, and reports
// the changes that would bring them to snapshot, and warnings: spellings the
// normaliser cannot match, and what the model cannot hold (a MySQL engine
// other than InnoDB, an invalid index, a NOT VALID constraint, a GENERATED
// ALWAYS identity). It never reads a table snapshot does not name.
func Drift(ctx context.Context, db *sql.DB, snapshot *Schema) (*DriftReport, error) {
	if snapshot == nil {
		return nil, &Error{Code: "nil", Reason: "Drift needs the snapshot"}
	}
	refs := make([]TableRef, 0, len(snapshot.Tables)) // not nil: nil would read every table
	for _, t := range snapshot.Tables {
		refs = append(refs, t.Ref())
	}
	enums := make([]TableRef, 0, len(snapshot.Enums))
	for _, e := range snapshot.Enums {
		enums = append(enums, e.ref())
	}
	inspected, notes, err := inspectWith(ctx, db, snapshot.Dialect, refs, enums)
	if err != nil {
		return nil, err
	}
	return drift(inspected, notes, snapshot)
}

// A querier is what inspect reads through: a transaction on its connection.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// inspect is Inspect with its notes; Inspect returns its schema alone.
func inspect(ctx context.Context, db *sql.DB, d velox.Dialect, tables []TableRef) (*Schema, []note, error) {
	return inspectWith(ctx, db, d, tables, nil)
}

// inspectWith is inspect, reading the enum types enums names too, as Drift
// does for its snapshot's: a type no column uses any more is still there.
func inspectWith(ctx context.Context, db *sql.DB, d velox.Dialect, tables, enums []TableRef) (*Schema, []note, error) {
	switch d {
	case velox.Postgres:
	case velox.MySQL, velox.SQLite:
		return nil, nil, &Error{Code: "dialect", Reason: "Inspect on " + dialectName(d) + " is not built yet"}
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
	// PostgreSQL prints a timestamptz literal in the session's zone, and the
	// normaliser reads one without a zone as UTC; it prints a literal's
	// backslashes as written only with standard_conforming_strings on.
	for _, q := range []string{`SET TimeZone = 'UTC'`, `SET standard_conforming_strings = on`} {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			return nil, nil, wrap(ctx, "inspect", err)
		}
	}
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, nil, wrap(ctx, "inspect", err)
	}
	defer func() { _ = tx.Rollback() }()
	s, notes, err := inspectPostgres(ctx, tx, tables, enums)
	if err != nil {
		return nil, nil, wrap(ctx, "inspect", err)
	}
	s.sort()
	return s, notes, nil
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
