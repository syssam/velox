package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

// Product holds the schema definition for the Product entity.
type Product struct {
	velox.Schema
}

// Mixin of the Product.
func (Product) Mixin() []velox.Mixin {
	return []velox.Mixin{
		mixin.Time{},
	}
}

// Fields of the Product.
func (Product) Fields() []velox.Field {
	return []velox.Field{
		field.String("name").
			NotEmpty().
			MaxLen(200).
			Annotations(
				graphql.WhereInput(),
				graphql.OrderField("NAME"),
			),
		field.Float("price").
			Positive().
			Annotations(
				graphql.WhereOps(graphql.OpsComparison),
				graphql.OrderField("PRICE"),
			),
		field.Int("stock").
			NonNegative().
			Default(0).
			Annotations(
				graphql.WhereOps(graphql.OpsComparison),
			),
		field.Bytes("thumbnail").
			Optional().
			Nillable(),
		field.Bool("published").
			Default(false).
			Annotations(
				graphql.WhereInput(),
			),
	}
}

// Edges of the Product.
func (Product) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("tags", Tag.Type).
			Comment("Tags associated with this product"),
	}
}

// Annotations of the Product.
func (Product) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.QueryField(),
		graphql.RelayConnection(),
		graphql.Mutations(
			graphql.MutationCreate(),
			graphql.MutationUpdate(),
		),
	}
}
