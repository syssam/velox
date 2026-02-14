package gen

import (
	"database/sql"
	"fmt"
	"go/token"
	"reflect"
	"slices"
	"strings"

	"ariga.io/atlas/sql/postgres"

	"github.com/syssam/velox/compiler/load"
	"github.com/syssam/velox/dialect"
	"github.com/syssam/velox/dialect/sql/schema"
	"github.com/syssam/velox/schema/field"
)

// =============================================================================
// Field methods
// =============================================================================

// Constant returns the constant name of the field.
func (f Field) Constant() string {
	return "Field" + pascal(f.Name)
}

// DefaultName returns the variable name of the default value of this field.
func (f Field) DefaultName() string { return "Default" + pascal(f.Name) }

// UpdateDefaultName returns the variable name of the update default value of this field.
func (f Field) UpdateDefaultName() string { return "Update" + f.DefaultName() }

// DefaultValue returns the default value of the field. Invoked by the template.
func (f Field) DefaultValue() any { return f.def.DefaultValue }

// DefaultFunc returns a bool stating if the default value is a func. Invoked by the template.
func (f Field) DefaultFunc() bool { return f.def.DefaultKind == reflect.Func }

// OrderName returns the function/option name for ordering by this field.
func (f Field) OrderName() string {
	name := "By" + pascal(f.Name)
	// Some users store associations count as a separate field.
	// In this case, we suffix the order name with "Field".
	if f.typ == nil || !strings.HasSuffix(name, "Count") {
		return name
	}
	for _, e := range f.typ.Edges {
		if nameE, err := e.OrderCountName(); err == nil && nameE == name {
			return name + "Field"
		}
	}
	return name
}

// BuilderField returns the struct member of the field in the builder.
func (f Field) BuilderField() string {
	if f.IsEdgeField() {
		e, err := f.Edge()
		if err != nil {
			panic(fmt.Sprintf("velox/gen: failed to resolve edge for field %q: %v", f.Name, err))
		}
		return e.BuilderField()
	}
	return builderField(f.Name)
}

// StructField returns the struct member of the field in the model.
func (f Field) StructField() string {
	return pascal(f.Name)
}

// EnumNames returns the enum values of a field.
func (f Field) EnumNames() []string {
	names := make([]string, 0, len(f.Enums))
	for _, e := range f.Enums {
		names = append(names, e.Name)
	}
	return names
}

// EnumValues returns the values of the enum field.
func (f Field) EnumValues() []string {
	values := make([]string, 0, len(f.Enums))
	for _, e := range f.Enums {
		values = append(values, e.Value)
	}
	return values
}

// EnumName returns the constant name for the enum.
func (f Field) EnumName(enum string) string {
	if !token.IsExported(enum) || !token.IsIdentifier(enum) {
		enum = pascal(enum)
	}
	return pascal(f.Name) + enum
}

// EnumTypeName returns the generated enum type name for fields without custom GoType.
// The format is {EntityName}{FieldStructField}, e.g., "ABTestingType" for ABTesting.Type field.
// This is used when generating code in the main ent package.
func (f Field) EnumTypeName() string {
	if f.typ == nil {
		return f.StructField()
	}
	return f.typ.Name + f.StructField()
}

// SubpackageEnumTypeName returns the enum type name as used in the entity's subpackage.
// This is just the field struct name, e.g., "Type" for the type field.
// Used when generating code in subpackages.
func (f Field) SubpackageEnumTypeName() string {
	return f.StructField()
}

// EnumPkgPath returns the import path of the subpackage where the enum type is defined.
// For example, "github.com/project/velox/abtesting" for an ABTesting entity.
// Returns empty string if the field doesn't belong to a type.
func (f Field) EnumPkgPath() string {
	if f.typ == nil || f.cfg == nil {
		return ""
	}
	// Construct full import path: {Config.Package}/{entityDir}
	// e.g., "github.com/project/velox" + "/" + "abtesting"
	if f.cfg.Package != "" {
		return f.cfg.Package + "/" + f.typ.PackageDir()
	}
	return f.typ.PackageDir()
}

