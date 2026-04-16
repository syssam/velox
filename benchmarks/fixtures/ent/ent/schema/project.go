package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type Project struct{ ent.Schema }

func (Project) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (Project) Fields() []ent.Field {
	return []ent.Field{
		field.Text("notes").Optional().Nillable(),
		field.Float("amount").Default(0).Annotations(entgql.OrderField("AMOUNT")),
		field.Enum("status").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("STATUS")),
		field.Enum("priority").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("PRIORITY")),
		field.Int("quantity").Default(0).Annotations(entgql.OrderField("QUANTITY")),
		field.Text("description").Optional().Nillable(),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.Bool("active").Default(false),
	}
}

func (Project) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("invoices", Invoice.Type).Annotations(entgql.RelayConnection()),
		edge.To("apikeys", ApiKey.Type).Annotations(entgql.RelayConnection()),
		edge.To("shipment_links", Shipment.Type).Annotations(entgql.RelayConnection()),
	}
}

func (Project) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
