package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type AuditLog struct{ velox.Schema }

func (AuditLog) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (AuditLog) Fields() []velox.Field {
	return []velox.Field{
		field.Int("sort_order").Default(0).Annotations(graphql.OrderField("SORT_ORDER")),
		field.String("email").MaxLen(255).Annotations(graphql.OrderField("EMAIL")),
		field.Int("quantity").Default(0).Annotations(graphql.OrderField("QUANTITY")),
		field.String("code").MaxLen(50).Annotations(graphql.OrderField("CODE")),
		field.Text("description").Optional().Nillable(),
		field.Enum("status").Values("low", "medium", "high").Default("medium").Annotations(graphql.OrderField("STATUS")),
		field.String("url").MaxLen(500).Optional().Nillable().Annotations(graphql.OrderField("URL")),
	}
}

func (AuditLog) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("tasks", Task.Type).Annotations(graphql.RelayConnection()),
		edge.To("reviews", Review.Type).Annotations(graphql.RelayConnection()),
		edge.To("inventory_links", Inventory.Type).Annotations(graphql.RelayConnection()),
	}
}

func (AuditLog) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