// Validator returns the validator name.
func (f Field) Validator() string {
	return pascal(f.Name) + "Validator"
}

// MutationGet returns the method name for getting the field value.
// The default name is just a pascal format. If the method conflicts
// with the mutation methods, prefix the method with "Get".
func (f Field) MutationGet() string {
	name := pascal(f.Name)
	if mutMethods[name] || (name == "SetID" && f.typ.ID.UserDefined) {
		name = "Get" + name
	}
	return name
}

// MutationGetOld returns the method name for getting the old value of a field.
func (f Field) MutationGetOld() string {
	name := "Old" + pascal(f.Name)
	if mutMethods[name] {
		name = "Get" + name
	}
	return name
}

// MutationReset returns the method name for resetting the field value.
// The default name is "Reset<FieldName>". If the method conflicts
// with the mutation methods, suffix the method with "Field".
func (f Field) MutationReset() string {
	name := "Reset" + pascal(f.Name)
	if mutMethods[name] {
		name += "Field"
	}
	return name
}

// MutationSet returns the method name for setting the field value.
// The default name is "Set<FieldName>". If the method conflicts
// with the mutation methods, suffix the method with "Field".
func (f Field) MutationSet() string {
	name := "Set" + f.StructField()
	if mutMethods[name] {
		name += "Field"
	}
	return name
}

// MutationClear returns the method name for clearing the field value.
func (f Field) MutationClear() string {
	return "Clear" + f.StructField()
}

// MutationCleared returns the method name for indicating if the field
// was cleared in the mutation.
func (f Field) MutationCleared() string {
	return f.StructField() + "Cleared"
}

// MutationAdd returns the method name for adding a value to the field.
// The default name is "Add<FieldName>". If the method conflicts with
// the mutation methods, suffix the method with "Field".
func (f Field) MutationAdd() string {
	name := "Add" + f.StructField()
	if mutMethods[name] {
		name += "Field"
	}
	return name
}

// MutationAdded returns the method name for getting the field value
// that was added to the field.
func (f Field) MutationAdded() string {
	name := "Added" + f.StructField()
	if mutMethods[name] {
		name += "Field"
	}
	return name
}

// MutationAppend returns the method name for appending a list of values to the field.
// The default name is "Append<FieldName>". If the method conflicts with the mutation methods,
// suffix the method with "Field".
func (f Field) MutationAppend() string {
	name := "Append" + f.StructField()
	if mutMethods[name] {
		name += "Field"
	}
	return name
}

// MutationAppended returns the method name for getting the field value
// that was added to the field.
func (f Field) MutationAppended() string {
	name := "Appended" + f.StructField()
	if mutMethods[name] {
		name += "Field"
	}
	return name
}

// RequiredFor returns a list of dialects that this field is required for.
// A field can be required in one database, but optional in the other. e.g.,
// in case a SchemaType was defined as "serial" for PostgreSQL, but "int" for SQLite.
func (f Field) RequiredFor() (dialects []string) {
	// Return nil if storage config is not set (single dialect mode).
	if f.cfg == nil || f.cfg.Storage == nil || len(f.cfg.Storage.Dialects) == 0 {
		return nil
	}
	seen := make(map[string]struct{})
	if f.def != nil && f.def.SchemaType != nil {
		switch f.def.SchemaType[dialect.Postgres] {
		case postgres.TypeSerial, postgres.TypeBigSerial, postgres.TypeSmallSerial:
			seen[dialect.Postgres] = struct{}{}
		}
	}
	switch d := f.Column().Default.(type) {
	// Static values (or nil) are set by
	// the builders, unless explicitly set.
	case nil:
	// Database default values for all dialects.
	case schema.Expr:
		return nil
	case map[string]schema.Expr:
		for k := range d {
			seen[k] = struct{}{}
		}
	}
	for _, d := range f.cfg.Storage.Dialects {
		if _, ok := seen[strings.ToLower(strings.TrimPrefix(d, "dialect."))]; !ok {
			dialects = append(dialects, d)
		}
	}
	return dialects
}

