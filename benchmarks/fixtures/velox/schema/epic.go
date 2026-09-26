package schema

import (
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Epic struct{ velox.Schema }

func (Epic) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (Epic) Fields() []velox.Field {
	return []velox.Field{
		field.Text("notes").Optional().Nillable(),
		field.Enum("priority").Values("low", "medium", "high").Default("medium").Annotations(graphql.OrderField("PRIORITY")),
		field.String("code").MaxLen(50).Annotations(graphql.OrderField("CODE")),
		field.String("url").MaxLen(500).Optional().Nillable().Annotations(graphql.OrderField("URL")),
		field.String("email").MaxLen(255).Annotations(graphql.OrderField("EMAIL")),
		field.String("name").NotEmpty().MaxLen(100).Annotations(graphql.OrderField("NAME")),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.Float("score").Default(0).Optional().Nillable().Annotations(graphql.OrderField("SCORE")),
	}
}

func (Epic) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("warehouses", Warehouse.Type).Annotations(graphql.RelayConnection()),
		edge.To("customers", Customer.Type).Annotations(graphql.RelayConnection()),
		edge.To("task_links", Task.Type).Annotations(graphql.RelayConnection()),
	}
}

func (Epic) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
