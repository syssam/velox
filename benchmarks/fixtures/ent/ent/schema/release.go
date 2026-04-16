package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type Release struct{ ent.Schema }

func (Release) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (Release) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").NotEmpty().MaxLen(100).Annotations(entgql.OrderField("NAME")),
		field.Float("amount").Default(0).Annotations(entgql.OrderField("AMOUNT")),
		field.Int("sort_order").Default(0).Annotations(entgql.OrderField("SORT_ORDER")),
		field.Text("description").Optional().Nillable(),
		field.String("url").MaxLen(500).Optional().Nillable().Annotations(entgql.OrderField("URL")),
		field.Enum("priority").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("PRIORITY")),
	}
}

func (Release) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("invoiceitems", InvoiceItem.Type).Annotations(entgql.RelayConnection()),
		edge.To("comments", Comment.Type).Annotations(entgql.RelayConnection()),
		edge.To("message_links", Message.Type).Annotations(entgql.RelayConnection()),
	}
}

func (Release) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
