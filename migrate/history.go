package migrate

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/ddl"
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

// historyTable is the history table as the session's statements name it:
// qualified by the schema (PostgreSQL) or database (MySQL) open read, so a
// file that changes search_path or USEs another database still records in
// it; unqualified on SQLite.
func (s *session) historyTable() string {
	switch s.d {
	case velox.Postgres:
		return s.q(s.schema) + "." + s.q(s.cfg.Table)
	case velox.MySQL:
		return s.q(s.db) + "." + s.q(s.cfg.Table)
	}
	return s.q(s.cfg.Table)
}

// createHistory creates the history table in the schema or database open
// read, when it is missing.
func (s *session) createHistory(ctx context.Context) error {
	var b strings.Builder
	b.WriteString("CREATE TABLE IF NOT EXISTS " + s.historyTable() + " (")
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
	return wrap(ctx, "creating the history table "+s.cfg.Table, err)
}

// hasHistory reports whether the history table exists.
func (s *session) hasHistory(ctx context.Context) (bool, error) {
	var q string
	args := []any{s.cfg.Table}
	switch s.d {
	case velox.Postgres:
		q = `SELECT to_regclass($1) IS NOT NULL`
		args = []any{s.historyTable()}
	case velox.MySQL:
		q = `SELECT count(*) > 0 FROM information_schema.tables WHERE table_schema = ? AND table_name = ?`
		args = []any{s.db, s.cfg.Table}
	default:
		q = `SELECT count(*) > 0 FROM sqlite_master WHERE type = 'table' AND name = ?`
	}
	var ok bool
	err := s.conn.QueryRowContext(ctx, q, args...).Scan(&ok)
	return ok, err
}

// history is the table's rows by version, none when there is no table.
func (s *session) history(ctx context.Context) ([]Applied, error) {
	rows, err := s.readHistory(ctx)
	return rows, wrap(ctx, "reading the history table "+s.cfg.Table, err)
}

// readHistory is history, its errors unwrapped. The columns are qualified
// by the table, so a column missing from a table of another shape is an
// error on SQLite too, not a string. With no table there are no rows.
func (s *session) readHistory(ctx context.Context) ([]Applied, error) {
	if exists, err := s.hasHistory(ctx); err != nil || !exists {
		return nil, err
	}
	col := func(name string) string { return s.q(s.cfg.Table) + "." + s.q(name) }
	at := col("applied_at")
	switch s.d {
	case velox.Postgres:
		at = "to_char(" + at + " AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS.US')"
	case velox.MySQL:
		at = "CAST(" + at + " AS CHAR)"
	}
	q := fmt.Sprintf("SELECT %s, %s, %s, %s, %s, %s, %s, %s FROM %s ORDER BY %[1]s",
		col("version"), col("name"), col("checksum"), at, col("duration_ms"), col("dirty"), col("statements"), col("error"), s.historyTable())
	rs, err := s.conn.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	var rows []Applied
	for rs.Next() {
		var (
			a        Applied
			version  int64
			appliedS string
			ms       int64
		)
		if err := rs.Scan(&version, &a.Name, &a.Sum, &appliedS, &ms, &a.Dirty, &a.Statements, &a.Error); err != nil {
			return nil, err
		}
		a.Version, a.Duration = uint64(version), time.Duration(ms)*time.Millisecond
		if a.AppliedAt, err = time.Parse(appliedLayout, appliedS); err != nil {
			return nil, &Error{Code: "history", Reason: fmt.Sprintf("version %d: applied_at %q is not %s", version, appliedS, appliedLayout)}
		}
		rows = append(rows, a)
	}
	return rows, rs.Err()
}

// at is t as applied_at is written.
func (s *session) at(t time.Time) string {
	at := t.UTC().Format(appliedLayout)
	if s.d == velox.Postgres {
		at += "+00:00"
	}
	return at
}

// record inserts a's row.
func (s *session) record(ctx context.Context, a Applied) error {
	at := s.at(a.AppliedAt)
	cols := make([]string, len(historyTypes))
	phs := make([]string, len(historyTypes))
	for i, c := range historyTypes {
		cols[i], phs[i] = s.q(c.name), s.ph(i+1)
	}
	q := "INSERT INTO " + s.historyTable() + " (" + strings.Join(cols, ", ") + ") VALUES (" + strings.Join(phs, ", ") + ")"
	_, err := s.conn.ExecContext(ctx, q, int64(a.Version), a.Name, a.Sum, at, a.Duration.Milliseconds(), a.Dirty, a.Statements, a.Error)
	return err
}

// hasTables reports whether the database holds a table besides the
// history. PostgreSQL: an ordinary or partitioned table, not temporary, of
// a schema not named pg_% (pg_catalog, pg_toast, pg_temp_N) nor
// information_schema, that no extension owns (as PostGIS's
// spatial_ref_sys); the history is matched by its schema, the one open read,
// and its name. MySQL: a base table of the database open read but the history. SQLite:
// a table of main not named sqlite_% nor the history.
func (s *session) hasTables(ctx context.Context) (bool, error) {
	var q string
	switch s.d {
	case velox.Postgres:
		q = `SELECT EXISTS (SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind IN ('r', 'p') AND c.relpersistence <> 't'
AND n.nspname NOT LIKE 'pg\_%' AND n.nspname <> 'information_schema'
AND NOT (n.nspname = $2 AND c.relname = $1)
AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_depend d WHERE d.classid = 'pg_catalog.pg_class'::regclass AND d.objid = c.oid AND d.deptype = 'e'))`
	case velox.MySQL:
		q = `SELECT count(*) > 0 FROM information_schema.tables WHERE table_schema = ? AND table_type = 'BASE TABLE' AND table_name <> ?`
	default:
		q = `SELECT count(*) > 0 FROM main.sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite\_%' ESCAPE '\' AND lower(name) <> lower(?)`
	}
	args := []any{s.cfg.Table}
	switch s.d {
	case velox.Postgres:
		args = append(args, s.schema)
	case velox.MySQL:
		args = []any{s.db, s.cfg.Table}
	}
	var ok bool
	err := s.conn.QueryRowContext(ctx, q, args...).Scan(&ok)
	return ok, err
}
