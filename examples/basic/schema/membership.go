package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/mixin"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

// Membership holds the schema definition for the Membership entity.
// This is a join table for the M2M relationship between User and Group.
type Membership struct {
	velox.Schema
}

// Mixin of the Membership.
func (Membership) Mixin() []velox.Mixin {
	return []velox.Mixin{
		mixin.Time{},
	}
}

// Fields of the Membership.
func (Membership) Fields() []velox.Field {
	return []velox.Field{
		field.Enum("role").
			Values("member", "admin", "owner").
			Default("member"),
	}
}

// Edges of the Membership.
func (Membership) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("user", User{}).
			Unique().
			Required(),
		edge.To("group", Group{}).
			Unique().
			Required(),
	}
}
