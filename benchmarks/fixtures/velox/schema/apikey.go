package schema

import (
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type ApiKey struct{ velox.Schema }

func (ApiKey) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (ApiKey) Fields() []velox.Field {
	return []velox.Field{
		field.Float("score").Default(0).Optional().Nillable().Annotations(graphql.OrderField("SCORE")),
		field.Text("notes").Optional().Nillable(),
		field.Int("sort_order").Default(0).Annotations(graphql.OrderField("SORT_ORDER")),
		field.Enum("status").Values("low", "medium", "high").Default("medium").Annotations(graphql.OrderField("STATUS")),
		field.Enum("priority").Values("low", "medium", "high").Default("medium").Annotations(graphql.OrderField("PRIORITY")),
		field.Text("description").Optional().Nillable(),
		field.String("title").NotEmpty().MaxLen(200).Annotations(graphql.OrderField("TITLE")),
		field.Int("quantity").Default(0).Annotations(graphql.OrderField("QUANTITY")),
	}
}

func (ApiKey) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("features", Feature.Type).Annotations(graphql.RelayConnection()),
		edge.To("bugs", Bug.Type).Annotations(graphql.RelayConnection()),
		edge.To("inventory_links", Inventory.Type).Annotations(graphql.RelayConnection()),
	}
}

func (ApiKey) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
