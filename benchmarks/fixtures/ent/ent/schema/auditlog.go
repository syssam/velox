package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type AuditLog struct{ ent.Schema }

func (AuditLog) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (AuditLog) Fields() []ent.Field {
	return []ent.Field{
		field.Int("sort_order").Default(0).Annotations(entgql.OrderField("SORT_ORDER")),
		field.String("email").MaxLen(255).Annotations(entgql.OrderField("EMAIL")),
		field.Int("quantity").Default(0).Annotations(entgql.OrderField("QUANTITY")),
		field.String("code").MaxLen(50).Annotations(entgql.OrderField("CODE")),
		field.Text("description").Optional().Nillable(),
		field.Enum("status").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("STATUS")),
		field.String("url").MaxLen(500).Optional().Nillable().Annotations(entgql.OrderField("URL")),
	}
}

func (AuditLog) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("tasks", Task.Type).Annotations(entgql.RelayConnection()),
		edge.To("reviews", Review.Type).Annotations(entgql.RelayConnection()),
		edge.To("inventory_links", Inventory.Type).Annotations(entgql.RelayConnection()),
	}
}

func (AuditLog) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
