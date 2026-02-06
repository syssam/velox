package schema

import (
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/dialect/sqlschema"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/index"
	"github.com/syssam/velox/schema/mixin"
)

// OrderItem holds the schema definition for the OrderItem entity.
type OrderItem struct {
	velox.Schema
}

// Mixin of the OrderItem.
func (OrderItem) Mixin() []velox.Mixin {
	return []velox.Mixin{
		mixin.Time{},
	}
}

// Fields of the OrderItem.
func (OrderItem) Fields() []velox.Field {
	return []velox.Field{
		// UUID primary key
		field.UUID("id", uuid.UUID{}).
			Default(uuid.New).
			Immutable(),

		// Quantity
		field.Int("quantity").
			Positive().
			Default(1).
			Comment("Quantity ordered"),

		// Unit price at time of order (snapshot)
		field.Other("unit_price", decimal.Decimal{}).
			SchemaType(map[string]string{
				"postgres": "DECIMAL(10,2)",
				"sqlite":   "REAL",
			}).
			Annotations(
				sqlschema.Default("0.00"),
			).
			Comment("Unit price at time of order"),

		// Line total (quantity * unit_price)
		field.Other("line_total", decimal.Decimal{}).
			SchemaType(map[string]string{
				"postgres": "DECIMAL(12,2)",
				"sqlite":   "REAL",
			}).
			Annotations(
				sqlschema.Default("0.00"),
			).
			Comment("Line item total"),

		// Product name snapshot (in case product is deleted)
		field.String("product_name").
			MaxLen(255).
			Comment("Product name at time of order"),

		// Product SKU snapshot
		field.String("product_sku").
			MaxLen(50).
			Comment("Product SKU at time of order"),

		// Order ID (FK)
		field.UUID("order_id", uuid.UUID{}).
			Immutable().
			Comment("Order this item belongs to"),

		// Product ID (FK, nullable if product is deleted)
		field.UUID("product_id", uuid.UUID{}).
			Optional().
			Nillable().
			Comment("Product reference"),
	}
}

// Edges of the OrderItem.
func (OrderItem) Edges() []velox.Edge {
	return []velox.Edge{
		edge.From("order", Order.Type).
			Ref("items").
			Field("order_id").
			Unique().
			Required().
			Immutable().
			Comment("Order this item belongs to"),

		edge.From("product", Product.Type).
			Ref("order_items").
			Field("product_id").
			Unique().
			Comment("Product reference"),
	}
}

// Indexes of the OrderItem.
func (OrderItem) Indexes() []velox.Index {
	return []velox.Index{
		index.Fields("order_id"),
		index.Fields("product_id"),
	}
}

// Annotations of the OrderItem.
func (OrderItem) Annotations() []schema.Annotation {
	return []schema.Annotation{
		// Skip direct mutations - items are managed through order
		graphql.Skip(graphql.SkipMutationCreateInput, graphql.SkipMutationUpdateInput),
	}
}
