package schema

import (
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Feature struct{ velox.Schema }

func (Feature) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (Feature) Fields() []velox.Field {
	return []velox.Field{
		field.Enum("priority").Values("low", "medium", "high").Default("medium").Annotations(graphql.OrderField("PRIORITY")),
		field.Float("score").Default(0).Optional().Nillable().Annotations(graphql.OrderField("SCORE")),
		field.String("name").NotEmpty().MaxLen(100).Annotations(graphql.OrderField("NAME")),
		field.String("title").NotEmpty().MaxLen(200).Annotations(graphql.OrderField("TITLE")),
		field.String("url").MaxLen(500).Optional().Nillable().Annotations(graphql.OrderField("URL")),
		field.Int("quantity").Default(0).Annotations(graphql.OrderField("QUANTITY")),
		field.JSON("metadata", map[string]any{}).Optional(),
	}
}

func (Feature) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("sessions", Session.Type).Annotations(graphql.RelayConnection()),
		edge.To("messages", Message.Type).Annotations(graphql.RelayConnection()),
		edge.To("tag_links", Tag.Type).Annotations(graphql.RelayConnection()),
	}
}

func (Feature) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
