package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type Folder struct{ ent.Schema }

func (Folder) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (Folder) Fields() []ent.Field {
	return []ent.Field{
		field.Float("amount").Default(0).Annotations(entgql.OrderField("AMOUNT")),
		field.String("name").NotEmpty().MaxLen(100).Annotations(entgql.OrderField("NAME")),
		field.Float("score").Default(0).Optional().Nillable().Annotations(entgql.OrderField("SCORE")),
		field.String("code").MaxLen(50).Annotations(entgql.OrderField("CODE")),
		field.Bool("active").Default(false),
		field.String("email").MaxLen(255).Annotations(entgql.OrderField("EMAIL")),
		field.Text("notes").Optional().Nillable(),
		field.Text("description").Optional().Nillable(),
	}
}

func (Folder) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("invoiceitems", InvoiceItem.Type).Annotations(entgql.RelayConnection()),
		edge.To("shipmentitems", ShipmentItem.Type).Annotations(entgql.RelayConnection()),
		edge.To("label_links", Label.Type).Annotations(entgql.RelayConnection()),
	}
}

func (Folder) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
