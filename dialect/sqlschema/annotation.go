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
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/syssam/velox/dialect/sql"
	"github.com/syssam/velox/schema"
)

// AnnotationName is the name used for SQL annotations (entity/field/edge level).
const AnnotationName = "sql"

// IndexAnnotationName is the name used for index-specific SQL annotations.
// Kept separate from AnnotationName to prevent collision when both are used
// on the same index (e.g. Desc() + &IndexAnnotation{Where: "..."}).
const IndexAnnotationName = "sqlindex"

// CascadeAction defines cascade behavior for foreign key constraints.
type CascadeAction string

const (
	// Cascade deletes or updates related rows when the referenced row is deleted or updated.
	Cascade CascadeAction = "CASCADE"
	// SetNull sets the foreign key column to NULL when the referenced row is deleted or updated.
	SetNull CascadeAction = "SET NULL"
	// Restrict prevents deletion or update of the referenced row.
	Restrict CascadeAction = "RESTRICT"
	// SetDefault sets the foreign key column to its default value.
	SetDefault CascadeAction = "SET DEFAULT"
	// NoAction is similar to Restrict but checked at the end of the transaction.
	NoAction CascadeAction = "NO ACTION"
)

// ConstName returns the constant name of a cascade action (e.g., "Cascade", "SetNull").
// Used by code generation templates for printing the constant name.
func (c CascadeAction) ConstName() string {
	switch c {
	case Cascade:
		return "Cascade"
	case SetNull:
		return "SetNull"
	case Restrict:
		return "Restrict"
	case SetDefault:
		return "SetDefault"
	case NoAction:
		return "NoAction"
	default:
		return string(c)
	}
}

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
	// err carries errors that occurred during annotation construction (e.g. ViewFor
	// with bind args or an empty query). Checked by the compiler loader via Err().
	err error
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

	// StorageParams sets storage parameters for the index (e.g. "fillfactor=90").
	// PostgreSQL-specific: emitted as WITH (...) on CREATE INDEX.
	StorageParams string

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
	return IndexAnnotationName
}

// Merge implements schema.Merger so multiple IndexAnnotation values on the same
// index are combined rather than the second silently overwriting the first.
// Non-zero fields in other win; zero fields leave the receiver's value intact.
func (a IndexAnnotation) Merge(other schema.Annotation) schema.Annotation {
	var ant IndexAnnotation
	switch v := other.(type) {
	case IndexAnnotation:
		ant = v
	case *IndexAnnotation:
		if v != nil {
			ant = *v
		}
	default:
		return a
	}
	if ant.Type != "" {
		a.Type = ant.Type
	}
	if ant.Types != nil {
		if a.Types == nil {
			a.Types = make(map[string]string)
		}
		maps.Copy(a.Types, ant.Types)
	}
	if ant.Where != "" {
		a.Where = ant.Where
	}
	if ant.StorageParams != "" {
		a.StorageParams = ant.StorageParams
	}
	if ant.Desc {
		a.Desc = ant.Desc
	}
	if ant.DescColumns != nil {
		if a.DescColumns == nil {
			a.DescColumns = make(map[string]bool)
		}
		maps.Copy(a.DescColumns, ant.DescColumns)
	}
	if ant.OpClass != "" {
		a.OpClass = ant.OpClass
	}
	if ant.OpClassColumns != nil {
		if a.OpClassColumns == nil {
			a.OpClassColumns = make(map[string]string)
		}
		maps.Copy(a.OpClassColumns, ant.OpClassColumns)
	}
	if ant.Prefix != 0 {
		a.Prefix = ant.Prefix
	}
	if ant.PrefixColumns != nil {
		if a.PrefixColumns == nil {
			a.PrefixColumns = make(map[string]uint)
		}
		maps.Copy(a.PrefixColumns, ant.PrefixColumns)
	}
	if ant.IncludeColumns != nil {
		a.IncludeColumns = append(a.IncludeColumns, ant.IncludeColumns...)
	}
	return a
}

// Name implements schema.Annotation.
func (a Annotation) Name() string {
	return AnnotationName
}

// Err returns any error that occurred during annotation construction (e.g. from ViewFor).
// The compiler loader checks this and surfaces it as a schema validation error.
func (a Annotation) Err() error {
	return a.err
}

