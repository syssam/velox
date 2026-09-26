package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Inventory struct{ velox.Schema }

func (Inventory) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (Inventory) Fields() []velox.Field {
	return []velox.Field{
		field.String("name").NotEmpty().MaxLen(100).Annotations(graphql.OrderField("NAME")),
		field.String("url").MaxLen(500).Optional().Nillable().Annotations(graphql.OrderField("URL")),
		field.Float("score").Default(0).Optional().Nillable().Annotations(graphql.OrderField("SCORE")),
		field.Int("sort_order").Default(0).Annotations(graphql.OrderField("SORT_ORDER")),
		field.String("title").NotEmpty().MaxLen(200).Annotations(graphql.OrderField("TITLE")),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.Int("quantity").Default(0).Annotations(graphql.OrderField("QUANTITY")),
		field.Float("amount").Default(0).Annotations(graphql.OrderField("AMOUNT")),
	}
}

func (Inventory) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("permissions", Permission.Type).Annotations(graphql.RelayConnection()),
		edge.To("products", Product.Type).Annotations(graphql.RelayConnection()),
		edge.To("file_links", File.Type).Annotations(graphql.RelayConnection()),
	}
}

func (Inventory) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
