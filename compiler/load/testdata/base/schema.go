package base

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/field"
)

// BaseFields returns common base fields.
// This is a helper function, not a schema type.
func BaseFields() []velox.Field {
	return []velox.Field{
		field.Int("base_field"),
	}
}

// User holds the schema definition for the User entity.
type User struct {
	velox.Schema
}

// Fields of the User.
func (User) Fields() []velox.Field {
	return append(
		BaseFields(),
		field.String("user_field"),
	)
}