// IsBool returns true if the field is a bool field.
func (f Field) IsBool() bool { return f.Type != nil && f.Type.Type == field.TypeBool }

// IsBytes returns true if the field is a bytes field.
func (f Field) IsBytes() bool { return f.Type != nil && f.Type.Type == field.TypeBytes }

// IsTime returns true if the field is a timestamp field.
func (f Field) IsTime() bool { return f.Type != nil && f.Type.Type == field.TypeTime }

// IsJSON returns true if the field is a JSON field.
func (f Field) IsJSON() bool { return f.Type != nil && f.Type.Type == field.TypeJSON }

// IsOther returns true if the field is an Other field.
func (f Field) IsOther() bool { return f.Type != nil && f.Type.Type == field.TypeOther }

// IsString returns true if the field is a string field.
func (f Field) IsString() bool { return f.Type != nil && f.Type.Type == field.TypeString }

// IsUUID returns true if the field is a UUID field.
func (f Field) IsUUID() bool { return f.Type != nil && f.Type.Type == field.TypeUUID }

// IsInt returns true if the field is an int field.
func (f Field) IsInt() bool { return f.Type != nil && f.Type.Type == field.TypeInt }

// IsInt64 returns true if the field is an int64 field.
func (f Field) IsInt64() bool { return f.Type != nil && f.Type.Type == field.TypeInt64 }

// IsEnum returns true if the field is an enum field.
func (f Field) IsEnum() bool { return f.Type != nil && f.Type.Type == field.TypeEnum }

// IsEdgeField reports if the given field is an edge-field (i.e. a foreign-key)
// that was referenced by one of the edges.
func (f Field) IsEdgeField() bool { return f.fk != nil }

// IsDeprecated returns true if the field is deprecated.
func (f Field) IsDeprecated() bool { return f.def != nil && f.def.Deprecated }

// DeprecationReason returns the deprecation reason of the field.
func (f Field) DeprecationReason() string {
	if f.def != nil {
		return f.def.DeprecatedReason
	}
	return ""
}

// Edge returns the edge this field is point to.
func (f Field) Edge() (*Edge, error) {
	if !f.IsEdgeField() {
		return nil, fmt.Errorf("field %q is not an edge-field (missing foreign-key)", f.Name)
	}
	if e := f.fk.Edge; e.OwnFK() {
		return e, nil
	}
	return f.fk.Edge.Ref, nil
}

// Sensitive returns true if the field is a sensitive field.
func (f Field) Sensitive() bool { return f.def != nil && f.def.Sensitive }

// Comment returns the comment of the field,
func (f Field) Comment() string {
	if f.def != nil {
		return f.def.Comment
	}
	return ""
}

// NillableValue reports if the field holds a Go value (not a pointer), but the field is nillable.
// It's used by the templates to prefix values with pointer operators (e.g. &intValue or *intValue).
func (f Field) NillableValue() bool {
	return f.Nillable && !f.Type.RType.IsPtr()
}

// ScanType returns the Go type that is used for `rows.Scan`.
func (f Field) ScanType() string {
	if f.Type.ValueScanner() {
		if f.Nillable && !f.standardNullType() {
			return "sql.NullScanner"
		}
		return f.Type.RType.String()
	}
	switch f.Type.Type {
	case field.TypeJSON, field.TypeBytes:
		return "[]byte"
	case field.TypeString, field.TypeEnum:
		return "sql.NullString"
	case field.TypeBool:
		return "sql.NullBool"
	case field.TypeTime:
		return "sql.NullTime"
	case field.TypeInt, field.TypeInt8, field.TypeInt16, field.TypeInt32, field.TypeInt64,
		field.TypeUint, field.TypeUint8, field.TypeUint16, field.TypeUint32, field.TypeUint64:
		return "sql.NullInt64"
	case field.TypeFloat32, field.TypeFloat64:
		return "sql.NullFloat64"
	}
	return f.Type.String()
}

