package schema

import (
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Order struct{ velox.Schema }

func (Order) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (Order) Fields() []velox.Field {
	return []velox.Field{
		field.Int("sort_order").Default(0).Annotations(graphql.OrderField("SORT_ORDER")),
		field.Text("description").Optional().Nillable(),
		field.Float("score").Default(0).Optional().Nillable().Annotations(graphql.OrderField("SCORE")),
		field.String("url").MaxLen(500).Optional().Nillable().Annotations(graphql.OrderField("URL")),
		field.Bool("active").Default(false),
		field.String("email").MaxLen(255).Annotations(graphql.OrderField("EMAIL")),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.Enum("priority").Values("low", "medium", "high").Default("medium").Annotations(graphql.OrderField("PRIORITY")),
	}
}

func (Order) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("storys", Story.Type).Annotations(graphql.RelayConnection()),
		edge.To("configs", AppConfig.Type).Annotations(graphql.RelayConnection()),
		edge.To("post_links", Post.Type).Annotations(graphql.RelayConnection()),
	}
}

func (Order) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
