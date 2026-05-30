package runner

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	// go-sql-driver/mysql registers the "mysql" database/sql driver used by both
	// the velox and ent executors against MySQL. ORM-free, same rationale as
	// lib/pq in db_postgres.go.
	_ "github.com/go-sql-driver/mysql"
)

// mysqlEnvVar holds the velox MySQL DSN, e.g.
// "root:test@tcp(localhost:3306)/parity?parseTime=true&multiStatements=true".
const mysqlEnvVar = "VELOX_TEST_MYSQL"

// mysqlEntSuffix names ent's sibling database on a shared MySQL server, same
// separate-database convention as Postgres.
const mysqlEntSuffix = "_ent"

// deriveEntMySQLDSN returns a sibling DSN whose database is "<base>_ent" so the
// ent client migrates into its own database. A go-sql-driver DSN has the form
// "user:pass@tcp(host:port)/dbname?params"; this rewrites only the path segment
// between the last '/' and the '?'. Returns the base db name and the derived DSN.
func deriveEntMySQLDSN(dsn string) (baseDB, entDSN string) {
	slash := strings.LastIndex(dsn, "/")
	if slash < 0 {
		return "", dsn
	}
	rest := dsn[slash+1:]
	q := strings.IndexByte(rest, '?')
	if q < 0 {
		baseDB = rest
		return baseDB, dsn[:slash+1] + baseDB + mysqlEntSuffix
	}
	baseDB = rest[:q]
	return baseDB, dsn[:slash+1] + baseDB + mysqlEntSuffix + rest[q:]
}

// mysqlServerDSN returns a DSN with no database selected (path "/") so CREATE
// DATABASE can run before either database exists. It preserves the query params.
func mysqlServerDSN(dsn string) string {
	slash := strings.LastIndex(dsn, "/")
	if slash < 0 {
		return dsn
	}
	rest := dsn[slash+1:]
	if q := strings.IndexByte(rest, '?'); q >= 0 {
		return dsn[:slash+1] + rest[q:]
	}
	return dsn[:slash+1]
}

// ensureMySQLDatabases makes sure both the velox (base) and ent (_ent)
// databases exist using CREATE DATABASE IF NOT EXISTS against a no-database
// connection. If the user lacks the privilege the create fails and the caller
// treats it as "skip" rather than a hard failure.
func ensureMySQLDatabases(ctx context.Context, dsn, baseDB string) error {
	srv, err := sql.Open("mysql", mysqlServerDSN(dsn))
	if err != nil {
		return err
	}
	defer func() { _ = srv.Close() }()
	if err := srv.PingContext(ctx); err != nil {
		return err
	}
	for _, db := range []string{baseDB, baseDB + mysqlEntSuffix} {
		if !isSafeIdent(db) {
			return fmt.Errorf("refusing to create database with unsafe name %q", db)
		}
		if _, err := srv.ExecContext(ctx,
			"CREATE DATABASE IF NOT EXISTS "+quoteMySQLIdent(db)); err != nil {
			return err
		}
	}
	return nil
}

// truncateMySQL empties every table in the database the given *sql.DB is
// connected to. FK checks are disabled around the truncate so circular FK
// graphs tear down in any order, then re-enabled. TRUNCATE resets AUTO_INCREMENT
// so ids restart at 1 each program, matching SQLite's fresh-client behavior.
func truncateMySQL(ctx context.Context, db *sql.DB) error {
	tables, err := listMySQLTables(ctx, db)
	if err != nil {
		return err
	}
	if len(tables) == 0 {
		return nil
	}
	if _, err := db.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 0"); err != nil {
		return err
	}
	defer func() { _, _ = db.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 1") }()
	for _, t := range tables {
		if _, err := db.ExecContext(ctx, "TRUNCATE TABLE "+quoteMySQLIdent(t)); err != nil {
			return err
		}
	}
	return nil
}

// listMySQLTables returns the base tables in the connection's current database.
func listMySQLTables(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx,
		"SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE() AND table_type = 'BASE TABLE'")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		tables = append(tables, name)
	}
	return tables, rows.Err()
}

// quoteMySQLIdent backtick-quotes a MySQL identifier, escaping embedded backticks.
func quoteMySQLIdent(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}
