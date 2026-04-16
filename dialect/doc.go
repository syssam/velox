// Package dialect provides database dialect abstraction for Velox ORM.
//
// This package defines the interfaces and types used for database-specific
// operations, allowing Velox to support multiple database backends including
// PostgreSQL, MySQL, and SQLite.
//
// # Supported Dialects
//
// The following dialects are supported:
//
//   - Postgres: PostgreSQL database
//   - MySQL: MySQL/MariaDB database
//   - SQLite: SQLite database
//
// # Dialect Constants
//
// Each dialect is identified by a constant string:
//
//	dialect.Postgres = "postgres"
//	dialect.MySQL    = "mysql"
//	dialect.SQLite   = "sqlite"
//
// # Driver Interface
//
// The package defines the Driver interface for database operations:
//
//	type Driver interface {
//	    Exec(ctx context.Context, query string, args, v any) error
//	    Query(ctx context.Context, query string, args, v any) error
//	    Tx(ctx context.Context) (Tx, error)
//	    Close() error
//	    Dialect() string
//	}
//
// # Transaction Interface
//
// The Tx interface extends Driver with transaction methods:
//
//	type Tx interface {
//	    Driver
//	    Commit() error
//	    Rollback() error
//	}
//
// # ExecQuerier Interface
//
// The ExecQuerier interface is implemented by both Driver and Tx:
//
//	type ExecQuerier interface {
//	    Exec(ctx context.Context, query string, args, v any) error
//	    Query(ctx context.Context, query string, args, v any) error
//	}
//
// # Usage
//
// Opening a database connection:
//
//	import (
//	    "github.com/syssam/velox/dialect"
//	    "github.com/syssam/velox/dialect/sql"
//	)
//
//	db, err := sql.Open(dialect.Postgres, "postgres://...")
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer db.Close()
//
// Using with Velox client:
//
//	client := ent.NewClient(ent.Driver(db))
//
// # Sub-packages
//
// The dialect package contains several sub-packages:
//
//   - dialect/sql: SQL query builders and driver implementation
//   - dialect/sql/schema: Schema introspection and migration
//   - dialect/sql/sqlgraph: Graph traversal for eager loading
//   - dialect/sql/sqljson: JSON field operations
package dialect
