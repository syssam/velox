package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/mixin"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

// Group holds the schema definition for the Group entity.
type Group struct {
	velox.Schema
}

// Mixin of the Group.
func (Group) Mixin() []velox.Mixin {
	return []velox.Mixin{
		mixin.Time{},
	}
}

// Fields of the Group.
func (Group) Fields() []velox.Field {
	return []velox.Field{
		field.String("name").
			Unique().
			NotEmpty(),
		field.String("description").
			Optional(),
	}
}

// Edges of the Group.
func (Group) Edges() []velox.Edge {
	return []velox.Edge{
		edge.From("members", User.Type).
			Ref("groups").
			Through("memberships", Membership.Type),
	}
}
