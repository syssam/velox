package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Label struct{ velox.Schema }

func (Label) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (Label) Fields() []velox.Field {
	return []velox.Field{
		field.Float("amount").Default(0).Annotations(graphql.OrderField("AMOUNT")),
		field.Bool("active").Default(false),
		field.String("name").NotEmpty().MaxLen(100).Annotations(graphql.OrderField("NAME")),
		field.String("title").NotEmpty().MaxLen(200).Annotations(graphql.OrderField("TITLE")),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.Enum("priority").Values("low", "medium", "high").Default("medium").Annotations(graphql.OrderField("PRIORITY")),
		field.Enum("status").Values("low", "medium", "high").Default("medium").Annotations(graphql.OrderField("STATUS")),
		field.Int("sort_order").Default(0).Annotations(graphql.OrderField("SORT_ORDER")),
	}
}

func (Label) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("projects", Project.Type).Annotations(graphql.RelayConnection()),
		edge.To("comments", Comment.Type).Annotations(graphql.RelayConnection()),
		edge.To("post_links", Post.Type).Annotations(graphql.RelayConnection()),
	}
}

func (Label) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
