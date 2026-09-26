package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Conversation struct{ velox.Schema }

func (Conversation) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (Conversation) Fields() []velox.Field {
	return []velox.Field{
		field.Int("quantity").Default(0).Annotations(graphql.OrderField("QUANTITY")),
		field.String("name").NotEmpty().MaxLen(100).Annotations(graphql.OrderField("NAME")),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.Enum("priority").Values("low", "medium", "high").Default("medium").Annotations(graphql.OrderField("PRIORITY")),
		field.Bool("active").Default(false),
		field.Text("notes").Optional().Nillable(),
	}
}

func (Conversation) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("auditlogs", AuditLog.Type).Annotations(graphql.RelayConnection()),
		edge.To("settings", Setting.Type).Annotations(graphql.RelayConnection()),
		edge.To("folder_links", Folder.Type).Annotations(graphql.RelayConnection()),
	}
}

func (Conversation) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
