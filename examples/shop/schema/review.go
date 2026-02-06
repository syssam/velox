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

// Review holds the schema definition for the Review entity.
// Demonstrates:
// - M2O relationships with explicit FK fields
// - Composite unique constraint (user + product = one review per product per user)
// - Enum fields
// - Deep nested query testing (User → Reviews → Product → Categories)
type Review struct {
	velox.Schema
}

// Mixin of the Review.
func (Review) Mixin() []velox.Mixin {
	return []velox.Mixin{
		mixin.Time{},
		mixin.SoftDelete{}, // Soft delete pattern
	}
}

// Fields of the Review.
func (Review) Fields() []velox.Field {
	return []velox.Field{
		// UUID primary key
		field.UUID("id", uuid.UUID{}).
			Default(uuid.New).
			Immutable(),

		// Rating 1-5
		field.Int("rating").
			Range(1, 5).
			Comment("Rating from 1 to 5 stars"),

		field.String("title").
			Optional().
			Nillable().
			MaxLen(200).
			Comment("Review title"),

		field.Text("content").
			Optional().
			Nillable().
			Comment("Review content/body"),

		field.Enum("status").
			Values("pending", "approved", "rejected", "flagged").
			Default("pending").
			Comment("Review moderation status"),

		field.Bool("verified_purchase").
			Default(false).
			Comment("Whether reviewer purchased the product"),

		field.Int("helpful_count").
			Default(0).
			NonNegative().
			Comment("Number of helpful votes"),

		field.Int("report_count").
			Default(0).
			NonNegative().
			Comment("Number of reports/flags"),

		// FK fields - explicit for testing
		field.UUID("user_id", uuid.UUID{}).
			Immutable().
			Comment("User who wrote the review"),

		field.UUID("product_id", uuid.UUID{}).
			Immutable().
			Comment("Product being reviewed"),

		// Optional: admin who moderated
		field.UUID("moderated_by_id", uuid.UUID{}).
			Optional().
			Nillable().
			Comment("Admin who moderated this review"),

		field.Time("moderated_at").
			Optional().
			Nillable().
			Comment("When the review was moderated"),

		// JSON for storing response from seller
		field.JSON("seller_response", &SellerResponse{}).
			Optional().
			Comment("Response from seller"),
	}
}

// SellerResponse represents a seller's response to a review.
type SellerResponse struct {
	Content     string `json:"content"`
	RespondedAt string `json:"responded_at"`
	RespondedBy string `json:"responded_by"`
}

// Edges of the Review.
func (Review) Edges() []velox.Edge {
	return []velox.Edge{
		// M2O: Review → User (author)
		edge.From("user", User.Type).
			Ref("reviews").
			Field("user_id").
			Unique().
			Required().
			Immutable().
			Comment("User who wrote the review"),

		// M2O: Review → Product (inverse of Product.reviews)
		edge.From("product", Product.Type).
			Ref("reviews").
			Field("product_id").
			Unique().
			Required().
			Immutable().
			Comment("Product being reviewed"),

		// M2O: Review → User (moderator) - multiple edges to same entity
		edge.To("moderated_by", User.Type).
			Field("moderated_by_id").
			Unique().
			Comment("Admin who moderated this review"),
	}
}

// Indexes of the Review.
func (Review) Indexes() []velox.Index {
	return []velox.Index{
		// Composite unique: one review per user per product
		index.Fields("user_id", "product_id").Unique(),
		index.Fields("product_id", "status"),
		index.Fields("user_id"),
		index.Fields("status"),
		index.Fields("rating"),
		index.Fields("created_at"),
		index.Fields("deleted_at"), // For soft delete queries
	}
}

// Annotations of the Review.
func (Review) Annotations() []schema.Annotation {
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
