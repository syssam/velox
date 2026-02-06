package schema

import (
	"github.com/google/uuid"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/dialect/sqlschema"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/index"
	"github.com/syssam/velox/schema/mixin"
)

// Category holds the schema definition for the Category entity.
// Demonstrates self-referential edges (parent/children hierarchy).
type Category struct {
	velox.Schema
}

// Mixin of the Category.
func (Category) Mixin() []velox.Mixin {
	return []velox.Mixin{
		mixin.Time{},
		mixin.SoftDelete{}, // Soft delete pattern
	}
}

// Fields of the Category.
func (Category) Fields() []velox.Field {
	return []velox.Field{
		// UUID primary key
		field.UUID("id", uuid.UUID{}).
			Default(uuid.New).
			Immutable(),

		field.String("name").
			NotEmpty().
			MaxLen(100).
			Comment("Category name"),

		field.String("slug").
			Unique().
			NotEmpty().
			MaxLen(100).
			Comment("URL-friendly slug"),

		field.Text("description").
			Optional().
			Nillable().
			Comment("Category description"),

		field.Int("sort_order").
			Default(0).
			Comment("Display order within parent"),

		field.Bool("is_active").
			Default(true).
			Comment("Whether category is visible"),

		// Self-referential FK - nullable for root categories
		field.UUID("parent_id", uuid.UUID{}).
			Optional().
			Nillable().
			Comment("Parent category ID (null for root)"),

		// Depth in hierarchy (computed)
		field.Int("depth").
			Default(0).
			NonNegative().
			Comment("Depth in category tree"),

		// Path for efficient tree queries (e.g., "/uuid1/uuid2/uuid3")
		field.String("path").
			Optional().
			MaxLen(1000).
			Comment("Materialized path for tree queries"),
	}
}

// Edges of the Category.
func (Category) Edges() []velox.Edge {
	return []velox.Edge{
		// Self-referential: parent category
		edge.To("parent", Category.Type).
			Field("parent_id").
			Unique().
			Comment("Parent category"),

		// Self-referential: child categories
		edge.From("children", Category.Type).
			Ref("parent").
			Comment("Child categories"),

		// M2M with Product (inverse side)
		edge.From("products", Product.Type).
			Ref("categories").
			Comment("Products in this category"),
	}
}

// Indexes of the Category.
func (Category) Indexes() []velox.Index {
	return []velox.Index{
		index.Fields("parent_id"),
		index.Fields("is_active", "sort_order"),
		index.Fields("depth"),
		index.Fields("deleted_at"), // For soft delete queries
	}
}

// Annotations of the Category.
func (Category) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
		graphql.Mutations(
			graphql.MutationCreate(),
			graphql.MutationUpdate(),
			graphql.MutationDelete(),
		),
		// OnDelete: SET NULL for parent_id when parent deleted
		sqlschema.OnDelete(sqlschema.SetNull),
	}
}
