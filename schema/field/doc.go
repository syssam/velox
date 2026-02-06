// Package field provides fluent builders for defining entity fields in Velox ORM.
//
// Field names follow database conventions (snake_case), while Go struct field
// names are automatically converted to PascalCase:
//
//	field.Int64("user_id")    // DB: user_id, Go: UserID
//	field.String("email")     // DB: email, Go: Email
//
// # Field Types
//
// The package supports various field types:
//
//	// String fields
//	field.String("name")
//	field.Text("description")
//
//	// Numeric fields
//	field.Int("count")
//	field.Int64("big_number")
//	field.Float64("price")
//
//	// Boolean fields
//	field.Bool("is_active")
//
//	// Time fields
//	field.Time("created_at")
//
//	// UUID fields
//	field.UUID("id", uuid.UUID{})
//
//	// Enum fields
//	field.Enum("status").Values("pending", "active", "inactive")
//
//	// JSON fields
//	field.JSON("metadata", map[string]any{})
//
//	// Binary fields
//	field.Bytes("data")
//
//	// Custom types
//	field.Custom("amount", decimal.Decimal{})
//
// # Field Options
//
// Fields support various configuration options:
//
//	field.String("email").
//	    Unique().              // Unique constraint
//	    Optional().            // Not required on create
//	    Nillable().            // Nullable in DB, pointer in Go
//	    Immutable().           // Cannot be updated
//	    Default("unknown").    // Default value
//	    Comment("User email")  // Database comment
//
// # Validation
//
// Fields support built-in validators:
//
//	// String validators
//	field.String("name").NotEmpty().MinLen(2).MaxLen(100)
//	field.String("email").Match(emailRegex)
//
//	// Numeric validators
//	field.Int64("age").NonNegative().Max(150)
//	field.Int64("rating").Range(1, 5)
//	field.Float64("price").Positive()
//
// Struct tag validation (go-playground/validator):
//
//	field.String("email").
//	    ValidateCreate("required,email").
//	    ValidateUpdate("omitempty,email")
//
// # Nullability
//
// Velox separates API input requirements from database nullability:
//
//	// Optional: Not required in input, NOT NULL in DB
//	field.String("role").Optional().Default("user")
//
//	// Nillable: Nullable in DB, pointer in Go
//	field.String("nickname").Nillable()
//
//	// Both: Optional input, nullable DB
//	field.String("bio").Optional().Nillable()
//
// # Defaults
//
// Fields support both literal and function defaults:
//
//	// Literal default
//	field.String("status").Default("active")
//	field.Int64("count").Default(0)
//
//	// Function default (called at runtime)
//	field.Time("created_at").Default(time.Now)
//	field.UUID("id", uuid.UUID{}).Default(uuid.New)
//
//	// Update default
//	field.Time("updated_at").UpdateDefault(time.Now)
//
// # SQL Annotations
//
// Fields can be customized with SQL annotations:
//
//	import "github.com/syssam/velox/dialect/sqlschema"
//
//	field.String("data").
//	    Annotations(
//	        sqlschema.ColumnType("JSONB"),
//	        sqlschema.Check("length(data) > 0"),
//	    )
//
// # Custom Go Types
//
// Fields can use custom Go types with appropriate scanning:
//
//	field.Other("amount", decimal.Decimal{}).
//	    SchemaType(map[string]string{
//	        dialect.Postgres: "decimal(10,2)",
//	    })
package field