// Merge implements schema.Merger so multiple Annotation values on the same
// entity/field/edge are combined rather than the second silently overwriting the first.
// Non-zero fields in other win; zero fields leave the receiver's value intact.
func (a Annotation) Merge(other schema.Annotation) schema.Annotation {
	var ant Annotation
	switch v := other.(type) {
	case Annotation:
		ant = v
	case *Annotation:
		if v != nil {
			ant = *v
		}
	default:
		return a
	}
	if ant.Schema != "" {
		a.Schema = ant.Schema
	}
	if ant.Table != "" {
		a.Table = ant.Table
	}
	if ant.Charset != "" {
		a.Charset = ant.Charset
	}
	if ant.Collation != "" {
		a.Collation = ant.Collation
	}
	if ant.Default != "" {
		a.Default = ant.Default
	}
	if ant.DefaultExpr != "" {
		a.DefaultExpr = ant.DefaultExpr
	}
	if ant.DefaultExprs != nil {
		if a.DefaultExprs == nil {
			a.DefaultExprs = make(map[string]string)
		}
		maps.Copy(a.DefaultExprs, ant.DefaultExprs)
	}
	if ant.Options != "" {
		a.Options = ant.Options
	}
	if ant.Size != 0 {
		a.Size = ant.Size
	}
	if ant.WithComments != nil {
		a.WithComments = ant.WithComments
	}
	if ant.Incremental != nil {
		a.Incremental = ant.Incremental
	}
	if ant.IncrementStart != nil {
		a.IncrementStart = ant.IncrementStart
	}
	if ant.OnDelete != "" {
		a.OnDelete = ant.OnDelete
	}
	if ant.OnUpdate != "" {
		a.OnUpdate = ant.OnUpdate
	}
	if ant.Check != "" {
		a.Check = ant.Check
	}
	if len(ant.Checks) > 0 {
		if a.Checks == nil {
			a.Checks = make(map[string]string)
		}
		maps.Copy(a.Checks, ant.Checks)
	}
	if ant.Skip {
		a.Skip = true
	}
	if ant.ViewAs != "" {
		a.ViewAs = ant.ViewAs
	}
	if len(ant.ViewFor) > 0 {
		if a.ViewFor == nil {
			a.ViewFor = make(map[string]string)
		}
		maps.Copy(a.ViewFor, ant.ViewFor)
	}
	if ant.ColumnType != "" {
		a.ColumnType = ant.ColumnType
	}
	if ant.IndexType != "" {
		a.IndexType = ant.IndexType
	}
	if ant.StorageParams != "" {
		a.StorageParams = ant.StorageParams
	}
	if ant.Prefix != "" {
		a.Prefix = ant.Prefix
	}
	if ant.PrefixColumns {
		a.PrefixColumns = ant.PrefixColumns
	}
	if ant.err != nil {
		a.err = errors.Join(a.err, ant.err)
	}
	return a
}

// Ensure Annotation implements both schema.Annotation and schema.Merger.
var _ interface {
	schema.Annotation
	schema.Merger
} = (*Annotation)(nil)

// Ensure IndexAnnotation implements both schema.Annotation and schema.Merger.
var _ interface {
	schema.Annotation
	schema.Merger
} = (*IndexAnnotation)(nil)

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

// validateSQLFragment checks that a SQL fragment does not contain statement
// separators or dangerous DDL/DML keywords that could lead to injection.
// The context parameter describes what is being validated (e.g., "column type",
// "CHECK expression") for error messages.
func validateSQLFragment(context, value string) error {
	if strings.Contains(value, ";") {
		return fmt.Errorf("sqlschema: %s %q contains forbidden character ';'", context, value)
	}
	upper := strings.ToUpper(strings.TrimSpace(value))
	// Keywords with trailing space to avoid false positives (e.g., "UPDATED_AT" matching "UPDATE").
	// Also check "-- " for SQL comment injection.
	for _, kw := range []string{"SELECT ", "DROP ", "DELETE ", "INSERT ", "UPDATE ", "ALTER ", "TRUNCATE ", "EXEC ", "EXECUTE ", "--"} {
		if strings.Contains(upper, kw) {
			return fmt.Errorf("sqlschema: %s %q contains forbidden SQL keyword %q", context, value, strings.TrimSpace(kw))
		}
	}
	return nil
}

// ValidateColumnType checks that a column type string does not contain
// SQL statement separators or dangerous patterns that could lead to injection.
func ValidateColumnType(typ string) error {
	return validateSQLFragment("column type", typ)
}

