//go:build ignore

package schema

import (
	"regexp"

	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/index"
	"github.com/syssam/velox/schema/mixin"
)

type User struct{ schema.Schema }

func (User) Mixin() []schema.Mixin { return []schema.Mixin{mixin.Time{}, Audited{}} }

func (User) Fields() []schema.Field {
	return []schema.Field{
		field.Int64("org_id").Immutable(),
		field.String("name").NotEmpty().MaxLen(50).Comment("The display name."),
		field.String("email").Unique().Match(regexp.MustCompile(`^[^@]+@[^@]+$`)),
		field.Enum("role").Values("admin", "editor", "viewer").Default("viewer"),
		field.String("nickname").Optional().Default(""),
		field.Text("bio").Nillable(),
		field.Int("age").Optional().Default(0).Range(0, 150),
		field.String("password_hash").Sensitive(),
		field.Float("score").Nillable().Default(1.5),
	}
}

func (User) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("posts", Post.Type),
		edge.To("groups", Group.Type),
		edge.To("friends", User.Type),
		edge.To("spouse", User.Type).Unique(),
	}
}

func (User) Indexes() []schema.Index {
	return []schema.Index{index.Fields("org_id", "name").Unique()}
}

func (User) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.Comment("User is a person who writes posts.")}
}
