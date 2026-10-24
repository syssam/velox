package migrate

import (
	"context"
	"database/sql"
	"io/fs"
	"strings"

	"github.com/syssam/velox2"
)

// Verify applies dir to db, an empty database, and fails unless Drift then
// finds no change against dir's schema.json: a test's check that the files
// make what the snapshot says, catching a hand edit the snapshot does not
// know. A database holding a table of its default schema but the history
// table, or a row of the history, is refused, as is one that differs after
// the files ran; both are an *Error of Code "drift". Drift's warnings do
// not fail it. On SQLite, db must be a file database: Verify, Up and Drift
// each open a connection of their own and close it, and each connection to
// :memory: is a new, empty database.
func Verify(ctx context.Context, db *sql.DB, dir fs.FS) error {
	d, err := ReadDir(dir)
	if err != nil {
		return err
	}
	if db == nil {
		return &Error{Code: "nil", Reason: "Verify needs a database"}
	}
	if err := verifyEmpty(ctx, db, d.Config); err != nil {
		return err
	}
	if _, err := Up(ctx, db, dir); err != nil {
		return err
	}
	r, err := Drift(ctx, db, d.Snapshot)
	if err != nil {
		return err
	}
	if len(r.Changes) == 0 {
		return nil
	}
	lines := make([]string, len(r.Changes))
	for i, c := range r.Changes {
		lines[i] = c.String()
	}
	return &Error{Code: "drift", Reason: "the database differs from schema.json after applying the directory:\n  " + strings.Join(lines, "\n  ")}
}

// The tables of the connection's default schema, by name.
var verifyTablesQuery = map[velox.Dialect]string{
	velox.Postgres: `SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = current_schema() AND c.relkind IN ('r', 'p') ORDER BY 1`,
	velox.MySQL: `SELECT TABLE_NAME FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_TYPE = 'BASE TABLE' ORDER BY 1`,
	velox.SQLite: `SELECT name FROM sqlite_schema WHERE type = 'table' AND substr(name, 1, 7) <> 'sqlite_' ORDER BY 1`,
}

// verifyEmpty refuses db unless its default schema holds no table but the
// history table, and that no row.
func verifyEmpty(ctx context.Context, db *sql.DB, cfg Config) error {
	s, err := open(ctx, db, cfg)
	if err != nil {
		return err
	}
	defer s.close()
	refuse := func(object string) error {
		return &Error{Code: "drift", Object: object, Reason: "Verify needs an empty database"}
	}
	var found string
	err = eachRow(ctx, s.conn, verifyTablesQuery[cfg.Dialect], nil, func(rows *sql.Rows) error {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		if found == "" && name != cfg.Table {
			found = name
		}
		return nil
	})
	if err != nil {
		return wrap(ctx, "verify", err)
	}
	if found != "" {
		return refuse(found)
	}
	rows, _, err := s.history(ctx)
	if err != nil {
		return err
	}
	if len(rows) > 0 {
		return refuse(cfg.Table)
	}
	return nil
}
