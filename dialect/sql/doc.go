// Package sql provides SQL query building primitives and database dialect abstraction.
//
// This package is the foundation for generating and executing SQL queries across
// different database systems (PostgreSQL, MySQL, SQLite). It provides a fluent API
// for constructing type-safe SQL statements.
//
// # Builder Types
//
// The package provides specialized builders for different SQL operations:
//
//   - Builder: Low-level SQL string builder with identifier quoting
//   - Selector: SELECT query builder with joins, predicates, and pagination
//   - InsertBuilder: INSERT statement builder with RETURNING support
//   - UpdateBuilder: UPDATE statement builder with SET and WHERE clauses
//   - DeleteBuilder: DELETE statement builder with WHERE predicates
//
// # Dialect Support
//
// SQL generation adapts to different database dialects:
//
//	import "github.com/syssam/velox/dialect"
//
//	// PostgreSQL
//	b := sql.Dialect(dialect.Postgres)
//	b.Select("id", "name").From("users").Where(sql.EQ("status", "active"))
//
//	// MySQL
//	b := sql.Dialect(dialect.MySQL)
//
// # Predicates
//
// The package provides type-safe predicate functions:
//
//	// Equality
//	sql.EQ("name", "john")           // name = 'john'
//	sql.NEQ("status", "deleted")     // status <> 'deleted'
//
//	// Comparison
//	sql.GT("age", 18)                // age > 18
//	sql.LTE("price", 100.0)          // price <= 100.0
//
//	// String matching
//	sql.Contains("name", "john")     // name LIKE '%john%'
//	sql.HasPrefix("email", "admin")  // email LIKE 'admin%'
//
//	// NULL checks
//	sql.IsNull("deleted_at")         // deleted_at IS NULL
//	sql.NotNull("email")             // email IS NOT NULL
//
//	// IN clauses
//	sql.In("status", "active", "pending")  // status IN ('active', 'pending')
//
// # Joins
//
// Join operations are supported through the selector:
//
//	sql.Select("u.id", "u.name", "p.title").
//	    From(sql.Table("users").As("u")).
//	    Join(sql.Table("posts").As("p")).On("u.id", "p.user_id").
//	    Where(sql.EQ("u.status", "active"))
//
// # Pagination
//
// Both offset-based and cursor-based pagination are supported:
//
//	// Offset pagination
//	sql.Select("*").From("users").Offset(20).Limit(10)
//
//	// Cursor pagination (see pagination.go)
//	sql.Select("*").From("users").
//	    OrderBy("created_at", sql.OrderDesc).
//	    Cursor(lastCursor, 10)
//
// # Row-Level Locking
//
// Pessimistic locking for transactions:
//
//	sql.Select("*").From("users").
//	    Where(sql.EQ("id", 1)).
//	    ForUpdate()  // SELECT ... FOR UPDATE
//
// # Usage with Velox ORM
//
// This package is typically used internally by generated code, but can be
// used directly for custom queries:
//
//	client.User.Query().
//	    Where(func(s *sql.Selector) {
//	        s.Where(sql.GT("age", 18))
//	    })
package sql
