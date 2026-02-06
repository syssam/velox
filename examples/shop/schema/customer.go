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

// Customer holds the schema definition for the Customer entity.
// Demonstrates:
// - O2O relationship with User (optional link)
// - O2M relationship with Orders (with OnDelete cascade)
// - JSON fields with struct type
// - Soft delete pattern
type Customer struct {
	velox.Schema
}

// Mixin of the Customer.
func (Customer) Mixin() []velox.Mixin {
	return []velox.Mixin{
		mixin.Time{},
		mixin.SoftDelete{}, // Soft delete pattern
	}
}

// Fields of the Customer.
func (Customer) Fields() []velox.Field {
	return []velox.Field{
		// UUID primary key
		field.UUID("id", uuid.UUID{}).
			Default(uuid.New).
			Immutable(),

		field.String("email").
			Unique().
			NotEmpty().
			MaxLen(255).
			Annotations(
				graphql.CreateInputValidate("required,email"),
				graphql.UpdateInputValidate("omitempty,email"),
			).
			Comment("Customer email address"),

		field.String("first_name").
			NotEmpty().
			MaxLen(100).
			Comment("Customer first name"),

		field.String("last_name").
			NotEmpty().
			MaxLen(100).
			Comment("Customer last name"),

		field.String("phone").
			Optional().
			Nillable().
			MaxLen(20).
			Comment("Customer phone number"),

		field.Enum("status").
			Values("active", "inactive", "suspended", "vip").
			Default("active").
			Comment("Customer account status"),

		// Shipping address as JSON (pointer types are automatically nillable)
		field.JSON("shipping_address", &Address{}).
			Optional().
			Comment("Default shipping address"),

		// Billing address as JSON
		field.JSON("billing_address", &Address{}).
			Optional().
			Comment("Billing address"),

		// Customer preferences
		field.JSON("preferences", &CustomerPreferences{}).
			Optional().
			Comment("Customer preferences"),

		// Total spent (denormalized for quick access)
		field.Float("total_spent").
			Default(0).
			NonNegative().
			Comment("Total amount spent by customer"),

		// Order count (denormalized)
		field.Int("order_count").
			Default(0).
			NonNegative().
			Comment("Number of orders placed"),

		// Optional link to User account
		field.UUID("user_id", uuid.UUID{}).
			Optional().
			Nillable().
			Unique().
			Comment("Linked user account"),
	}
}

// Address represents a customer address (stored as JSON).
type Address struct {
	Street     string `json:"street"`
	City       string `json:"city"`
	State      string `json:"state"`
	PostalCode string `json:"postal_code"`
	Country    string `json:"country"`
}

// CustomerPreferences represents customer preferences.
type CustomerPreferences struct {
	Newsletter       bool   `json:"newsletter"`
	SmsNotifications bool   `json:"sms_notifications"`
	Currency         string `json:"currency"`
	Language         string `json:"language"`
}

// Edges of the Customer.
func (Customer) Edges() []velox.Edge {
	return []velox.Edge{
		// O2M: Customer → Orders (with cascade delete for orders when customer deleted)
		edge.To("orders", Order.Type).
			Annotations(
				sqlschema.OnDelete(sqlschema.Cascade),
			).
			Comment("Customer orders"),

		// O2O: Customer ↔ User (inverse side)
		edge.From("user", User.Type).
			Ref("customer").
			Field("user_id").
			Unique().
			Comment("Linked user account"),
	}
}

// Indexes of the Customer.
func (Customer) Indexes() []velox.Index {
	return []velox.Index{
		index.Fields("status"),
		index.Fields("last_name", "first_name"),
		index.Fields("user_id"),
		index.Fields("deleted_at"), // For soft delete queries
	}
}

// Annotations of the Customer.
func (Customer) Annotations() []schema.Annotation {
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
