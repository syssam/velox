// Package entschema defines the Ent parity schema (mirror of the velox schema).
package entschema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// Author holds the schema definition for the Author entity.
type Author struct{ ent.Schema }

// Mixin of the Author.
func (Author) Mixin() []ent.Mixin {
	return []ent.Mixin{
		timeMixin{},
	}
}

// Fields of the Author.
func (Author) Fields() []ent.Field {
	return []ent.Field{
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
func (Author) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("posts", Post.Type),
		edge.To("comments", Comment.Type),
	}
}
