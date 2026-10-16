//go:build ignore

package schema

import (
	"regexp"

	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/edge"
	"github.com/syssam/velox2/schema/field"
	"github.com/syssam/velox2/schema/mixin"
)

type User struct{ schema.Schema }

func (User) Mixin() []schema.Mixin { return []schema.Mixin{mixin.Time{}, Audited{}} }

func (User) Fields() []schema.Field {
	return []schema.Field{
		field.String("name").NotEmpty().MaxLen(20),
		field.String("email").Unique().Match(regexp.MustCompile(`^[^@]+@[^@]+$`)),
		field.Enum("role").Values("admin", "editor", "viewer").Default("viewer"),
		field.String("nickname").Optional(),
		field.Int("age").Optional().Range(0, 150),
		field.String("password_hash").Sensitive(),
		field.Float("score").Default(1.5),
	}
}

func (User) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("posts", Post.Type),
		edge.To("groups", Group.Type),
	}
}
