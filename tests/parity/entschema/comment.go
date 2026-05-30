package entschema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// Comment holds the schema definition for the Comment entity.
type Comment struct{ ent.Schema }

// Mixin of the Comment.
func (Comment) Mixin() []ent.Mixin {
	return []ent.Mixin{
		timeMixin{},
	}
}

// Fields of the Comment.
func (Comment) Fields() []ent.Field {
	return []ent.Field{
		field.Text("content"),
		field.Strings("labels").
			Optional(),
	}
}

// Edges of the Comment.
func (Comment) Edges() []ent.Edge {
	return []ent.Edge{
		// The post->comments cascade is declared on the assoc edge in Post (Ent
		// reads OnDelete only from the assoc side). The inverse edge here just
		// names the back-reference.
		edge.From("post", Post.Type).
			Ref("comments").
			Unique().
			Required(),
		edge.From("author", Author.Type).
			Ref("comments").
			Unique().
			Required(),
	}
}
