package buildflags

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/field"
)

// User holds the schema definition for the User entity.
type User struct {
	velox.Schema
}

// Fields of the User.
func (User) Fields() []velox.Field {
	return []velox.Field{
		field.String("name"),
	}
}
