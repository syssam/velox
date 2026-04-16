package schema

import (
	"example.com/fullgql/rule"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/dialect/sqlschema"
	"github.com/syssam/velox/privacy"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/index"
	"github.com/syssam/velox/schema/mixin"
)

// Todo holds the schema definition for the Todo entity.
type Todo struct {
	velox.Schema
}

// Mixin of the Todo.
func (Todo) Mixin() []velox.Mixin {
	return []velox.Mixin{
		mixin.Time{},
	}
}

// Fields of the Todo.
func (Todo) Fields() []velox.Field {
	return []velox.Field{
		field.String("title").
			NotEmpty().
			MaxLen(200).
			Annotations(
				graphql.WhereInput(),
				graphql.OrderField("TITLE"),
				graphql.CreateInputValidate("required,min=1,max=200"),
				graphql.UpdateInputValidate("omitempty,min=1,max=200"),
			),
		field.Text("description").
			Optional().
			Nillable().
			Annotations(
				graphql.Skip(graphql.SkipWhereInput),
			),
		field.Enum("status").
			Values("todo", "in_progress", "done", "canceled").
			Default("todo").
			Annotations(
				graphql.WhereInput(),
				graphql.OrderField("STATUS"),
			),
		field.Enum("priority").
			Values("low", "medium", "high", "urgent").
			Default("medium").
			Annotations(
				graphql.WhereInput(),
				graphql.OrderField("PRIORITY"),
			),
		field.Time("due_date").
			Optional().
			Nillable().
			Annotations(
				graphql.WhereOps(graphql.OpsComparison),
			),
		field.Bool("completed").
			Default(false).
			Annotations(
				graphql.WhereInput(),
			),
		field.Int("estimated_hours").
			Optional().
			Nillable().
			NonNegative().
			Annotations(
				graphql.WhereOps(graphql.OpsComparison),
			),
	}
}

// Edges of the Todo.
func (Todo) Edges() []velox.Edge {
	return []velox.Edge{
		edge.From("owner", User.Type).
			Ref("todos").
			Unique().
			Required().
			Comment("The user who owns this todo"),
		edge.To("comments", Comment.Type).
			Comment("Comments on this todo").
			Annotations(
				sqlschema.OnDelete(sqlschema.Cascade),
			),
		edge.To("tags", Tag.Type).
			Comment("Tags associated with this todo"),
		edge.From("category", Category.Type).
			Ref("todos").
			Unique().
			Comment("Category this todo belongs to"),
		edge.To("labels", Label.Type).
			Comment("Labels on this todo"),
		edge.From("workspace", Workspace.Type).
			Ref("todos").
			Unique().
			Comment("Workspace this todo belongs to"),
	}
}

// Indexes of the Todo.
func (Todo) Indexes() []velox.Index {
	return []velox.Index{
		index.Fields("status", "priority"),
		index.Fields("due_date"),
		index.Fields("completed"),
	}
}

// Policy defines the privacy policy of the Todo.
func (Todo) Policy() velox.Policy {
	return privacy.Policy{
		Mutation: privacy.MutationPolicy{
			rule.DenyEmptyField("title"),
		},
		Query: privacy.QueryPolicy{
			privacy.AlwaysAllowRule(),
		},
	}
}

// Annotations of the Todo.
func (Todo) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.QueryField(),
		graphql.RelayConnection(),
		graphql.Mutations(
			graphql.MutationCreate(),
			graphql.MutationUpdate(),
		),
		graphql.WhereInputEdges("owner", "tags", "category"),
	}
}
