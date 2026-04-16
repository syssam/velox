package sql_test

import (
	"fmt"

	"github.com/syssam/velox/dialect"
	sql "github.com/syssam/velox/dialect/sql"
)

// ExampleSelect demonstrates building a SELECT query with WHERE and ORDER BY.
func ExampleSelect() {
	query, args := sql.Select("id", "name", "age").
		From(sql.Table("users")).
		Where(sql.EQ("status", "active")).
		OrderBy("name").
		Query()
	fmt.Println(query)
	fmt.Println(args)
	// Output:
	// SELECT `id`, `name`, `age` FROM `users` WHERE `status` = ? ORDER BY `name`
	// [active]
}

// ExampleInsert demonstrates building an INSERT query with columns and values.
func ExampleInsert() {
	query, args := sql.Insert("users").
		Columns("name", "age").
		Values("Alice", 30).
		Query()
	fmt.Println(query)
	fmt.Println(args)
	// Output:
	// INSERT INTO `users` (`name`, `age`) VALUES (?, ?)
	// [Alice 30]
}

// ExampleUpdate demonstrates building an UPDATE query with SET and WHERE.
func ExampleUpdate() {
	query, args := sql.Update("users").
		Set("name", "Bob").
		Set("age", 25).
		Where(sql.EQ("id", 1)).
		Query()
	fmt.Println(query)
	fmt.Println(args)
	// Output:
	// UPDATE `users` SET `name` = ?, `age` = ? WHERE `id` = ?
	// [Bob 25 1]
}

// ExampleDelete demonstrates building a DELETE query with WHERE.
func ExampleDelete() {
	query, args := sql.Delete("users").
		Where(sql.EQ("name", "Alice")).
		Query()
	fmt.Println(query)
	fmt.Println(args)
	// Output:
	// DELETE FROM `users` WHERE `name` = ?
	// [Alice]
}

// ExampleDialect demonstrates using the PostgreSQL dialect, which uses
// double-quote identifiers and numbered placeholders ($1, $2, ...).
func ExampleDialect() {
	query, args := sql.Dialect(dialect.Postgres).
		Insert("users").
		Columns("name", "age").
		Values("Carol", 28).
		Query()
	fmt.Println(query)
	fmt.Println(args)
	// Output:
	// INSERT INTO "users" ("name", "age") VALUES ($1, $2)
	// [Carol 28]
}
