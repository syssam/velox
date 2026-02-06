// Package sqlschema provides SQL-specific annotations for Velox schemas.
// Follows Ent's entsql annotation style for familiarity.
//
// Import this package as:
//
//	import "github.com/syssam/velox/dialect/sqlschema"
//
// Then use sqlschema.* functions in your code:
//
//	sqlschema.OnDelete(sqlschema.Cascade)
//
// # API Styles
//
// This package supports two API styles, following Ent's conventions:
//
// Functional style (recommended for simple cases):
//
//	sqlschema.Size(10)
//	sqlschema.ColumnType("JSONB")
//	sqlschema.OnDelete(sqlschema.Cascade)
//	sqlschema.DefaultExpr("gen_random_uuid()")
//
// Struct literal style (for complex configurations):
//
//	sqlschema.Annotation{
//	    Size:       10,
//	    ColumnType: "JSONB",
//	    OnDelete:   sqlschema.Cascade,
//	}
//
// # Field Annotations
//
// Column type and size:
//
//	field.String("code").Annotations(sqlschema.Size(10))
//	field.String("data").Annotations(sqlschema.ColumnType("JSONB"))
//	field.String("name").Annotations(sqlschema.Charset("utf8mb4"), sqlschema.Collation("utf8mb4_unicode_ci"))
//
// Constraints:
//
//	field.Int("age").Annotations(sqlschema.Check("age >= 0"))
//
// Database-level defaults:
//
//	field.Time("created_at").Annotations(sqlschema.Default("CURRENT_TIMESTAMP"))
//	field.UUID("id").Annotations(sqlschema.DefaultExpr("gen_random_uuid()"))
//
// # Edge Annotations
//
// Foreign key cascade actions:
//
//	edge.To("posts", Post.Type).Annotations(sqlschema.OnDelete(sqlschema.Cascade))
//	edge.To("comments", Comment.Type).Annotations(sqlschema.OnDelete(sqlschema.SetNull))
//
// # Index Annotations
//
// Index storage parameters:
//
//	index.Fields("id").Annotations(sqlschema.IndexType("BTREE"), sqlschema.StorageParams("fillfactor=90"))
//
// # Cascade Actions
//
// Available constants for OnDelete and OnUpdate:
//
//	sqlschema.Cascade    - Delete/update related rows
//	sqlschema.SetNull    - Set foreign key to NULL
//	sqlschema.Restrict   - Prevent delete/update if related rows exist
//	sqlschema.SetDefault - Set foreign key to default value
//	sqlschema.NoAction   - No action (database default)
package sqlschema

import (
	"github.com/syssam/velox/dialect/sql"
	"github.com/syssam/velox/schema"
)

// AnnotationName is the name used for SQL annotations.
const AnnotationName = "sql"

// CascadeAction defines cascade behavior for foreign key constraints.
type CascadeAction string

const (
	Cascade    CascadeAction = "CASCADE"
	SetNull    CascadeAction = "SET NULL"
	Restrict   CascadeAction = "RESTRICT"
	SetDefault CascadeAction = "SET DEFAULT"
	NoAction   CascadeAction = "NO ACTION"
)

