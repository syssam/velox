package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type Deployment struct{ ent.Schema }

func (Deployment) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (Deployment) Fields() []ent.Field {
	return []ent.Field{
		field.String("title").NotEmpty().MaxLen(200).Annotations(entgql.OrderField("TITLE")),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.Text("notes").Optional().Nillable(),
		field.Text("description").Optional().Nillable(),
		field.String("email").MaxLen(255).Annotations(entgql.OrderField("EMAIL")),
		field.String("name").NotEmpty().MaxLen(100).Annotations(entgql.OrderField("NAME")),
	}
}

func (Deployment) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("invoices", Invoice.Type).Annotations(entgql.RelayConnection()),
		edge.To("releases", Release.Type).Annotations(entgql.RelayConnection()),
		edge.To("subscription_links", Subscription.Type).Annotations(entgql.RelayConnection()),
	}
}

func (Deployment) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
