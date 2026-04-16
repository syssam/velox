package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type Milestone struct{ ent.Schema }

func (Milestone) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (Milestone) Fields() []ent.Field {
	return []ent.Field{
		field.String("email").MaxLen(255).Annotations(entgql.OrderField("EMAIL")),
		field.Enum("status").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("STATUS")),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.Enum("priority").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("PRIORITY")),
		field.Text("description").Optional().Nillable(),
		field.Float("amount").Default(0).Annotations(entgql.OrderField("AMOUNT")),
	}
}

func (Milestone) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("categorys", Category.Type).Annotations(entgql.RelayConnection()),
		edge.To("configs", AppConfig.Type).Annotations(entgql.RelayConnection()),
		edge.To("auditlog_links", AuditLog.Type).Annotations(entgql.RelayConnection()),
	}
}

func (Milestone) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
