//go:build !hidegroups

package buildflags

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/field"
)

// Group holds the schema definition for the Group entity.
type Group struct {
	velox.Schema
}

// Fields of the Group.
func (Group) Fields() []velox.Field {
	return []velox.Field{
		field.String("name"),
	}
}
