// Package schema defines the User, Group, and Membership entities for the
// edge-schema example. The classic Django-style M2M-with-intermediate model:
//
//	User ⇆ Group via Membership(role, joined_at)
package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

// User belongs to zero or more Groups through a Membership.
type User struct {
	velox.Schema
}

func (User) Fields() []velox.Field {
	return []velox.Field{
		field.String("name").NotEmpty(),
		field.String("email").Unique().NotEmpty(),
	}
}

// Edges of User. The Through() call tells velox that this M2M relationship
// is backed by an explicit edge schema (Membership) that carries extra
// fields (role, joined_at). Without Through(), velox would auto-generate
// a minimal join table with no extra columns.
func (User) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("groups", Group.Type).
			Through("memberships", Membership.Type),
	}
}
