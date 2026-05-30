package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/dialect/sqlschema"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

// Post holds the schema definition for the Post entity.
type Post struct {
	velox.Schema
}

// Mixin of the Post.
func (Post) Mixin() []velox.Mixin {
	return []velox.Mixin{
		mixin.Time{},
	}
}

// Fields of the Post.
func (Post) Fields() []velox.Field {
	return []velox.Field{
		field.String("title"),
		field.Enum("status").
			Values("draft", "published").
			Default("draft"),
		field.Int("view_count").
			Default(0).
			Annotations(graphql.OrderField("VIEW_COUNT")),
		field.Strings("labels").
			Optional(),
	}
}

// Edges of the Post.
func (Post) Edges() []velox.Edge {
	return []velox.Edge{
		edge.From("author", Author.Type).
			Ref("posts").
			Unique().
			Required(),
		// OnDelete(Cascade) on the assoc (O2M) edge — mirrors Ent's canonical
		// placement (the referential action is read from the assoc side). Deleting
		// a Post cascade-deletes its Comments. This makes the migration's FK action
		// observable through behavior: if the generated migration emits the wrong
		// ON DELETE action, deleting a post that has a comment FK-fails instead of
		// cascading, surfacing as a VeloxBug in the differential harness — the
		// migration-DDL guard.
		edge.To("comments", Comment.Type).
			Annotations(sqlschema.OnDelete(sqlschema.Cascade)),
		edge.To("tags", Tag.Type),
	}
}

// Annotations of the Post.
func (Post) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.QueryField(),
		graphql.RelayConnection(),
		graphql.MultiOrder(),
	}
}
