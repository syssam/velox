// Package dialect names the SQL dialects velox knows, as the old velox's package
// of the same name does, so that a schema writes
//
//	field.String("data").SchemaType(map[string]string{
//		dialect.Postgres: "jsonb",
//		dialect.MySQL:    "json",
//	})
//
// The names are the strings velox.Dialect's String method returns. They are
// also the keys of sqlschema.DefaultExprs.
package dialect

// The dialects.
const (
	MySQL    = "mysql"
	Postgres = "postgres"
	SQLite   = "sqlite"
)
