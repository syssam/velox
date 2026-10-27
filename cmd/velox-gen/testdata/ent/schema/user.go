// Package schema declares the tables of postgres.sql and mysql.sql as velox
// schemas: the ent fixture of velox-gen migrate baseline.
package schema

import (
	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/edge"
	"github.com/syssam/velox2/schema/field"
	"github.com/syssam/velox2/schema/index"
)

// User is ent's users table.
type User struct{ schema.Schema }

// Fields returns the columns after the ID.
func (User) Fields() []schema.Field {
	return []schema.Field{
		field.String("name").MaxLen(255),
		field.String("email").MaxLen(255).Unique(),
		field.Enum("role").Values("admin", "member"),
	}
}

// Indexes returns ent's user_name, and user_email beside the field's own
// unique index.
func (User) Indexes() []schema.Index {
	return []schema.Index{index.Fields("name"), index.Fields("email").Unique()}
}

// Edges returns Posts, whose key is ent's user_posts column, and a second
// edge to posts, whose foreign key's 66-byte name ent caps.
func (User) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("posts", Post.Type).StorageKey(edge.Column("user_posts")),
		edge.To("drafts_written_but_never_published_by_this_user_so_far", Post.Type),
	}
}
