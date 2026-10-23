package migrate

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/ddl"
)

// appliedLayout is applied_at as the runner writes and reads it, in UTC.
// PostgreSQL's is written with "+00:00".
const appliedLayout = "2006-01-02 15:04:05.000000"

// historyTypes is the history table's columns and their types on each
// dialect: PostgreSQL, MySQL, SQLite. SQLite's applied_at is text, holding
// appliedLayout, so the driver never reads it as a time.
var historyTypes = [...]struct{ name, pg, my, lite string }{
	{"version", "bigint", "bigint", "integer"},
	{"name", "text", "varchar(255)", "text"},
	{"checksum", "text", "varchar(64)", "text"},
	{"applied_at", "timestamp with time zone", "datetime(6)", "text"},
	{"duration_ms", "bigint", "bigint", "bigint"},
	{"dirty", "boolean", "tinyint(1)", "boolean"},
	{"statements", "integer", "integer", "integer"},
	{"error", "text", "text", "text"},
}

// q is name quoted for the session's dialect.
func (s *session) q(name string) string { return ddl.Ident(s.d, name) }

// ph is the session's placeholder for the i-th argument, from 1.
func (s *session) ph(i int) string {
	if s.d == velox.Postgres {
		return fmt.Sprintf("$%d", i)
	}
	return "?"
}

// createHistory creates the history table in the connection's default
// schema, when it is missing.
func (s *session) createHistory(ctx context.Context) error {
	var b strings.Builder
	b.WriteString("CREATE TABLE IF NOT EXISTS " + s.q(s.cfg.Table) + " (")
	for i, c := range historyTypes {
		if i > 0 {
			b.WriteString(", ")
		}
		typ := c.pg
		switch s.d {
		case velox.MySQL:
			typ = c.my
		case velox.SQLite:
			typ = c.lite
		}
		b.WriteString(s.q(c.name) + " " + typ + " NOT NULL")
		if c.name == "version" {
			b.WriteString(" PRIMARY KEY")
		}
	}
	b.WriteString(")")
	if s.d == velox.MySQL {
		b.WriteString(" ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin")
	}
	_, err := s.conn.ExecContext(ctx, b.String())
	return err
}

// hasHistory reports whether the history table exists.
func (s *session) hasHistory(ctx context.Context) (bool, error) {
	var q string
	switch s.d {
	case velox.Postgres:
		q = `SELECT to_regclass($1) IS NOT NULL`
	case velox.MySQL:
		q = `SELECT count(*) > 0 FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?`
	default:
		q = `SELECT count(*) > 0 FROM sqlite_master WHERE type = 'table' AND name = ?`
	}
	name := s.cfg.Table
	if s.d == velox.Postgres {
		name = s.q(name)
	}
	var ok bool
	err := s.conn.QueryRowContext(ctx, q, name).Scan(&ok)
	return ok, err
}

// history is the table's rows by version; exists is false when there is no table.
func (s *session) history(ctx context.Context) (rows []Applied, exists bool, err error) {
	if exists, err = s.hasHistory(ctx); err != nil || !exists {
		return nil, exists, err
	}
	at := s.q("applied_at")
	switch s.d {
	case velox.Postgres:
		at = "to_char(" + at + " AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS.US')"
	case velox.MySQL:
		at = "CAST(" + at + " AS CHAR)"
	}
	q := fmt.Sprintf("SELECT %s, %s, %s, %s, %s, %s, %s, %s FROM %s ORDER BY %[1]s",
		s.q("version"), s.q("name"), s.q("checksum"), at, s.q("duration_ms"), s.q("dirty"), s.q("statements"), s.q("error"), s.q(s.cfg.Table))
	rs, err := s.conn.QueryContext(ctx, q)
	if err != nil {
		return nil, true, err
	}
	defer func() { _ = rs.Close() }()
	for rs.Next() {
		var (
			a        Applied
			version  int64
			appliedS string
			ms       int64
		)
		if err := rs.Scan(&version, &a.Name, &a.Sum, &appliedS, &ms, &a.Dirty, &a.Statements, &a.Error); err != nil {
			return nil, true, err
		}
		a.Version, a.Duration = uint64(version), time.Duration(ms)*time.Millisecond
		if a.AppliedAt, err = time.Parse(appliedLayout, appliedS); err != nil {
			return nil, true, &Error{Code: "history", Reason: fmt.Sprintf("version %d: applied_at %q is not %s", version, appliedS, appliedLayout)}
		}
		rows = append(rows, a)
	}
	return rows, true, rs.Err()
}

// record inserts a's row.
func (s *session) record(ctx context.Context, a Applied) error {
	at := a.AppliedAt.UTC().Format(appliedLayout)
	if s.d == velox.Postgres {
		at += "+00:00"
	}
	cols := make([]string, len(historyTypes))
	phs := make([]string, len(historyTypes))
	for i, c := range historyTypes {
		cols[i], phs[i] = s.q(c.name), s.ph(i+1)
	}
	q := "INSERT INTO " + s.q(s.cfg.Table) + " (" + strings.Join(cols, ", ") + ") VALUES (" + strings.Join(phs, ", ") + ")"
	_, err := s.conn.ExecContext(ctx, q, int64(a.Version), a.Name, a.Sum, at, a.Duration.Milliseconds(), a.Dirty, a.Statements, a.Error)
	return err
}
