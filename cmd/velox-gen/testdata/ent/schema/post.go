package schema

import (
	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/edge"
	"github.com/syssam/velox2/schema/field"
)

// Post is ent's posts table.
type Post struct{ schema.Schema }

// Fields returns the column after the ID.
func (Post) Fields() []schema.Field {
	return []schema.Field{field.String("title").MaxLen(255)}
}

// Edges returns Author, the inverse of User.Posts.
func (Post) Edges() []schema.Edge {
	return []schema.Edge{edge.From("author", User.Type).Ref("posts").Unique()}
}
