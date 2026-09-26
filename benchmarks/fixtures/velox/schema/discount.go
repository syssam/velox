package schema

import (
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Discount struct{ velox.Schema }

func (Discount) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (Discount) Fields() []velox.Field {
	return []velox.Field{
		field.String("email").MaxLen(255).Annotations(graphql.OrderField("EMAIL")),
		field.String("title").NotEmpty().MaxLen(200).Annotations(graphql.OrderField("TITLE")),
		field.Int("quantity").Default(0).Annotations(graphql.OrderField("QUANTITY")),
		field.String("code").MaxLen(50).Annotations(graphql.OrderField("CODE")),
		field.Enum("status").Values("low", "medium", "high").Default("medium").Annotations(graphql.OrderField("STATUS")),
		field.Enum("priority").Values("low", "medium", "high").Default("medium").Annotations(graphql.OrderField("PRIORITY")),
		field.String("name").NotEmpty().MaxLen(100).Annotations(graphql.OrderField("NAME")),
		field.Int("sort_order").Default(0).Annotations(graphql.OrderField("SORT_ORDER")),
	}
}

func (Discount) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("settings", Setting.Type).Annotations(graphql.RelayConnection()),
		edge.To("customers", Customer.Type).Annotations(graphql.RelayConnection()),
		edge.To("apikey_links", ApiKey.Type).Annotations(graphql.RelayConnection()),
	}
}

func (Discount) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
