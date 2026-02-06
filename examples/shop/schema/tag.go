package schema

import (
	"github.com/google/uuid"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/index"
	"github.com/syssam/velox/schema/mixin"
)

// Tag holds the schema definition for the Tag entity.
// Demonstrates M2M relationship with Product.
type Tag struct {
	velox.Schema
}

// Mixin of the Tag.
func (Tag) Mixin() []velox.Mixin {
	return []velox.Mixin{
		mixin.Time{},
	}
}

// Fields of the Tag.
func (Tag) Fields() []velox.Field {
	return []velox.Field{
		// UUID primary key
		field.UUID("id", uuid.UUID{}).
			Default(uuid.New).
			Immutable(),

		field.String("name").
			Unique().
			NotEmpty().
			MaxLen(50).
			Comment("Tag name"),

		field.String("slug").
			Unique().
			NotEmpty().
			MaxLen(50).
			Comment("URL-friendly slug"),

		field.String("color").
			Optional().
			MaxLen(7). // #RRGGBB
			Comment("Tag color for UI"),

		field.Enum("type").
			Values("product", "collection", "promotion", "seasonal").
			Default("product").
			Comment("Tag type for categorization"),
	}
}

// Edges of the Tag.
func (Tag) Edges() []velox.Edge {
	return []velox.Edge{
		// M2M with Product (inverse side)
		edge.From("products", Product.Type).
			Ref("tags").
			Comment("Products with this tag"),
	}
}

// Indexes of the Tag.
func (Tag) Indexes() []velox.Index {
	return []velox.Index{
		index.Fields("type"),
	}
}

// Annotations of the Tag.
func (Tag) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
		graphql.Mutations(
			graphql.MutationCreate(),
			graphql.MutationUpdate(),
			graphql.MutationDelete(),
		),
	}
}