// Annotation holds SQL-specific settings for fields and edges.
// Can be used with functional constructors or struct literals:
//
//	// Functional style
//	sql.Size(10)
//	sql.ColumnType("JSONB")
//
//	// Struct literal style (like Ent)
//	sql.Annotation{Size: intPtr(10), ColumnType: "JSONB"}
//	sql.Annotation{Table: "users"}
type Annotation struct {
	// Table overrides the database table name for an entity.
	// Equivalent to sqlschema.Annotation{Table: "users"}.
	Table string

	// Schema specifies the database schema for this entity (for multi-schema support).
	// Used when entities are spread across multiple database schemas.
	Schema string

	// Skip indicates this field/entity should be skipped in SQL generation.
	Skip bool

	// Size overrides the column size (e.g., VARCHAR(Size)).
	Size int64

	// ColumnType sets a custom database column type.
	ColumnType string

	// Collation sets the collation for string columns.
	Collation string

	// Check adds a CHECK constraint expression.
	Check string

	// OnDelete sets the ON DELETE cascade action.
	OnDelete CascadeAction

	// OnUpdate sets the ON UPDATE cascade action.
	OnUpdate CascadeAction

	// Default is the SQL literal default value.
	Default string

	// DefaultExpr is a SQL expression for the default value.
	DefaultExpr string

	// Charset sets the character set for string columns.
	Charset string

	// Incremental indicates whether the column has auto-increment behavior.
	Incremental *bool

	// IncrementStart sets the auto-increment start value (pointer to distinguish unset).
	IncrementStart *int

	// Options sets additional table options (MySQL specific).
	Options string

	// Checks holds multiple CHECK constraints as name-expression pairs.
	// Map from constraint name to expression.
	Checks map[string]string

	// ViewAs defines the view definition SQL.
	ViewAs string

	// ViewFor defines dialect-specific view definitions.
	// Map from dialect name to SQL definition.
	ViewFor map[string]string

	// IndexType sets the index access method (BTREE, HASH, GIN, etc.).
	IndexType string

	// StorageParams sets storage parameters for the index (e.g., "fillfactor=90").
	StorageParams string

	// WithComments controls whether comments are stored (exported for Ent compatibility).
	WithComments *bool

	// Prefix sets a column name prefix for mixin fields.
	Prefix string

	// PrefixColumns indicates whether to prefix columns with the type name.
	PrefixColumns bool

	// DefaultExprs provides dialect-specific default expressions.
	// Map from dialect name to expression string.
	DefaultExprs map[string]string
}

// IndexAnnotation holds SQL-specific settings for indexes.
type IndexAnnotation struct {
	// Type sets the index access method (BTREE, HASH, GIN, etc.).
	Type string

	// Types provides dialect-specific index types.
	// Map from dialect name to index type.
	Types map[string]string

	// Where sets the partial index predicate (WHERE clause).
	Where string

	// Desc indicates descending sort order for the index.
	Desc bool

	// DescColumns specifies descending order per column.
	// Map from column name to whether it should be descending.
	DescColumns map[string]bool

	// OpClass sets the operator class for the index (PostgreSQL).
	OpClass string

	// OpClassColumns specifies operator class per column.
	// Map from column name to operator class.
	OpClassColumns map[string]string

	// Prefix sets the index prefix length (MySQL).
	Prefix uint

	// PrefixColumns specifies prefix length per column.
	// Map from column name to prefix length.
	PrefixColumns map[string]uint

	// IncludeColumns specifies columns to include in a covering index.
	IncludeColumns []string
}

// Name implements schema.Annotation for IndexAnnotation.
func (IndexAnnotation) Name() string {
	return AnnotationName
}

// Name implements schema.Annotation.
func (a Annotation) Name() string {
	return AnnotationName
}

// Ensure Annotation implements schema.Annotation.
var _ schema.Annotation = (*Annotation)(nil)

// Table sets the database table name for an entity.
//
// Example:
//
//	func (User) Annotations() []schema.Annotation {
//	    return []schema.Annotation{
//	        sql.Table("users"),
//	    }
//	}
func Table(name string) Annotation {
	return Annotation{Table: name}
}

// Size sets the column size override.
//
// Example:
//
//	field.String("code").
//	    Annotations(sqlschema.Size(10))
func Size(size int64) Annotation {
	return Annotation{Size: size}
}

// OnDelete sets the ON DELETE cascade action for an edge.
//
// Example:
//
//	edge.To("posts", Post.Type).
//	    Annotations(sql.OnDelete(sql.Cascade))
func OnDelete(action CascadeAction) Annotation {
	return Annotation{OnDelete: action}
}

// OnUpdate sets the ON UPDATE cascade action for an edge.
//
// Example:
//
//	edge.To("posts", Post.Type).
//	    Annotations(sql.OnUpdate(sql.Cascade))
func OnUpdate(action CascadeAction) Annotation {
	return Annotation{OnUpdate: action}
}

