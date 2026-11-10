//go:build ignore

package schema

import (
	"regexp"

	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type User struct{ schema.Schema }

func (User) Mixin() []schema.Mixin { return []schema.Mixin{mixin.Time{}, Audited{}} }

func (User) Fields() []schema.Field {
	return []schema.Field{
		field.String("name").NotEmpty().MaxLen(20),
		field.String("email").Unique().Match(regexp.MustCompile(`^[^@]+@[^@]+$`)),
		field.Enum("role").Values("admin", "editor", "viewer").Default("viewer"),
		field.String("nickname").Optional().Default(""),
		field.Int("age").Optional().Default(0).Range(1, 150),
		// Optional with validators: unset is "", which the validators let pass.
		field.String("motto").Optional().Default("").NotEmpty().MaxLen(10),
		field.String("password_hash").Sensitive(),
		field.Float("score").Nillable().Default(1.5),
	}
}

func (User) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("posts", Post.Type),
		edge.To("groups", Group.Type),
	}
}
