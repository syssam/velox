// Package schema provides the building blocks for defining Velox entity schemas.
//
// This package serves as the entry point for schema definition, re-exporting
// the core types and builders from its subpackages:
//
//   - [field]: Field builders for entity attributes
//   - [edge]: Edge builders for entity relationships
//   - [index]: Index builders for database indexes
//   - [mixin]: Reusable schema components
//   - [annotation/graphql]: GraphQL-specific annotations
//   - [annotation/sql]: SQL-specific annotations
//
// # Quick Start
//
// Define an entity schema by embedding velox.Schema and implementing the
// required methods:
//
//	type User struct{ velox.Schema }
//
//	func (User) Mixin() []velox.Mixin {
//	    return []velox.Mixin{
//	        mixin.ID{},    // int64 auto-increment primary key
//	        mixin.Time{},  // created_at and updated_at timestamps
//	    }
//	}
//
//	func (User) Fields() []velox.Field {
//	    return []velox.Field{
//	        field.String("email").Unique().Email().MaxLen(255),
//	        field.String("name").NotEmpty().MaxLen(100),
//	        field.Enum("status").Values("active", "suspended", "deleted"),
//	    }
//	}
//
//	func (User) Edges() []velox.Edge {
//	    return []velox.Edge{
//	        edge.To("posts", Post.Type),                     // O2M: User has many Posts
//	        edge.To("profile", Profile.Type).Unique(),       // O2O: User has one Profile
//	    }
//	}
//
//	func (User) Indexes() []velox.Index {
//	    return []velox.Index{
//	        index.Fields("email").Unique(),
//	        index.Fields("status", "created_at"),
//	    }
//	}
//
// # Field Types
//
// The field package provides builders for all common field types:
//
//	field.String("name")           // VARCHAR
//	field.Text("bio")              // TEXT (unlimited)
//	field.Int64("count")           // BIGINT
//	field.Float64("price")         // DOUBLE PRECISION
//	field.Bool("active")           // BOOLEAN
//	field.Time("created_at")       // TIMESTAMP
//	field.UUID("id", uuid.UUID{})  // UUID
//	field.Enum("status")           // ENUM
//	field.JSON("metadata", M{})    // JSONB
//	field.Bytes("data")            // BYTEA
//
// # Validation
//
// Fields support both built-in validators and struct tag validators:
//
//	// Built-in validators (self-documenting)
//	field.String("email").NotEmpty().MaxLen(255).Email()
//	field.Int64("age").NonNegative().Max(150)
//	field.Float64("rating").Range(0, 5)
//
//	// Struct tag validators (go-playground/validator syntax)
//	field.String("password").ValidateCreate("required,min=8,max=72")
//
// # Relationships
//
// The edge package defines entity relationships following Ent-style conventions:
//
//	// One-to-Many (default)
//	edge.To("posts", Post.Type)
//
//	// One-to-One
//	edge.To("profile", Profile.Type).Unique()
//
//	// Many-to-One (belongs to)
//	edge.From("author", User.Type).Field("user_id")
//
//	// Many-to-Many (through join table)
//	edge.To("tags", Tag.Type).Through(PostTag.Type)
//
// # Mixins
//
// The mixin package provides reusable schema components:
//
//	mixin.ID{}          // int64 auto-increment primary key
//	mixin.UUIDID{}      // UUID primary key
//	mixin.Time{}        // created_at, updated_at timestamps
//	mixin.SoftDelete{}  // deleted_at with soft-delete hooks
//	mixin.TenantID{}    // Multi-tenant isolation
//
// # Annotations
//
// Annotations customize code generation behavior:
//
//	// GraphQL annotations
//	graphql.RelayConnection()           // Enable Relay-style connections
//	graphql.Type("Member")              // Custom GraphQL type name
//	graphql.Skip(graphql.SkipMutations) // Skip mutation generation
//
//	// SQL annotations
//	sql.ColumnType("JSONB")             // Custom column type
//	sql.OnDelete(sql.Cascade)           // Cascade delete
//	sql.Check("age >= 0")               // CHECK constraint
//
// For detailed documentation on each subpackage, see their respective package docs.
package schema
