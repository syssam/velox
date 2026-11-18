// Package veloxschema declares the relations example's entities: an Author has many
// Posts, a Post belongs to its Author and has many Comments, and Posts and Tags
// are many-to-many, through the join table post_tags. go tool
// velox-gen reads it and writes the veloxgen package beside it.
package veloxschema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

// Author writes posts.
type Author struct{ velox.Schema }

// Fields returns the column after the ID.
func (Author) Fields() []velox.Field { return []velox.Field{field.String("name")} }

// Edges returns Posts, which Post.Author declares the other side of. An edge
// declared with To has no column of its own: the key is Post's author_id.
func (Author) Edges() []velox.Edge { return []velox.Edge{edge.To("posts", Post.Type)} }

// Post belongs to an Author and has Comments.
type Post struct{ velox.Schema }

// Fields returns the columns after the ID. AuthorID is the foreign key of the
// Author edge.
func (Post) Fields() []velox.Field {
	return []velox.Field{
		field.Int64("author_id"),
		field.String("title"),
		field.Int64("votes"),
	}
}

// Edges returns Author, the inverse of Author.Posts, Comments and Tags. Unique
// makes Author a belongs-to (a velox.One), Required makes its key NOT NULL,
// Field names the column that holds the key, and OnDelete(edge.Cascade) says
// the database deletes an Author's posts with it. Tags, a To edge whose inverse
// is not Unique either, is many-to-many: velox-gen writes the join entity
// PostTag, a row of post_tags, whose primary key is (post_id, tag_id).
func (Post) Edges() []velox.Edge {
	return []velox.Edge{
		edge.From("author", Author.Type).Ref("posts").Unique().Required().Field("author_id").OnDelete(edge.Cascade),
		edge.To("comments", Comment.Type),
		edge.To("tags", Tag.Type),
	}
}

// Tag labels posts.
type Tag struct{ velox.Schema }

// Fields returns the column after the ID.
func (Tag) Fields() []velox.Field { return []velox.Field{field.String("name").Unique()} }

// Edges returns Posts, the inverse of Post.Tags.
func (Tag) Edges() []velox.Edge { return []velox.Edge{edge.From("posts", Post.Type).Ref("tags")} }

// Comment is a reply to a Post.
type Comment struct{ velox.Schema }

// Fields returns the columns after the ID.
func (Comment) Fields() []velox.Field {
	return []velox.Field{
		field.Int64("post_id"),
		field.String("body"),
	}
}

// Edges returns Post, the inverse of Post.Comments, which post_id holds the key
// of.
func (Comment) Edges() []velox.Edge {
	return []velox.Edge{
		edge.From("post", Post.Type).Ref("comments").Unique().Required().Field("post_id"),
	}
}