// HasValueScanner indicates if the field has (an external) ValueScanner.
func (f Field) HasValueScanner() bool {
	return f.def != nil && f.def.ValueScanner
}

// ValueFunc returns a path to the Value field (func) of the external ValueScanner.
func (f Field) ValueFunc() (string, error) {
	if !f.HasValueScanner() {
		return "", fmt.Errorf("%q does not have an external ValueScanner", f.Name)
	}
	return fmt.Sprintf("%s.ValueScanner.%s.Value", f.typ.Package(), f.StructField()), nil
}

// ScanValueFunc returns a path to the ScanValue field (func) of the external ValueScanner.
func (f Field) ScanValueFunc() (string, error) {
	if !f.HasValueScanner() {
		return "", fmt.Errorf("%q does not have an external ValueScanner", f.Name)
	}
	return fmt.Sprintf("%s.ValueScanner.%s.ScanValue", f.typ.Package(), f.StructField()), nil
}

// FromValueFunc returns a path to the FromValue field (func) of the external ValueScanner.
func (f Field) FromValueFunc() (string, error) {
	if !f.HasValueScanner() {
		return "", fmt.Errorf("%q does not have an external ValueScanner", f.Name)
	}
	return fmt.Sprintf("%s.ValueScanner.%s.FromValue", f.typ.Package(), f.StructField()), nil
}

// NewScanType returns an expression for creating a new object
// to be used by the `rows.Scan` method. A sql.Scanner or a
// nillable-type supported by the SQL driver (e.g. []byte).
func (f Field) NewScanType() string {
	if f.Type.ValueScanner() {
		expr := fmt.Sprintf("new(%s)", f.Type.RType.String())
		if f.Nillable && !f.standardNullType() {
			expr = fmt.Sprintf("&sql.NullScanner{S: %s}", expr)
		}
		return expr
	}
	expr := f.Type.String()
	switch f.Type.Type {
	case field.TypeJSON, field.TypeBytes:
		expr = "[]byte"
	case field.TypeString, field.TypeEnum:
		expr = "sql.NullString"
	case field.TypeBool:
		expr = "sql.NullBool"
	case field.TypeTime:
		expr = "sql.NullTime"
	case field.TypeInt, field.TypeInt8, field.TypeInt16, field.TypeInt32, field.TypeInt64,
		field.TypeUint, field.TypeUint8, field.TypeUint16, field.TypeUint32, field.TypeUint64:
		expr = "sql.NullInt64"
	case field.TypeFloat32, field.TypeFloat64:
		expr = "sql.NullFloat64"
	}
	return fmt.Sprintf("new(%s)", expr)
}

// ScanTypeField extracts the nullable type field (if exists) from the given receiver.
// It also does the type conversion if needed.
func (f Field) ScanTypeField(rec string) string {
	expr := rec
	if f.Type.ValueScanner() {
		if !f.Type.RType.IsPtr() {
			expr = "*" + expr
		}
		if f.Nillable && !f.standardNullType() {
			return fmt.Sprintf("%s.S.(*%s)", expr, f.Type.RType.String())
		}
		return expr
	}
	switch f.Type.Type {
	case field.TypeEnum:
		expr = fmt.Sprintf("%s(%s.String)", f.Type, rec)
	case field.TypeString, field.TypeBool, field.TypeInt64, field.TypeFloat64:
		expr = f.goType(fmt.Sprintf("%s.%s", rec, titleCase(f.Type.Type.String())))
	case field.TypeTime:
		expr = fmt.Sprintf("%s.Time", rec)
	case field.TypeFloat32:
		expr = fmt.Sprintf("%s(%s.Float64)", f.Type, rec)
	case field.TypeInt, field.TypeInt8, field.TypeInt16, field.TypeInt32,
		field.TypeUint, field.TypeUint8, field.TypeUint16, field.TypeUint32, field.TypeUint64:
		expr = fmt.Sprintf("%s(%s.Int64)", f.Type, rec)
	}
	return expr
}

