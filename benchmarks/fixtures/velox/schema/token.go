package schema

import (
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Token struct{ velox.Schema }

func (Token) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (Token) Fields() []velox.Field {
	return []velox.Field{
		field.Float("score").Default(0).Optional().Nillable().Annotations(graphql.OrderField("SCORE")),
		field.Int("sort_order").Default(0).Annotations(graphql.OrderField("SORT_ORDER")),
		field.Enum("status").Values("low", "medium", "high").Default("medium").Annotations(graphql.OrderField("STATUS")),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.String("name").NotEmpty().MaxLen(100).Annotations(graphql.OrderField("NAME")),
		field.Text("notes").Optional().Nillable(),
		field.Int("quantity").Default(0).Annotations(graphql.OrderField("QUANTITY")),
	}
}

func (Token) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("environments", Environment.Type).Annotations(graphql.RelayConnection()),
		edge.To("permissions", Permission.Type).Annotations(graphql.RelayConnection()),
		edge.To("file_links", File.Type).Annotations(graphql.RelayConnection()),
	}
}

func (Token) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