// WithComments controls whether the field's comment is stored in the database.
// By default, comments are stored. Use WithComments(false) to disable.
//
// Example:
//
//	field.String("internal_note").
//	    Comment("This comment won't be stored in the database").
//	    Annotations(sql.WithComments(false))
func WithComments(enable bool) Annotation {
	return Annotation{WithComments: &enable}
}

// ColumnType sets a custom database column type.
//
// Example:
//
//	field.String("data").
//	    Annotations(sql.ColumnType("JSONB"))
func ColumnType(typ string) Annotation {
	return Annotation{ColumnType: typ}
}

// Collation sets the collation for a string column.
//
// Example:
//
//	field.String("name").
//	    Annotations(sql.Collation("utf8mb4_unicode_ci"))
func Collation(c string) Annotation {
	return Annotation{Collation: c}
}

// Check adds a CHECK constraint to the column.
//
// Example:
//
//	field.Int("age").
//	    Annotations(sql.Check("age >= 0"))
func Check(expr string) Annotation {
	return Annotation{Check: expr}
}

// Default sets a SQL literal default value for migrations.
// Use this for database-level defaults like CURRENT_TIMESTAMP or literal values.
// The value is used as-is in the DEFAULT clause.
//
// Example:
//
//	field.Time("created_at").
//	    Default(time.Now).
//	    Annotations(sql.Default("CURRENT_TIMESTAMP"))
//
//	field.String("status").
//	    Annotations(sql.Default("'pending'"))
func Default(value string) Annotation {
	return Annotation{Default: value}
}

// DefaultExpr sets a SQL expression as the default value for migrations.
// Use this for computed defaults that reference other columns or use functions.
// The expression is used as-is in the DEFAULT clause (not quoted).
//
// Example:
//
//	field.String("slug").
//	    Annotations(sql.DefaultExpr("lower(title)"))
//
//	field.UUID("id").
//	    Annotations(sql.DefaultExpr("gen_random_uuid()"))
func DefaultExpr(expr string) Annotation {
	return Annotation{DefaultExpr: expr}
}

// Charset sets the character set for string columns.
//
// Example:
//
//	field.String("name").
//	    Annotations(sql.Charset("utf8mb4"))
func Charset(charset string) Annotation {
	return Annotation{Charset: charset}
}

// Schema sets the database schema for this entity.
//
// Example:
//
//	func (User) Annotations() []schema.Annotation {
//	    return []schema.Annotation{
//	        sql.Schema("public"),
//	    }
//	}
func Schema(schemaName string) Annotation {
	return Annotation{Schema: schemaName}
}

// Skip marks this entity/field to be skipped in SQL generation.
//
// Example:
//
//	field.String("computed").
//	    Annotations(sql.Skip())
func Skip() Annotation {
	return Annotation{Skip: true}
}

// IndexType sets the index access method for index annotations.
//
// Example:
//
//	index.Fields("tags").Annotations(sql.IndexType("GIN"))
func IndexType(typ string) Annotation {
	return Annotation{IndexType: typ}
}

// StorageParams sets storage parameters for indexes.
//
// Example:
//
//	index.Fields("id").Annotations(sql.StorageParams("fillfactor=90"))
func StorageParams(params string) Annotation {
	return Annotation{StorageParams: params}
}

// Desc returns an index annotation indicating descending sort order.
// Use this on indexes for reverse chronological ordering.
//
// Example:
//
//	index.Fields("created_at").Annotations(sqlschema.Desc())
func Desc() *IndexAnnotation {
	return &IndexAnnotation{Desc: true}
}

// View returns an annotation that defines a view with the given SQL query.
// Use this for entities that represent database views instead of tables.
//
// Example:
//
//	func (PetNames) Annotations() []schema.Annotation {
//	    return []schema.Annotation{
//	        sqlschema.View("SELECT name FROM pets"),
//	    }
//	}
func View(query string) *Annotation {
	return &Annotation{ViewAs: query}
}