// standardNullType reports if the field is one of the standard SQL types.
func (f Field) standardNullType() bool {
	return slices.ContainsFunc([]reflect.Type{
		nullBoolType,
		nullBoolPType,
		nullFloatType,
		nullFloatPType,
		nullInt32Type,
		nullInt32PType,
		nullInt64Type,
		nullInt64PType,
		nullTimeType,
		nullTimePType,
		nullStringType,
		nullStringPType,
	}, f.Type.RType.TypeEqual)
}

// Column returns the table column. It sets it as a primary key (auto_increment)
// in case of ID field, unless stated otherwise.
func (f Field) Column() *schema.Column {
	c := &schema.Column{
		Name:     f.StorageKey(),
		Type:     f.Type.Type,
		Unique:   f.Unique,
		Nullable: f.Nillable,
		Size:     f.size(),
		Enums:    f.EnumValues(),
		Comment:  f.sqlComment(),
	}
	switch {
	case f.Default && (f.Type.Numeric() || f.Type.Type == field.TypeBool):
		c.Default = f.DefaultValue()
	case f.Default && (f.IsString() || f.IsEnum()):
		if s, ok := f.DefaultValue().(string); ok {
			c.Default = s
		}
	}
	// Override with annotation default if present.
	f.applyEntSQLDefaults(c)
	// If FeatureAutoDefault is enabled and the field doesn't have an explicit
	// default, automatically add a zero-value default to ensure safe migrations
	// on tables with existing data. This applies to ALL NOT NULL fields.
	if c.Default == nil && f.needsAutoDefault() {
		c.Default = f.zeroValue()
	}
	// Override the collation defined in the
	// schema if it was provided by an annotation.
	if ant := f.EntSQL(); ant != nil && ant.Collation != "" {
		c.Collation = ant.Collation
	}
	if f.def != nil {
		c.SchemaType = f.def.SchemaType
	}
	return c
}

// needsAutoDefault reports whether the field should have an automatic
// database DEFAULT value added when FeatureAutoDefault is enabled.
// This applies to ALL NOT NULL fields (both Required and Optional) to ensure
// safe migrations on tables with existing data - following big tech best practices.
//
// Excluded types (require explicit Default() or sqlschema.DefaultExpr()):
//   - Enum: no universal zero value
//   - JSON: zero could be '{}', '[]', or 'null'
//   - Time: zero time (0001-01-01) is rarely useful, use DefaultExpr("CURRENT_TIMESTAMP")
//   - UUID: zero UUID is rarely useful, use DefaultExpr("gen_random_uuid()")
//   - Bytes: zero bytes may not be meaningful
//   - Other: custom types have no known zero value
func (f Field) needsAutoDefault() bool {
	// Feature must be enabled.
	if f.cfg == nil || !f.cfg.HasFeature(FeatureAutoDefault.Name) {
		return false
	}
	// Skip Nillable fields - NULL columns don't need DEFAULT.
	if f.Nillable {
		return false
	}
	// Skip if field already has an explicit default.
	if f.Default {
		return false
	}
	// Only support types with meaningful zero values.
	if f.Type == nil {
		return false
	}
	switch f.Type.Type {
	case field.TypeString:
		return true
	case field.TypeBool:
		return true
	case field.TypeInt, field.TypeInt8, field.TypeInt16, field.TypeInt32, field.TypeInt64:
		return true
	case field.TypeUint, field.TypeUint8, field.TypeUint16, field.TypeUint32, field.TypeUint64:
		return true
	case field.TypeFloat32, field.TypeFloat64:
		return true
	default:
		// Enum, JSON, Time, UUID, Bytes, Other - require explicit default
		return false
	}
}

