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

// User holds the schema definition for the User entity.
// Demonstrates multiple edges to same entity and O2O relationship.
type User struct {
	velox.Schema
}

// Mixin of the User.
func (User) Mixin() []velox.Mixin {
	return []velox.Mixin{
		mixin.Time{},
		mixin.SoftDelete{}, // Soft delete pattern
	}
}

// Fields of the User.
func (User) Fields() []velox.Field {
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
			Comment("User email address"),

		field.String("username").
			Unique().
			NotEmpty().
			MaxLen(50).
			Comment("Username for display"),

		field.String("password_hash").
			Sensitive().
			MaxLen(255).
			Annotations(
				graphql.Skip(graphql.SkipType), // Never expose password hash
			).
			Comment("Hashed password"),

		field.String("first_name").
			Optional().
			Nillable().
			MaxLen(100).
			Comment("First name"),

		field.String("last_name").
			Optional().
			Nillable().
			MaxLen(100).
			Comment("Last name"),

		field.String("avatar_url").
			Optional().
			Nillable().
			MaxLen(500).
			Comment("Profile picture URL"),

		field.Enum("role").
			Values("customer", "staff", "admin", "super_admin").
			Default("customer").
			Comment("User role"),

		field.Enum("status").
			Values("active", "inactive", "suspended", "pending_verification").
			Default("pending_verification").
			Comment("Account status"),

		field.Time("last_login_at").
			Optional().
			Nillable().
			Comment("Last login timestamp"),

		field.Time("email_verified_at").
			Optional().
			Nillable().
			Comment("Email verification timestamp"),

		// Metadata as JSON
		field.JSON("preferences", map[string]any{}).
			Optional().
			Comment("User preferences"),
	}
}

// Edges of the User.
func (User) Edges() []velox.Edge {
	return []velox.Edge{
		// O2O: User ↔ Customer (optional link)
		edge.To("customer", Customer.Type).
			Unique().
			Comment("Associated customer account"),

		// O2M: User → Reviews (user wrote these reviews)
		edge.To("reviews", Review.Type).
			Comment("Reviews written by this user"),

		// O2M: User → Products (products created by this user - for staff/admin)
		edge.To("created_products", Product.Type).
			Comment("Products created by this user"),

		// O2M: User → Products (products last updated by this user)
		edge.To("updated_products", Product.Type).
			Comment("Products last updated by this user"),
	}
}

// Indexes of the User.
func (User) Indexes() []velox.Index {
	return []velox.Index{
		index.Fields("role"),
		index.Fields("status"),
		index.Fields("role", "status"),
		index.Fields("deleted_at"), // For soft delete queries
	}
}

// Annotations of the User.
func (User) Annotations() []schema.Annotation {
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
