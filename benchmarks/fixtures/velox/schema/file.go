package schema

import (
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type File struct{ velox.Schema }

func (File) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (File) Fields() []velox.Field {
	return []velox.Field{
		field.Enum("priority").Values("low", "medium", "high").Default("medium").Annotations(graphql.OrderField("PRIORITY")),
		field.Enum("status").Values("low", "medium", "high").Default("medium").Annotations(graphql.OrderField("STATUS")),
		field.String("name").NotEmpty().MaxLen(100).Annotations(graphql.OrderField("NAME")),
		field.String("code").MaxLen(50).Annotations(graphql.OrderField("CODE")),
		field.String("email").MaxLen(255).Annotations(graphql.OrderField("EMAIL")),
		field.Int("sort_order").Default(0).Annotations(graphql.OrderField("SORT_ORDER")),
	}
}

func (File) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("categorys", Category.Type).Annotations(graphql.RelayConnection()),
		edge.To("comments", Comment.Type).Annotations(graphql.RelayConnection()),
		edge.To("apikey_links", ApiKey.Type).Annotations(graphql.RelayConnection()),
	}
}

func (File) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