// zeroValue returns the zero value for the field type to be used
// as database DEFAULT when FeatureAutoDefault is enabled.
func (f Field) zeroValue() any {
	if f.Type == nil {
		return nil
	}
	switch f.Type.Type {
	case field.TypeString:
		return ""
	case field.TypeBool:
		return false
	case field.TypeInt, field.TypeInt8, field.TypeInt16, field.TypeInt32, field.TypeInt64:
		return 0
	case field.TypeUint, field.TypeUint8, field.TypeUint16, field.TypeUint32, field.TypeUint64:
		return 0
	case field.TypeFloat32, field.TypeFloat64:
		return 0.0
	default:
		return nil
	}
}

// incremental returns if the column has an incremental behavior.
// If no value is defined externally, we use a provided def flag.
func (f Field) incremental(def bool) bool {
	if ant := f.EntSQL(); ant != nil && ant.Incremental != nil {
		return *ant.Incremental
	}
	return def
}

// size returns the field size defined in the schema.
func (f Field) size() int64 {
	if ant := f.EntSQL(); ant != nil && ant.Size != 0 {
		return ant.Size
	}
	if f.def != nil && f.def.Size != nil {
		return *f.def.Size
	}
	return 0
}

// applyEntSQLDefaults overrides the column's default-value if an
// sqlschema.Default, DefaultExpr, or DefaultExprs annotation is set.
func (f Field) applyEntSQLDefaults(c *schema.Column) {
	switch ant := f.EntSQL(); {
	case ant == nil:
	case ant.Default != "":
		c.Default = schema.Expr(ant.Default)
	case ant.DefaultExpr != "":
		c.Default = schema.Expr(ant.DefaultExpr)
	case ant.DefaultExprs != nil:
		x := make(map[string]schema.Expr)
		for k, v := range ant.DefaultExprs {
			x[k] = schema.Expr(v)
		}
		c.Default = x
	}
}

// PK is like Column, but for table primary key.
func (f Field) PK() *schema.Column {
	c := &schema.Column{
		Name:      f.StorageKey(),
		Type:      f.Type.Type,
		Key:       schema.PrimaryKey,
		Comment:   f.sqlComment(),
		Increment: f.incremental(f.Type.Type.Integer()),
	}
	// If the PK was defined by the user, and it is UUID or string.
	if f.UserDefined && !f.Type.Numeric() {
		c.Increment = false
		c.Type = f.Type.Type
		c.Unique = f.Unique
		if f.def != nil && f.def.Size != nil {
			c.Size = *f.def.Size
		}
	}
	// Override with annotation default if present.
	f.applyEntSQLDefaults(c)

	// Override collation with annotation value.
	if ant := f.EntSQL(); ant != nil && ant.Collation != "" {
		c.Collation = ant.Collation
	}

	if f.def != nil {
		c.SchemaType = f.def.SchemaType
	}
	return c
}

// sqlComment returns the SQL database comment for the field, if defined and enabled.
func (f Field) sqlComment() string {
	fa, ta := f.EntSQL(), f.typ.EntSQL()
	switch c := f.Comment(); {
	// Field annotation gets precedence over type annotation.
	case fa != nil && fa.WithComments != nil:
		if *fa.WithComments {
			return c
		}
	case ta != nil && ta.WithComments != nil:
		if *ta.WithComments {
			return c
		}
	}
	return ""
}

// StorageKey returns the storage name of the field (SQL column name).
func (f Field) StorageKey() string {
	if f.def != nil && f.def.StorageKey != "" {
		return f.def.StorageKey
	}
	return snake(f.Name)
}

