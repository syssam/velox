package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type Conversation struct{ ent.Schema }

func (Conversation) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (Conversation) Fields() []ent.Field {
	return []ent.Field{
		field.Int("quantity").Default(0).Annotations(entgql.OrderField("QUANTITY")),
		field.String("name").NotEmpty().MaxLen(100).Annotations(entgql.OrderField("NAME")),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.Enum("priority").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("PRIORITY")),
		field.Bool("active").Default(false),
		field.Text("notes").Optional().Nillable(),
	}
}

func (Conversation) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("auditlogs", AuditLog.Type).Annotations(entgql.RelayConnection()),
		edge.To("settings", Setting.Type).Annotations(entgql.RelayConnection()),
		edge.To("folder_links", Folder.Type).Annotations(entgql.RelayConnection()),
	}
}

func (Conversation) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
