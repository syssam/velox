package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/index"
	"github.com/syssam/velox/schema/mixin"
)

type Workspace struct{ velox.Schema }

func (Workspace) Mixin() []velox.Mixin {
	return []velox.Mixin{mixin.Time{}}
}

func (Workspace) Fields() []velox.Field {
	return []velox.Field{
		field.String("name").
			NotEmpty().
			MaxLen(100),
		field.String("description").
			Optional().
			Nillable().
			MaxLen(500),
		field.Time("deleted_at").
			Optional().
			Nillable(),
		field.Bool("active").
			Default(true),
	}
}

func (Workspace) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("members", Member.Type),
		edge.To("todos", Todo.Type),
	}
}

func (Workspace) Indexes() []velox.Index {
	return []velox.Index{
		index.Fields("name").Unique(),
	}
}

func (Workspace) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.QueryField(),
		graphql.RelayConnection(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
