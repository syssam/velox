package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Environment struct{ velox.Schema }

func (Environment) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (Environment) Fields() []velox.Field {
	return []velox.Field{
		field.Text("notes").Optional().Nillable(),
		field.Bool("active").Default(false),
		field.String("email").MaxLen(255).Annotations(graphql.OrderField("EMAIL")),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.Int("quantity").Default(0).Annotations(graphql.OrderField("QUANTITY")),
		field.Int("sort_order").Default(0).Annotations(graphql.OrderField("SORT_ORDER")),
	}
}

func (Environment) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("permissions", Permission.Type).Annotations(graphql.RelayConnection()),
		edge.To("shipments", Shipment.Type).Annotations(graphql.RelayConnection()),
		edge.To("category_links", Category.Type).Annotations(graphql.RelayConnection()),
	}
}

func (Environment) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
