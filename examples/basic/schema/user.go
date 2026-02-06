// Package schema defines the entity schemas for the basic example.
package schema

import (
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/mixin"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/index"
)

// User holds the schema definition for the User entity.
type User struct {
	velox.Schema
}

// Mixin of the User.
func (User) Mixin() []velox.Mixin {
	return []velox.Mixin{
		mixin.Time{}, // Adds created_at and updated_at
	}
}

// Fields of the User.
func (User) Fields() []velox.Field {
	return []velox.Field{
		field.String("name").
			NotEmpty().
			MaxLen(100),
		field.String("email").
			Unique().
			NotEmpty(),
		field.Int("age").
			Optional().
			Positive(),
		field.Time("birthday").
			Optional().
			Default(time.Now),
		field.Enum("role").
			Values("admin", "user", "guest").
			Default("user"),
	}
}

// Edges of the User.
func (User) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("posts", Post{}).
			Comment("Posts written by this user"),
		edge.To("groups", Group{}).
			Through("memberships", Membership{}),
	}
}

// Indexes of the User.
func (User) Indexes() []velox.Index {
	return []velox.Index{
		index.Fields("email").
			Unique(),
		index.Fields("role", "created_at"),
	}
}
