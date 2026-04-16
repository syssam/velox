package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type Warehouse struct{ ent.Schema }

func (Warehouse) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (Warehouse) Fields() []ent.Field {
	return []ent.Field{
		field.String("url").MaxLen(500).Optional().Nillable().Annotations(entgql.OrderField("URL")),
		field.Int("quantity").Default(0).Annotations(entgql.OrderField("QUANTITY")),
		field.Float("amount").Default(0).Annotations(entgql.OrderField("AMOUNT")),
		field.Float("score").Default(0).Optional().Nillable().Annotations(entgql.OrderField("SCORE")),
		field.Text("notes").Optional().Nillable(),
		field.Bool("active").Default(false),
	}
}

func (Warehouse) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("attachments", Attachment.Type).Annotations(entgql.RelayConnection()),
		edge.To("products", Product.Type).Annotations(entgql.RelayConnection()),
		edge.To("label_links", Label.Type).Annotations(entgql.RelayConnection()),
	}
}

func (Warehouse) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
