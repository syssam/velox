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

// Product holds the schema definition for the Product entity.
// Demonstrates:
// - M2M relationships (Tags, Categories)
// - Multiple edges from same entity type (created_by, updated_by from User)
// - Soft delete pattern
// - Custom types (decimal.Decimal)
// - JSON fields with map and struct types
type Product struct {
	velox.Schema
}

// Mixin of the Product.
func (Product) Mixin() []velox.Mixin {
	return []velox.Mixin{
		mixin.Time{},
		mixin.SoftDelete{}, // Soft delete pattern
	}
}

// Fields of the Product.
func (Product) Fields() []velox.Field {
	return []velox.Field{
		// UUID primary key
		field.UUID("id", uuid.UUID{}).
			Default(uuid.New).
			Immutable().
			Comment("Product unique identifier"),

		field.String("name").
			NotEmpty().
			MaxLen(255).
			Comment("Product name"),

		field.String("sku").
			Unique().
			NotEmpty().
			MaxLen(50).
			Comment("Stock Keeping Unit"),

		field.Text("description").
			Optional().
			Nillable().
			Comment("Product description"),

		field.Text("short_description").
			Optional().
			Nillable().
			MaxLen(500).
			Comment("Short product description for listings"),

		// Decimal price using shopspring/decimal
		field.Other("price", decimal.Decimal{}).
			SchemaType(map[string]string{
				"postgres": "DECIMAL(10,2)",
				"sqlite":   "REAL",
			}).
			Annotations(
				sqlschema.Default("0.00"),
			).
			Comment("Product price"),

		// Decimal cost price
		field.Other("cost_price", decimal.Decimal{}).
			SchemaType(map[string]string{
				"postgres": "DECIMAL(10,2)",
				"sqlite":   "REAL",
			}).
			Optional().
			Nillable().
			Comment("Cost price for margin calculation"),

		// Compare at price (for sale display)
		field.Other("compare_at_price", decimal.Decimal{}).
			SchemaType(map[string]string{
				"postgres": "DECIMAL(10,2)",
				"sqlite":   "REAL",
			}).
			Optional().
			Nillable().
			Comment("Original price for sale comparison"),

		field.Int("stock_quantity").
			Default(0).
			NonNegative().
			Comment("Available stock quantity"),

		field.Int("low_stock_threshold").
			Default(10).
			NonNegative().
			Comment("Threshold for low stock alerts"),

		field.Enum("status").
			Values("draft", "active", "discontinued", "out_of_stock").
			Default("draft").
			Comment("Product status"),

		field.Bool("is_featured").
			Default(false).
			Comment("Featured product flag"),

		field.Bool("is_digital").
			Default(false).
			Comment("Whether product is digital/downloadable"),

		field.Float("weight").
			Optional().
			Nillable().
			NonNegative().
			Comment("Product weight in kg"),

		// Dimensions as JSON struct
		field.JSON("dimensions", &ProductDimensions{}).
			Optional().
			Comment("Product dimensions"),

		// Additional metadata as JSON map
		field.JSON("metadata", map[string]any{}).
			Optional().
			Comment("Additional product metadata"),

		// Image URLs as JSON array
		field.JSON("image_urls", []string{}).
			Optional().
			Comment("Product image URLs"),

		// SEO fields
		field.String("seo_title").
			Optional().
			Nillable().
			MaxLen(70).
			Comment("SEO title"),

		field.String("seo_description").
			Optional().
			Nillable().
			MaxLen(160).
			Comment("SEO meta description"),

		// Audit fields - FK to User
		field.UUID("created_by_id", uuid.UUID{}).
			Optional().
			Nillable().
			Comment("User who created this product"),

		field.UUID("updated_by_id", uuid.UUID{}).
			Optional().
			Nillable().
			Comment("User who last updated this product"),
	}
}

// ProductDimensions represents physical dimensions of a product.
type ProductDimensions struct {
	Length float64 `json:"length"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
	Unit   string  `json:"unit"` // cm, in, etc.
}

// Edges of the Product.
func (Product) Edges() []velox.Edge {
	return []velox.Edge{
		// M2M: Product ↔ Tags
		edge.To("tags", Tag.Type).
			Comment("Product tags"),

		// M2M: Product ↔ Categories
		edge.To("categories", Category.Type).
			Comment("Product categories"),

		// O2M: Product → Reviews
		edge.To("reviews", Review.Type).
			Comment("Product reviews"),

		// O2M: Product → OrderItems
		edge.To("order_items", OrderItem.Type).
			Comment("Order items containing this product"),

		// M2O: Product → User (created_by) - multiple edges from same entity
		edge.From("created_by", User.Type).
			Ref("created_products").
			Field("created_by_id").
			Unique().
			Comment("User who created this product"),

		// M2O: Product → User (updated_by) - multiple edges from same entity
		edge.From("updated_by", User.Type).
			Ref("updated_products").
			Field("updated_by_id").
			Unique().
			Comment("User who last updated this product"),
	}
}

// Indexes of the Product.
func (Product) Indexes() []velox.Index {
	return []velox.Index{
		index.Fields("status"),
		index.Fields("is_featured", "status"),
		index.Fields("sku"),
		index.Fields("created_by_id"),
		index.Fields("updated_by_id"),
		index.Fields("deleted_at"), // For soft delete queries
	}
}

// Annotations of the Product.
func (Product) Annotations() []schema.Annotation {
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
