package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type Story struct{ ent.Schema }

func (Story) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (Story) Fields() []ent.Field {
	return []ent.Field{
		field.String("title").NotEmpty().MaxLen(200).Annotations(entgql.OrderField("TITLE")),
		field.Text("description").Optional().Nillable(),
		field.Enum("priority").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("PRIORITY")),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.Text("notes").Optional().Nillable(),
		field.String("email").MaxLen(255).Annotations(entgql.OrderField("EMAIL")),
		field.Float("score").Default(0).Optional().Nillable().Annotations(entgql.OrderField("SCORE")),
		field.Int("sort_order").Default(0).Annotations(entgql.OrderField("SORT_ORDER")),
	}
}

func (Story) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("folders", Folder.Type).Annotations(entgql.RelayConnection()),
		edge.To("shipments", Shipment.Type).Annotations(entgql.RelayConnection()),
		edge.To("auditlog_links", AuditLog.Type).Annotations(entgql.RelayConnection()),
	}
}

func (Story) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
