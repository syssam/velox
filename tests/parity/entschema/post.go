package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

// Post holds the schema definition for the Post entity.
type Post struct{ ent.Schema }

// Mixin of the Post.
func (Post) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixin.Time{},
	}
}

// Fields of the Post.
func (Post) Fields() []ent.Field {
	return []ent.Field{
		field.String("title"),
		field.Enum("status").
			Values("draft", "published").
			Default("draft"),
		field.Int("view_count").
			Default(0).
			Annotations(entgql.OrderField("VIEW_COUNT")),
		field.Strings("labels").
			Optional(),
	}
}

// Edges of the Post.
func (Post) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("author", Author.Type).
			Ref("posts").
			Unique().
			Required(),
		edge.To("comments", Comment.Type),
		edge.To("tags", Tag.Type),
	}
}

// Annotations of the Post.
func (Post) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.QueryField(),
		entgql.RelayConnection(),
		entgql.MultiOrder(),
	}
}
