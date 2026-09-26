package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type User struct{ velox.Schema }

func (User) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (User) Fields() []velox.Field {
	return []velox.Field{
		field.String("code").MaxLen(50).Annotations(graphql.OrderField("CODE")),
		field.String("title").NotEmpty().MaxLen(200).Annotations(graphql.OrderField("TITLE")),
		field.Int("quantity").Default(0).Annotations(graphql.OrderField("QUANTITY")),
		field.Int("sort_order").Default(0).Annotations(graphql.OrderField("SORT_ORDER")),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.Bool("active").Default(false),
		field.String("email").MaxLen(255).Annotations(graphql.OrderField("EMAIL")),
	}
}

func (User) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("auditlogs", AuditLog.Type).Annotations(graphql.RelayConnection()),
		edge.To("orderitems", OrderItem.Type).Annotations(graphql.RelayConnection()),
		edge.To("comment_links", Comment.Type).Annotations(graphql.RelayConnection()),
	}
}

func (User) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
