package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Customer struct{ velox.Schema }

func (Customer) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (Customer) Fields() []velox.Field {
	return []velox.Field{
		field.Float("amount").Default(0).Annotations(graphql.OrderField("AMOUNT")),
		field.String("name").NotEmpty().MaxLen(100).Annotations(graphql.OrderField("NAME")),
		field.String("code").MaxLen(50).Annotations(graphql.OrderField("CODE")),
		field.String("email").MaxLen(255).Annotations(graphql.OrderField("EMAIL")),
		field.Enum("status").Values("low", "medium", "high").Default("medium").Annotations(graphql.OrderField("STATUS")),
		field.Int("sort_order").Default(0).Annotations(graphql.OrderField("SORT_ORDER")),
	}
}

func (Customer) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("teams", Team.Type).Annotations(graphql.RelayConnection()),
		edge.To("warehouses", Warehouse.Type).Annotations(graphql.RelayConnection()),
		edge.To("task_links", Task.Type).Annotations(graphql.RelayConnection()),
	}
}

func (Customer) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
