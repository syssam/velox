// Package dialect names the SQL dialects velox knows, as the old velox's package
// of the same name does, so that a schema writes
//
//	field.String("data").SchemaType(map[string]string{
//		dialect.Postgres: "jsonb",
//		dialect.MySQL:    "json",
//	})
//
// The names are the same strings as field.Postgres, field.MySQL and
// field.SQLite, which are these.
package dialect

// The dialects.
const (
	MySQL    = "mysql"
	Postgres = "postgres"
	SQLite   = "sqlite"
)
