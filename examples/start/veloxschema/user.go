// Package veloxschema declares the start example's one entity. go tool velox-gen
// reads it and writes the veloxgen package beside it.
package veloxschema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/field"
)

// User is a schema: a struct that embeds velox.Schema. Its table is "users",
// the snake_case plural of its name, and its ID is an int64 primary key that
// the schema does not need to declare.
type User struct{ velox.Schema }

// Fields returns the columns after the ID, in order. A field is not null
// unless it says Optional.
func (User) Fields() []velox.Field {
	return []velox.Field{
		field.String("name"),
		field.String("email").Unique(), // no two users share an email
		field.Int64("age"),
	}
}
