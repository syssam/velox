package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type Bug struct{ ent.Schema }

func (Bug) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (Bug) Fields() []ent.Field {
	return []ent.Field{
		field.String("code").MaxLen(50).Annotations(entgql.OrderField("CODE")),
		field.Enum("status").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("STATUS")),
		field.Text("notes").Optional().Nillable(),
		field.Float("amount").Default(0).Annotations(entgql.OrderField("AMOUNT")),
		field.Float("score").Default(0).Optional().Nillable().Annotations(entgql.OrderField("SCORE")),
		field.Enum("priority").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("PRIORITY")),
		field.String("email").MaxLen(255).Annotations(entgql.OrderField("EMAIL")),
	}
}

func (Bug) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("tokens", Token.Type).Annotations(entgql.RelayConnection()),
		edge.To("releases", Release.Type).Annotations(entgql.RelayConnection()),
		edge.To("invoiceitem_links", InvoiceItem.Type).Annotations(entgql.RelayConnection()),
	}
}

func (Bug) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
