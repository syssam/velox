package engine

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"strings"
)

// openSQLite is Connect's sql.Open for SQLite. SQLite enforces foreign keys
// only on a connection that turned them on, and a dsn without the pragma
// stores a reference to a missing row without a word. Unless the dsn names
// foreign_keys itself (modernc.org/sqlite's _pragma=foreign_keys(…),
// mattn/go-sqlite3's _foreign_keys or _fk), every connection the pool opens
// runs PRAGMA foreign_keys = ON first.
func openSQLite(driverName, dsn string) (*sql.DB, error) {
	db, err := sql.Open(driverName, dsn)
	if err != nil || namesForeignKeys(dsn) {
		return db, err
	}
	d := db.Driver()
	if err := db.Close(); err != nil {
		return nil, err
	}
	return sql.OpenDB(fkConnector{d: d, dsn: dsn}), nil
}

// namesForeignKeys reports whether a SQLite dsn sets foreign_keys itself.
func namesForeignKeys(dsn string) bool {
	_, query, _ := strings.Cut(dsn, "?")
	query = strings.ToLower(query)
	return strings.Contains(query, "foreign_keys") || strings.Contains(query, "_fk=")
}

// fkConnector opens connections of driver d to dsn, each with foreign keys on.
type fkConnector struct {
	d   driver.Driver
	dsn string
}

func (c fkConnector) Driver() driver.Driver { return c.d }

func (c fkConnector) Connect(ctx context.Context) (driver.Conn, error) {
	cn, err := c.d.Open(c.dsn)
	if err != nil {
		return nil, err
	}
	if err := execPragma(ctx, cn, "PRAGMA foreign_keys = ON"); err != nil {
		_ = cn.Close()
		return nil, fmt.Errorf("velox: turning SQLite's foreign keys on: %w", err)
	}
	return cn, nil
}

// execPragma runs q, which has no arguments, on cn.
func execPragma(ctx context.Context, cn driver.Conn, q string) error {
	if ex, ok := cn.(driver.ExecerContext); ok {
		_, err := ex.ExecContext(ctx, q, nil)
		if err != driver.ErrSkip {
			return err
		}
	}
	st, err := cn.Prepare(q)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	_, err = st.Exec(nil) //nolint:staticcheck // the fallback for a driver without ExecerContext
	return err
}
