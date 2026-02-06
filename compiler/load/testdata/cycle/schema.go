package cycle

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/compiler/load/testdata/cycle/fakevelox"
	"github.com/syssam/velox/schema/field"
)

// Enum is a custom type that creates a cycle.
type Enum = fakevelox.Enum

// Used is another custom type that creates a cycle.
type Used = fakevelox.Used

// User holds the schema definition for the User entity.
type User struct {
	velox.Schema
}

// Fields of the User.
// Uses Enum and Used types which create an import cycle.
func (User) Fields() []velox.Field {
	var _ Enum // Reference Enum type
	var _ Used // Reference Used type
	return []velox.Field{
		field.String("name"),
	}
}