// ValidateExpression checks that a SQL expression does not contain
// statement separators or dangerous DDL/DML keywords that could lead to injection.
func ValidateExpression(expr string) error {
	return validateSQLFragment("expression", expr)
}

// ColumnType sets a custom database column type.
// Panics if the type string contains SQL injection patterns (semicolons, DDL keywords).
//
// Example:
//
//	field.String("data").
//	    Annotations(sql.ColumnType("JSONB"))
func ColumnType(typ string) Annotation {
	if err := ValidateColumnType(typ); err != nil {
		panic(err)
	}
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
	if err := ValidateExpression(expr); err != nil {
		panic(fmt.Errorf("sqlschema.Check: %w", err))
	}
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
	if err := validateSQLFragment("default value", value); err != nil {
		panic(fmt.Errorf("sqlschema.Default: %w", err))
	}
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
	if err := ValidateExpression(expr); err != nil {
		panic(fmt.Errorf("sqlschema.DefaultExpr: %w", err))
	}
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

// SchemaTable sets both the database schema and table name for an entity in one annotation.
//
// Example:
//
//	func (User) Annotations() []schema.Annotation {
//	    return []schema.Annotation{
//	        sqlschema.SchemaTable("public", "users"),
//	    }
//	}
func SchemaTable(s, t string) Annotation {
	return Annotation{Schema: s, Table: t}
}

// Checks sets multiple named CHECK constraints on the table.
//
// Example:
//
//	func (User) Annotations() []schema.Annotation {
//	    return []schema.Annotation{
//	        sqlschema.Checks(map[string]string{
//	            "valid_age": "age >= 0 AND age < 150",
//	        }),
//	    }
//	}
func Checks(c map[string]string) Annotation {
	return Annotation{Checks: c}
}

// DefaultExprs sets dialect-specific default expressions for the column.
//
// Example:
//
//	field.UUID("id", uuid.Nil).
//	    Annotations(sqlschema.DefaultExprs(map[string]string{
//	        dialect.MySQL:    "uuid()",
//	        dialect.Postgres: "uuid_generate_v4()",
//	    }))
func DefaultExprs(exprs map[string]string) Annotation {
	return Annotation{DefaultExprs: exprs}
}

// IncrementStart sets the starting value for auto-increment columns.
//
// Example:
//
//	func (User) Annotations() []schema.Annotation {
//	    return []schema.Annotation{
//	        sqlschema.IncrementStart(1000),
//	    }
//	}
func IncrementStart(start int) Annotation {
	return Annotation{IncrementStart: &start}
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
//	index.Fields("tags").Annotations(sqlschema.IndexType("GIN"))
func IndexType(typ string) *IndexAnnotation {
	return &IndexAnnotation{Type: typ}
}

// StorageParams sets storage parameters for indexes (PostgreSQL-specific).
//
// Example:
//
//	index.Fields("id").Annotations(sqlschema.StorageParams("fillfactor=90"))
func StorageParams(params string) *IndexAnnotation {
	return &IndexAnnotation{StorageParams: params}
}

// Prefix returns a new index annotation with a prefix length for a single column index.
// MySQL-specific: limits the index to the first N characters of the column value.
//
// Example:
//
//	index.Fields("name").Annotations(sqlschema.Prefix(100))
//	// CREATE INDEX `t_name` ON `t`(`name`(100))
func Prefix(prefix uint) *IndexAnnotation {
	return &IndexAnnotation{Prefix: prefix}
}

// PrefixColumn returns a new index annotation with a prefix length for a specific column
// in a multi-column index. MySQL-specific.
//
// Example:
//
//	index.Fields("c1", "c2").Annotations(
//	    sqlschema.PrefixColumn("c1", 100),
//	    sqlschema.PrefixColumn("c2", 200),
//	)
//	// CREATE INDEX `t_c1_c2` ON `t`(`c1`(100), `c2`(200))
func PrefixColumn(name string, prefix uint) *IndexAnnotation {
	return &IndexAnnotation{PrefixColumns: map[string]uint{name: prefix}}
}

// DescColumns returns a new index annotation with descending order for specific columns
// in a multi-column index.
//
// Example:
//
//	index.Fields("c1", "c2", "c3").Annotations(sqlschema.DescColumns("c1", "c2"))
//	// CREATE INDEX `t_c1_c2_c3` ON `t`(`c1` DESC, `c2` DESC, `c3`)
func DescColumns(names ...string) *IndexAnnotation {
	ant := &IndexAnnotation{DescColumns: make(map[string]bool, len(names))}
	for _, name := range names {
		ant.DescColumns[name] = true
	}
	return ant
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

// OpClass returns a new index annotation with an operator class for a single column index.
// PostgreSQL-specific.
//
// Example:
//
//	index.Fields("col").Annotations(
//	    sqlschema.IndexType("BRIN"),
//	    sqlschema.OpClass("int8_bloom_ops"),
//	)
//	// CREATE INDEX "t_col" ON "t" USING BRIN ("col" int8_bloom_ops)
func OpClass(op string) *IndexAnnotation {
	return &IndexAnnotation{OpClass: op}
}

// OpClassColumn returns a new index annotation with an operator class for a specific column
// in a multi-column index. PostgreSQL-specific.
//
// Example:
//
//	index.Fields("c1", "c2").Annotations(
//	    sqlschema.IndexType("BRIN"),
//	    sqlschema.OpClassColumn("c1", "int8_bloom_ops"),
//	)
func OpClassColumn(name, op string) *IndexAnnotation {
	return &IndexAnnotation{OpClassColumns: map[string]string{name: op}}
}

// IncludeColumns returns a new index annotation specifying columns to include in a
// covering index (INCLUDE clause). PostgreSQL-specific.
//
// Example:
//
//	index.Fields("c1").Annotations(sqlschema.IncludeColumns("c2", "c3"))
//	// CREATE INDEX "t_c1" ON "t"("c1") INCLUDE ("c2", "c3")
func IncludeColumns(names ...string) *IndexAnnotation {
	return &IndexAnnotation{IncludeColumns: names}
}

// IndexTypes returns a new index annotation with dialect-specific index types.
//
// Example:
//
//	index.Fields("tags").Annotations(sqlschema.IndexTypes(map[string]string{
//	    dialect.MySQL:    "FULLTEXT",
//	    dialect.Postgres: "GIN",
//	}))
func IndexTypes(types map[string]string) *IndexAnnotation {
	return &IndexAnnotation{Types: types}
}

// IndexWhere returns a new index annotation configuring a partial index predicate.
// Works in SQLite and PostgreSQL. The WHERE clause must be in the same normal form
// as it is stored in the database (Atlas dev-database normalization applies).
//
// Example:
//
//	index.Fields("status").Annotations(sqlschema.IndexWhere("status = 'active'"))
//	// CREATE INDEX "t_status" ON "t"("status") WHERE (status = 'active')
func IndexWhere(pred string) *IndexAnnotation {
	return &IndexAnnotation{Where: pred}
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
	q, args := s.Query()
	if len(args) > 0 {
		return &Annotation{err: fmt.Errorf("sqlschema: view query must not contain bind arguments, got %d", len(args))}
	}
	return &Annotation{ViewFor: map[string]string{d: q}}
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
// Later annotations override earlier ones for scalar fields; maps are merged.
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
			result.Skip = true
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
		if a.Charset != "" {
			result.Charset = a.Charset
		}
		if a.Check != "" {
			result.Check = a.Check
		}
		if len(a.Checks) > 0 {
			if result.Checks == nil {
				result.Checks = make(map[string]string)
			}
			maps.Copy(result.Checks, a.Checks)
		}
		if a.Default != "" {
			result.Default = a.Default
		}
		if a.DefaultExpr != "" {
			result.DefaultExpr = a.DefaultExpr
		}
		if len(a.DefaultExprs) > 0 {
			if result.DefaultExprs == nil {
				result.DefaultExprs = make(map[string]string)
			}
			maps.Copy(result.DefaultExprs, a.DefaultExprs)
		}
		if a.Options != "" {
			result.Options = a.Options
		}
		if a.Incremental != nil {
			result.Incremental = a.Incremental
		}
		if a.IncrementStart != nil {
			result.IncrementStart = a.IncrementStart
		}
		if a.IndexType != "" {
			result.IndexType = a.IndexType
		}
		if a.StorageParams != "" {
			result.StorageParams = a.StorageParams
		}
		if a.ViewAs != "" {
			result.ViewAs = a.ViewAs
		}
		if len(a.ViewFor) > 0 {
			if result.ViewFor == nil {
				result.ViewFor = make(map[string]string)
			}
			maps.Copy(result.ViewFor, a.ViewFor)
		}
		if a.Prefix != "" {
			result.Prefix = a.Prefix
		}
		if a.PrefixColumns {
			result.PrefixColumns = true
		}
	}
	return result
}
