// Package schema defines the velox parity schema (Author/Post/Tag/Comment).
package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

// Author holds the schema definition for the Author entity.
type Author struct {
	velox.Schema
}

// Mixin of the Author.
func (Author) Mixin() []velox.Mixin {
	return []velox.Mixin{
		mixin.Time{},
	}
}

// Fields of the Author.
func (Author) Fields() []velox.Field {
	return []velox.Field{
		field.String("name"),
		field.Int("age").
			Default(0),
		field.Enum("role").
			Values("user", "admin", "guest").
			Default("user"),
		field.String("bio").
			Optional().
			Nillable(),
		field.Strings("labels").
			Optional(),
	}
}

// Edges of the Author.
func (Author) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("posts", Post.Type),
		edge.To("comments", Comment.Type),
	}
}
