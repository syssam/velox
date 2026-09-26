package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Plan struct{ velox.Schema }

func (Plan) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (Plan) Fields() []velox.Field {
	return []velox.Field{
		field.Float("score").Default(0).Optional().Nillable().Annotations(graphql.OrderField("SCORE")),
		field.String("email").MaxLen(255).Annotations(graphql.OrderField("EMAIL")),
		field.Float("amount").Default(0).Annotations(graphql.OrderField("AMOUNT")),
		field.String("name").NotEmpty().MaxLen(100).Annotations(graphql.OrderField("NAME")),
		field.String("title").NotEmpty().MaxLen(200).Annotations(graphql.OrderField("TITLE")),
		field.Enum("priority").Values("low", "medium", "high").Default("medium").Annotations(graphql.OrderField("PRIORITY")),
		field.Text("description").Optional().Nillable(),
		field.String("code").MaxLen(50).Annotations(graphql.OrderField("CODE")),
	}
}

func (Plan) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("sprints", Sprint.Type).Annotations(graphql.RelayConnection()),
		edge.To("labels", Label.Type).Annotations(graphql.RelayConnection()),
		edge.To("discount_links", Discount.Type).Annotations(graphql.RelayConnection()),
	}
}

func (Plan) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