// HasGoType indicate if a basic field (like string or bool)
// has a custom GoType.
func (f Field) HasGoType() bool {
	return f.Type != nil && f.Type.RType != nil
}

// ConvertedToBasic indicates if the Go type of the field
// can be converted to basic type (string, int, etc.).
func (f Field) ConvertedToBasic() bool {
	return !f.HasGoType() || f.BasicType("ident") != ""
}

// SignedType returns the "signed type version" of the field type.
// This behavior is required for supporting addition/subtraction
// in mutations for unsigned types.
func (f Field) SignedType() (*field.TypeInfo, error) {
	if !f.SupportsMutationAdd() {
		return nil, fmt.Errorf("field %q does not support MutationAdd", f.Name)
	}
	t := *f.Type
	switch f.Type.Type {
	case field.TypeUint8:
		t.Type = field.TypeInt8
	case field.TypeUint16:
		t.Type = field.TypeInt16
	case field.TypeUint32:
		t.Type = field.TypeInt32
	case field.TypeUint64:
		t.Type = field.TypeInt64
	case field.TypeUint:
		t.Type = field.TypeInt
	}
	return &t, nil
}

// SupportsMutationAdd reports if the field supports the mutation "Add(T) T" interface.
func (f Field) SupportsMutationAdd() bool {
	if !f.Type.Numeric() || f.IsEdgeField() {
		return false
	}
	return f.ConvertedToBasic() || f.implementsAdder()
}

// MutationAddAssignExpr returns the expression for summing to identifiers and assigning to the mutation field.
//
//	MutationAddAssignExpr(a, b) => *m.a += b		// Basic Go type.
//	MutationAddAssignExpr(a, b) => *m.a = m.Add(b)	// Custom Go types that implement the (Add(T) T) interface.
func (f Field) MutationAddAssignExpr(ident1, ident2 string) (string, error) {
	if !f.SupportsMutationAdd() {
		return "", fmt.Errorf("field %q does not support the add operation (a + b)", f.Name)
	}
	expr := "*%s += %s"
	if f.implementsAdder() {
		expr = "*%[1]s = %[1]s.Add(%[2]s)"
	}
	return fmt.Sprintf(expr, ident1, ident2), nil
}

func (f Field) implementsAdder() bool {
	if !f.HasGoType() {
		return false
	}
	// If the custom GoType supports the "Add(T) T" interface.
	m, ok := f.Type.RType.Methods["Add"]
	if !ok || len(m.In) != 1 || len(m.Out) != 1 {
		return false
	}
	return rtypeEqual(f.Type.RType, m.In[0]) && rtypeEqual(f.Type.RType, m.Out[0])
}

func rtypeEqual(t1, t2 *field.RType) bool {
	return t1.Kind == t2.Kind && t1.Ident == t2.Ident && t1.PkgPath == t2.PkgPath
}

// SupportsMutationAppend reports if the field supports the mutation append operation.
func (f Field) SupportsMutationAppend() bool {
	return f.IsJSON() && f.Type.RType != nil && f.Type.RType.Kind == reflect.Slice
}

var (
	nullBoolType    = reflect.TypeFor[sql.NullBool]()
	nullBoolPType   = reflect.TypeFor[*sql.NullBool]()
	nullFloatType   = reflect.TypeFor[sql.NullFloat64]()
	nullFloatPType  = reflect.TypeFor[*sql.NullFloat64]()
	nullInt32Type   = reflect.TypeFor[sql.NullInt32]()
	nullInt32PType  = reflect.TypeFor[*sql.NullInt32]()
	nullInt64Type   = reflect.TypeFor[sql.NullInt64]()
	nullInt64PType  = reflect.TypeFor[*sql.NullInt64]()
	nullTimeType    = reflect.TypeFor[sql.NullTime]()
	nullTimePType   = reflect.TypeFor[*sql.NullTime]()
	nullStringType  = reflect.TypeFor[sql.NullString]()
	nullStringPType = reflect.TypeFor[*sql.NullString]()
)

