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

// Order holds the schema definition for the Order entity.
// Demonstrates:
// - M2O with explicit FK field (customer_id)
// - O2M with cascade delete (items)
// - Custom type fields (decimal.Decimal)
// - JSON fields with struct type
// - Soft delete pattern
type Order struct {
	velox.Schema
}

// Mixin of the Order.
func (Order) Mixin() []velox.Mixin {
	return []velox.Mixin{
		mixin.Time{},
		mixin.SoftDelete{}, // Soft delete pattern
	}
}

// Fields of the Order.
func (Order) Fields() []velox.Field {
	return []velox.Field{
		// UUID primary key
		field.UUID("id", uuid.UUID{}).
			Default(uuid.New).
			Immutable(),

		// Order number (human readable)
		field.String("order_number").
			Unique().
			Immutable().
			MaxLen(50).
			Comment("Human readable order number"),

		field.Enum("status").
			Values("pending", "confirmed", "processing", "shipped", "delivered", "cancelled", "refunded").
			Default("pending").
			Comment("Order status"),

		field.Enum("payment_status").
			Values("pending", "paid", "failed", "refunded", "partially_refunded").
			Default("pending").
			Comment("Payment status"),

		field.String("payment_method").
			Optional().
			Nillable().
			MaxLen(50).
			Comment("Payment method used"),

		// Subtotal (sum of line items)
		field.Other("subtotal", decimal.Decimal{}).
			SchemaType(map[string]string{
				"postgres": "DECIMAL(12,2)",
				"sqlite":   "REAL",
			}).
			Annotations(
				sqlschema.Default("0.00"),
			).
			Comment("Order subtotal before tax and shipping"),

		// Discount amount
		field.Other("discount_amount", decimal.Decimal{}).
			SchemaType(map[string]string{
				"postgres": "DECIMAL(12,2)",
				"sqlite":   "REAL",
			}).
			Annotations(
				sqlschema.Default("0.00"),
			).
			Comment("Discount amount applied"),

		// Tax amount
		field.Other("tax_amount", decimal.Decimal{}).
			SchemaType(map[string]string{
				"postgres": "DECIMAL(12,2)",
				"sqlite":   "REAL",
			}).
			Annotations(
				sqlschema.Default("0.00"),
			).
			Comment("Tax amount"),

		// Shipping cost
		field.Other("shipping_cost", decimal.Decimal{}).
			SchemaType(map[string]string{
				"postgres": "DECIMAL(12,2)",
				"sqlite":   "REAL",
			}).
			Annotations(
				sqlschema.Default("0.00"),
			).
			Comment("Shipping cost"),

		// Total amount
		field.Other("total", decimal.Decimal{}).
			SchemaType(map[string]string{
				"postgres": "DECIMAL(12,2)",
				"sqlite":   "REAL",
			}).
			Annotations(
				sqlschema.Default("0.00"),
			).
			Comment("Total order amount"),

		// Shipping address snapshot
		field.JSON("shipping_address", &Address{}).
			Comment("Shipping address at time of order"),

		// Billing address snapshot
		field.JSON("billing_address", &Address{}).
			Optional().
			Comment("Billing address at time of order"),

		// Notes
		field.Text("notes").
			Optional().
			Nillable().
			Comment("Order notes"),

		// Internal notes (staff only)
		field.Text("internal_notes").
			Optional().
			Nillable().
			Annotations(
				graphql.Skip(graphql.SkipType), // Hide from public API
			).
			Comment("Internal staff notes"),

		// Tracking info
		field.String("tracking_number").
			Optional().
			Nillable().
			MaxLen(100).
			Comment("Shipping tracking number"),

		field.String("tracking_url").
			Optional().
			Nillable().
			MaxLen(500).
			Comment("Shipping tracking URL"),

		// Timestamps for order lifecycle
		field.Time("confirmed_at").
			Optional().
			Nillable().
			Comment("When order was confirmed"),

		field.Time("shipped_at").
			Optional().
			Nillable().
			Comment("When order was shipped"),

		field.Time("delivered_at").
			Optional().
			Nillable().
			Comment("When order was delivered"),

		field.Time("cancelled_at").
			Optional().
			Nillable().
			Comment("When order was cancelled"),

		// Customer ID (FK)
		field.UUID("customer_id", uuid.UUID{}).
			Comment("Customer who placed the order"),

		// Coupon code used (for reference)
		field.String("coupon_code").
			Optional().
			Nillable().
			MaxLen(50).
			Comment("Coupon code applied"),
	}
}

// Edges of the Order.
func (Order) Edges() []velox.Edge {
	return []velox.Edge{
		edge.From("customer", Customer.Type).
			Ref("orders").
			Field("customer_id").
			Unique().
			Required().
			Comment("Customer who placed the order"),

		// O2M with cascade delete
		edge.To("items", OrderItem.Type).
			Annotations(
				sqlschema.OnDelete(sqlschema.Cascade),
			).
			Comment("Order line items"),
	}
}

// Indexes of the Order.
func (Order) Indexes() []velox.Index {
	return []velox.Index{
		index.Fields("status"),
		index.Fields("payment_status"),
		index.Fields("customer_id"),
		index.Fields("created_at"),
		index.Fields("status", "payment_status"),
		index.Fields("deleted_at"), // For soft delete queries
	}
}

// Annotations of the Order.
func (Order) Annotations() []schema.Annotation {
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
