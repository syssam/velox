package schema

import (
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Sprint struct{ velox.Schema }

func (Sprint) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (Sprint) Fields() []velox.Field {
	return []velox.Field{
		field.JSON("metadata", map[string]any{}).Optional(),
		field.Enum("status").Values("low", "medium", "high").Default("medium").Annotations(graphql.OrderField("STATUS")),
		field.Float("amount").Default(0).Annotations(graphql.OrderField("AMOUNT")),
		field.Enum("priority").Values("low", "medium", "high").Default("medium").Annotations(graphql.OrderField("PRIORITY")),
		field.Text("description").Optional().Nillable(),
		field.Bool("active").Default(false),
	}
}

func (Sprint) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("customers", Customer.Type).Annotations(graphql.RelayConnection()),
		edge.To("features", Feature.Type).Annotations(graphql.RelayConnection()),
		edge.To("plan_links", Plan.Type).Annotations(graphql.RelayConnection()),
	}
}

func (Sprint) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
