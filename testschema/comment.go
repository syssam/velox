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

// Comment holds the schema definition for the Comment entity.
type Comment struct {
	velox.Schema
}

// Mixin of the Comment.
func (Comment) Mixin() []velox.Mixin {
	return []velox.Mixin{
		mixin.Time{},
	}
}

// Fields of the Comment.
func (Comment) Fields() []velox.Field {
	return []velox.Field{
		field.Text("content").
			NotEmpty(),
	}
}

// Annotations of the Comment (repro of multi-order edge-target gap).
func (Comment) Annotations() []schema.Annotation {
	return []schema.Annotation{graphql.MultiOrder()}
}

// Edges of the Comment.
func (Comment) Edges() []velox.Edge {
	return []velox.Edge{
		// OnDelete(Cascade): deleting a Post cascade-deletes its Comments. Also
		// the integration suite's guard that an explicit OnDelete annotation
		// generates compiling code (renders schema.Cascade, not schema.CASCADE).
		edge.From("post", Post.Type).
			Ref("comments").
			Unique().
			Required().
			Annotations(sqlschema.OnDelete(sqlschema.Cascade)),
		edge.From("author", User.Type).
			Ref("comments").
			Unique().
			Required(),
	}
}
