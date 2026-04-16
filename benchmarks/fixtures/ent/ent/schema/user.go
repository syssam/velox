package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type User struct{ ent.Schema }

func (User) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (User) Fields() []ent.Field {
	return []ent.Field{
		field.String("code").MaxLen(50).Annotations(entgql.OrderField("CODE")),
		field.String("title").NotEmpty().MaxLen(200).Annotations(entgql.OrderField("TITLE")),
		field.Int("quantity").Default(0).Annotations(entgql.OrderField("QUANTITY")),
		field.Int("sort_order").Default(0).Annotations(entgql.OrderField("SORT_ORDER")),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.Bool("active").Default(false),
		field.String("email").MaxLen(255).Annotations(entgql.OrderField("EMAIL")),
	}
}

func (User) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("auditlogs", AuditLog.Type).Annotations(entgql.RelayConnection()),
		edge.To("orderitems", OrderItem.Type).Annotations(entgql.RelayConnection()),
		edge.To("comment_links", Comment.Type).Annotations(entgql.RelayConnection()),
	}
}

func (User) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
