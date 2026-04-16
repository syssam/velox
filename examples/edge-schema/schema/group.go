package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

// Group contains zero or more Users through a Membership.
type Group struct {
	velox.Schema
}

func (Group) Fields() []velox.Field {
	return []velox.Field{
		field.String("name").Unique().NotEmpty(),
	}
}

// Edges of Group. The inverse side of the User.groups edge — also annotated
// with Through() so the Membership table is the single source of truth.
func (Group) Edges() []velox.Edge {
	return []velox.Edge{
		edge.From("users", User.Type).
			Ref("groups").
			Through("memberships", Membership.Type),
	}
}
