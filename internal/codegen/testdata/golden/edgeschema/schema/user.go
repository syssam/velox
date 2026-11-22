//go:build ignore

package schema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

// User is in groups through Membership, and follows users through Follow.
type User struct{ schema.Schema }

func (User) Fields() []schema.Field { return []schema.Field{field.String("name")} }

func (User) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("groups", Group.Type).Through("memberships", Membership.Type),
		edge.To("following", User.Type).Through("follows", Follow.Type).From("followers"),
	}
}