// ViewFor returns an annotation that defines a dialect-specific view.
// The function receives a Selector that should be configured with the view query.
//
// Example:
//
//	func (PetNames) Annotations() []schema.Annotation {
//	    return []schema.Annotation{
//	        sqlschema.ViewFor(dialect.Postgres, func(s *sql.Selector) {
//	            s.Select("name").From(sql.Table("pets"))
//	        }),
//	    }
//	}
func ViewFor(d string, fn func(*sql.Selector)) *Annotation {
	s := sql.Dialect(d).Select()
	fn(s)
	query, _ := s.Query()
	return &Annotation{
		ViewFor: map[string]string{
			d: query,
		},
	}
}

// Getters for use by generators.

// GetTable returns the table name and whether it was set.
func (a Annotation) GetTable() (string, bool) {
	return a.Table, a.Table != ""
}

// GetSize returns the size override and whether it was set.
func (a Annotation) GetSize() (int64, bool) {
	return a.Size, a.Size != 0
}

// GetOnDelete returns the ON DELETE action and whether it was set.
func (a Annotation) GetOnDelete() (CascadeAction, bool) {
	return a.OnDelete, a.OnDelete != ""
}

// GetOnUpdate returns the ON UPDATE action and whether it was set.
func (a Annotation) GetOnUpdate() (CascadeAction, bool) {
	return a.OnUpdate, a.OnUpdate != ""
}

// GetWithComments returns whether comments should be stored and whether it was set.
func (a Annotation) GetWithComments() (bool, bool) {
	if a.WithComments == nil {
		return true, false // default is true
	}
	return *a.WithComments, true
}

// GetColumnType returns the custom column type.
func (a Annotation) GetColumnType() string {
	return a.ColumnType
}

// GetCollation returns the collation setting.
func (a Annotation) GetCollation() string {
	return a.Collation
}

// GetCheck returns the CHECK constraint expression.
func (a Annotation) GetCheck() string {
	return a.Check
}

// GetDefault returns the SQL default value and whether it was set.
func (a Annotation) GetDefault() (string, bool) {
	return a.Default, a.Default != ""
}

// GetDefaultExpr returns the SQL default expression and whether it was set.
func (a Annotation) GetDefaultExpr() (string, bool) {
	return a.DefaultExpr, a.DefaultExpr != ""
}

// GetCharset returns the character set.
func (a Annotation) GetCharset() string {
	return a.Charset
}

// GetIncremental returns whether auto-increment is enabled and whether it was set.
func (a Annotation) GetIncremental() (bool, bool) {
	if a.Incremental == nil {
		return false, false
	}
	return *a.Incremental, true
}

// GetIndexType returns the index type.
func (a Annotation) GetIndexType() string {
	return a.IndexType
}

// GetStorageParams returns the storage parameters.
func (a Annotation) GetStorageParams() string {
	return a.StorageParams
}

// Merge combines multiple SQL annotations into one.
// Later annotations override earlier ones for the same field.
func Merge(annotations ...Annotation) Annotation {
	result := Annotation{}
	for _, a := range annotations {
		if a.Table != "" {
			result.Table = a.Table
		}
		if a.Schema != "" {
			result.Schema = a.Schema
		}
		if a.Skip {
			result.Skip = a.Skip
		}
		if a.Size != 0 {
			result.Size = a.Size
		}
		if a.OnDelete != "" {
			result.OnDelete = a.OnDelete
		}
		if a.OnUpdate != "" {
			result.OnUpdate = a.OnUpdate
		}
		if a.WithComments != nil {
			result.WithComments = a.WithComments
		}
		if a.ColumnType != "" {
			result.ColumnType = a.ColumnType
		}
		if a.Collation != "" {
			result.Collation = a.Collation
		}
		if a.Check != "" {
			result.Check = a.Check
		}
		if a.Default != "" {
			result.Default = a.Default
		}
		if a.DefaultExpr != "" {
			result.DefaultExpr = a.DefaultExpr
		}
		if a.Charset != "" {
			result.Charset = a.Charset
		}
		if a.Incremental != nil {
			result.Incremental = a.Incremental
		}
		if a.IndexType != "" {
			result.IndexType = a.IndexType
		}
		if a.StorageParams != "" {
			result.StorageParams = a.StorageParams
		}
	}
	return result
}
