package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type Permission struct{ ent.Schema }

func (Permission) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (Permission) Fields() []ent.Field {
	return []ent.Field{
		field.String("email").MaxLen(255).Annotations(entgql.OrderField("EMAIL")),
		field.String("title").NotEmpty().MaxLen(200).Annotations(entgql.OrderField("TITLE")),
		field.Text("description").Optional().Nillable(),
		field.Float("score").Default(0).Optional().Nillable().Annotations(entgql.OrderField("SCORE")),
		field.String("code").MaxLen(50).Annotations(entgql.OrderField("CODE")),
		field.Text("notes").Optional().Nillable(),
	}
}

func (Permission) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("invoiceitems", InvoiceItem.Type).Annotations(entgql.RelayConnection()),
		edge.To("products", Product.Type).Annotations(entgql.RelayConnection()),
		edge.To("folder_links", Folder.Type).Annotations(entgql.RelayConnection()),
	}
}

func (Permission) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
