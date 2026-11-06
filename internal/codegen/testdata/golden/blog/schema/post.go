//go:build ignore

package schema

import (
	"errors"

	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

type Post struct{ schema.Schema }

func (Post) Fields() []schema.Field {
	return []schema.Field{
		field.Int64("author_id"),
		field.String("title").NotEmpty(),
		field.String("slug").Validate(func(s string) error {
			if len(s) > 0 && s[0] == '-' {
				return errors.New("must not start with a dash")
			}
			return nil
		}),
		field.Enum("status").Values("draft", "published").Default("draft"),
		field.Time("published_at").Optional().Nillable(),
		field.Strings("tags").Default([]string{"new"}),
		field.Int("views").Min(0),
	}
}

func (Post) Edges() []schema.Edge {
	return []schema.Edge{
		edge.From("author", User.Type).Ref("posts").Unique().Required().Field("author_id").
			Comment("Author is the user who wrote the post."),
	}
}
