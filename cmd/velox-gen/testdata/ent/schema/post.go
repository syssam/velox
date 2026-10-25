package schema

import (
	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/edge"
	"github.com/syssam/velox2/schema/field"
	"github.com/syssam/velox2/schema/index"
)

// Post is ent's posts table.
type Post struct{ schema.Schema }

// Fields returns the columns after the ID.
func (Post) Fields() []schema.Field {
	return []schema.Field{
		field.String("title").MaxLen(255),
		field.Int64("reviewed_by_an_editor_before_the_post_was_published_on").Optional().Nillable(),
	}
}

// Indexes returns ent's index on both, whose 65-byte name ent caps.
func (Post) Indexes() []schema.Index {
	return []schema.Index{index.Fields("title", "reviewed_by_an_editor_before_the_post_was_published_on")}
}

// Edges returns Author, the inverse of User.Posts.
func (Post) Edges() []schema.Edge {
	return []schema.Edge{edge.From("author", User.Type).Ref("posts").Unique()}
}