// BasicType returns a Go expression for the given identifier
// to convert it to a basic type. For example:
//
//	v (http.Dir)		=> string(v)
//	v (fmt.Stringer)	=> v.String()
//	v (sql.NullString)	=> v.String
func (f Field) BasicType(ident string) (expr string) {
	if !f.HasGoType() {
		return ident
	}
	t, rt := f.Type, f.Type.RType
	switch t.Type {
	case field.TypeEnum:
		expr = ident
	case field.TypeBool:
		switch {
		case rt.Kind == reflect.Bool:
			expr = fmt.Sprintf("bool(%s)", ident)
		case rt.TypeEqual(nullBoolType) || rt.TypeEqual(nullBoolPType):
			expr = fmt.Sprintf("%s.Bool", ident)
		}
	case field.TypeBytes:
		if rt.Kind == reflect.Slice {
			expr = fmt.Sprintf("[]byte(%s)", ident)
		} else if rt.Kind == reflect.Array {
			expr = ident + "[:]"
		}
	case field.TypeTime:
		switch {
		case rt.TypeEqual(nullTimeType) || rt.TypeEqual(nullTimePType):
			expr = fmt.Sprintf("%s.Time", ident)
		case rt.Kind == reflect.Struct:
			expr = fmt.Sprintf("time.Time(%s)", ident)
		}
	case field.TypeString:
		switch {
		case rt.Kind == reflect.String:
			expr = fmt.Sprintf("string(%s)", ident)
		case t.Stringer():
			expr = fmt.Sprintf("%s.String()", ident)
		case rt.TypeEqual(nullStringType) || rt.TypeEqual(nullStringPType):
			expr = fmt.Sprintf("%s.String", ident)
		}
	case field.TypeJSON:
		expr = ident
	default:
		if t.Numeric() && rt.Kind >= reflect.Int && rt.Kind <= reflect.Float64 {
			expr = fmt.Sprintf("%s(%s)", rt.Kind, ident)
		}
	}
	return expr
}

// goType returns the Go expression for the given basic-type
// identifier to covert it to the custom Go type.
func (f Field) goType(ident string) string {
	if !f.HasGoType() {
		return ident
	}
	return fmt.Sprintf("%s(%s)", f.Type, ident)
}

func (f Field) enums(lf *load.Field) ([]Enum, error) {
	if len(lf.Enums) == 0 {
		return nil, fmt.Errorf("missing values for enum field %q", f.Name)
	}
	enums := make([]Enum, 0, len(lf.Enums))
	values := make(map[string]bool, len(lf.Enums))
	for i := range lf.Enums {
		switch name, value := f.EnumName(lf.Enums[i].N), lf.Enums[i].V; {
		case value == "":
			return nil, fmt.Errorf("%q field value cannot be empty", f.Name)
		case values[value]:
			return nil, fmt.Errorf("duplicate values %q for enum field %q", value, f.Name)
		case !token.IsIdentifier(name) && !f.HasGoType():
			return nil, fmt.Errorf("enum %q does not have a valid Go identifier (%q)", value, name)
		default:
			values[value] = true
			enums = append(enums, Enum{Name: name, Value: value})
		}
	}
	if value := lf.DefaultValue; value != nil {
		if value, ok := value.(string); !ok || !values[value] {
			return nil, fmt.Errorf("invalid default value for enum field %q", f.Name)
		}
	}
	return enums, nil
}

// Ops returns all predicate operations of the field.
func (f *Field) Ops() []Op {
	ops := fieldOps(f)
	if (f.Name != "id" || !f.HasGoType()) && f.cfg != nil && f.cfg.Storage != nil && f.cfg.Storage.Ops != nil {
		ops = append(ops, f.cfg.Storage.Ops(f)...)
	}
	return ops
}
