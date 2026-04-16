package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type Role struct{ ent.Schema }

func (Role) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (Role) Fields() []ent.Field {
	return []ent.Field{
		field.Float("score").Default(0).Optional().Nillable().Annotations(entgql.OrderField("SCORE")),
		field.Enum("status").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("STATUS")),
		field.Int("quantity").Default(0).Annotations(entgql.OrderField("QUANTITY")),
		field.String("title").NotEmpty().MaxLen(200).Annotations(entgql.OrderField("TITLE")),
		field.String("code").MaxLen(50).Annotations(entgql.OrderField("CODE")),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.String("name").NotEmpty().MaxLen(100).Annotations(entgql.OrderField("NAME")),
		field.Text("notes").Optional().Nillable(),
	}
}

func (Role) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("shipments", Shipment.Type).Annotations(entgql.RelayConnection()),
		edge.To("milestones", Milestone.Type).Annotations(entgql.RelayConnection()),
		edge.To("auditlog_links", AuditLog.Type).Annotations(entgql.RelayConnection()),
	}
}

func (Role) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
