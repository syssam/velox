package entschema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// Post holds the schema definition for the Post entity.
type Post struct{ ent.Schema }

// Mixin of the Post.
func (Post) Mixin() []ent.Mixin {
	return []ent.Mixin{
		timeMixin{},
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
		// OnDelete(Cascade) on the assoc (O2M) edge — Ent reads the referential
		// action only from the assoc side (entc/gen/graph.go skips inverse edges
		// when building foreign keys), so the annotation must live here, not on
		// Comment's inverse edge. Deleting a Post cascade-deletes its Comments.
		edge.To("comments", Comment.Type).
			Annotations(entsql.OnDelete(entsql.Cascade)),
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
