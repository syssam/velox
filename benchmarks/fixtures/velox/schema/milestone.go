package schema

import (
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Milestone struct{ velox.Schema }

func (Milestone) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (Milestone) Fields() []velox.Field {
	return []velox.Field{
		field.String("email").MaxLen(255).Annotations(graphql.OrderField("EMAIL")),
		field.Enum("status").Values("low", "medium", "high").Default("medium").Annotations(graphql.OrderField("STATUS")),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.Enum("priority").Values("low", "medium", "high").Default("medium").Annotations(graphql.OrderField("PRIORITY")),
		field.Text("description").Optional().Nillable(),
		field.Float("amount").Default(0).Annotations(graphql.OrderField("AMOUNT")),
	}
}

func (Milestone) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("categorys", Category.Type).Annotations(graphql.RelayConnection()),
		edge.To("configs", AppConfig.Type).Annotations(graphql.RelayConnection()),
		edge.To("auditlog_links", AuditLog.Type).Annotations(graphql.RelayConnection()),
	}
}

func (Milestone) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
