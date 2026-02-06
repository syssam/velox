package gen

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/token"
	"go/types"
	"path"
	"reflect"
	"sort"
	"strings"
	"unicode"

	"github.com/syssam/velox/compiler/load"
	"github.com/syssam/velox/dialect/sqlschema"
	entschema "github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
)

// The following types and their exported methods used by the codegen
// to generate the assets.
type (
	// Type represents one node-type in the graph, its relations and
	// the information it holds.
	Type struct {
		*Config
		schema *load.Schema
		// Name holds the type/ent name.
		Name string
		// alias, or local package name of the generated package.
		// Empty means no alias.
		alias string
		// ID holds the ID field of this type.
		ID *Field
		// Fields holds all the primitive fields of this type.
		Fields []*Field
		fields map[string]*Field
		// Edge holds all the edges of this type.
		Edges []*Edge
		// Indexes are the configured indexes for this type.
		Indexes []*Index
		// ForeignKeys are the foreign-keys that resides in the type table.
		ForeignKeys []*ForeignKey
		foreignKeys map[string]struct{}
		// Annotations that were defined for the field in the schema.
		// The mapping is from the Annotation.Name() to a JSON decoded object.
		Annotations Annotations
		// EdgeSchema indicates that this type (schema) is being used as an "edge schema".
		// The To and From fields holds references to the edges that go "through" this type.
		EdgeSchema struct {
			ID       []*Field
			To, From *Edge
		}
	}

	// Field holds the information of a type field used for the templates.
	Field struct {
		cfg *Config
		def *load.Field
		typ *Type
		// Name is the name of this field in the database schema.
		Name string
		// Type holds the type information of the field.
		Type *field.TypeInfo
		// Unique indicate if this field is a unique field.
		Unique bool
		// Optional indicates is this field is optional on create.
		Optional bool
		// Nillable indicates that this field can be null in the
		// database and pointer in the generated entities.
		Nillable bool
		// Default indicates if this field has a default value for creation.
		Default bool
		// Enums information for enum fields.
		Enums []Enum
		// UpdateDefault indicates if this field has a default value for update.
		UpdateDefault bool
		// Immutable indicates is this field cannot be updated.
		Immutable bool
		// StructTag of the field. default to "json".
		StructTag string
		// Validators holds the number of validators the field have.
		Validators int
		// Position info of the field.
		Position *load.Position
		// UserDefined indicates that this field was defined explicitly by the user in
		// the schema. Unlike the default id field, which is defined by the generator.
		UserDefined bool
		// Annotations that were defined for the field in the schema.
		// The mapping is from the Annotation.Name() to a JSON decoded object.
		Annotations Annotations
		// referenced foreign-key.
		fk *ForeignKey
	}

	// Edge of a graph between two types.
	Edge struct {
		def *load.Edge
		// Name holds the name of the edge.
		Name string
		// Type holds a reference to the type this edge is directed to.
		Type *Type
		// Optional indicates is this edge is optional on create.
		Optional bool
		// Immutable indicates is this edge cannot be updated.
		Immutable bool
		// Unique indicates if this edge is a unique edge.
		Unique bool
		// Inverse holds the name of the reference edge declared in the schema.
		Inverse string
		// Ref points to the reference edge. For Inverse edges (edge.From),
		// its points to the Assoc (edge.To). For Assoc edges, it points to
		// the inverse edge if it exists.
		Ref *Edge
		// Owner holds the type of the edge-owner. For assoc-edges it's the
		// type that holds the edge, for inverse-edges, it's the assoc type.
		Owner *Type
		// Through edge schema type.
		Through *Type
		// StructTag of the edge-field in the struct. default to "json".
		StructTag string
		// Relation holds the relation info of an edge.
		Rel Relation
		// Bidi indicates if this edge is a bidirectional edge. A self-reference
		// to the same type with the same name (symmetric relation). For example,
		// a User type have one of following edges:
		//
		//	edge.To("friends", User.Type)           // many 2 many.
		//	edge.To("spouse", User.Type).Unique()   // one 2 one.
		//
		Bidi bool
		// Annotations that were defined for the edge in the schema.
		// The mapping is from the Annotation.Name() to a JSON decoded object.
		Annotations Annotations
	}

	// Relation holds the relational database information for edges.
	Relation struct {
		// Type holds the relation type of the edge.
		Type Rel
		// Table holds the relation table for this edge.
		// For O2O and O2M, it's the table name of the type we're this edge point to.
		// For M2O, this is the owner's type, and for M2M this is the join table.
		Table string
		// Columns holds the relation column(s) in the relation table above.
		// For O2M, M2O and O2O, it contains one element with the column name.
		// For M2M edges, it contains two columns defined in the join table with
		// the same order as defined in the schema: (owner_id, reference_id).
		Columns []string
		// foreign-key information for non-M2M edges.
		fk *ForeignKey
	}

	// Index represents a database index used for either increasing speed
	// on database operations or defining constraints such as "UNIQUE INDEX".
	// Note that some indexes are created implicitly like table foreign keys.
	Index struct {
		// Name of the index. One column index is simply the column name.
		Name string
		// Unique index or not.
		Unique bool
		// Columns are the table columns.
		Columns []string
		// Annotations that were defined for the index in the schema.
		// The mapping is from the Annotation.Name() to a JSON decoded object.
		Annotations Annotations
	}

	// ForeignKey holds the information for foreign-key columns of types.
	// It's exported only because it's used by the codegen templates and
	// should not be used beside that.
	ForeignKey struct {
		// Field information for the foreign-key column.
		Field *Field
		// Edge that is associated with this foreign-key.
		Edge *Edge
		// UserDefined indicates that this foreign-key was defined explicitly as a field in the schema,
		// and was referenced by an edge. For example:
		//
		//	field.Int("owner_id").
		//		Optional()
		//
		//	edge.From("owner", User.Type).
		//		Ref("pets").
		//		Field("owner_id")
		//
		UserDefined bool
	}
	// Enum holds the enum information for schema enums in codegen.
	Enum struct {
		// Name is the Go name of the enum.
		Name string
		// Value in the schema.
		Value string
	}
)

// NewType creates a new type and its fields from the given schema.
func NewType(c *Config, schema *load.Schema) (*Type, error) {
	idType := c.IDType
	if idType == nil {
		idType = defaultIDType
	}
	typ := &Type{
		Config:      c,
		schema:      schema,
		Name:        schema.Name,
		Annotations: schema.Annotations,
		Fields:      make([]*Field, 0, len(schema.Fields)),
		fields:      make(map[string]*Field, len(schema.Fields)),
		foreignKeys: make(map[string]struct{}),
	}
	if !typ.IsView() {
		typ.ID = &Field{
			cfg:  c,
			typ:  typ,
			Name: "id",
			def: &load.Field{
				Name: "id",
			},
			Type:      idType,
			StructTag: structTag("id", ""),
		}
	}
	if err := ValidSchemaName(typ.Name); err != nil {
		return nil, err
	}
	for _, f := range schema.Fields {
		tf := &Field{
			cfg:           c,
			def:           f,
			typ:           typ,
			Name:          f.Name,
			Type:          f.Info,
			Unique:        f.Unique,
			Position:      f.Position,
			Nillable:      f.Nillable,
			Optional:      f.Optional,
			Default:       f.Default,
			UpdateDefault: f.UpdateDefault,
			Immutable:     f.Immutable,
			StructTag:     structTag(f.Name, f.Tag),
			Validators:    f.Validators,
			UserDefined:   true,
			Annotations:   f.Annotations,
		}
		if err := typ.checkField(tf, f); err != nil {
			return nil, err
		}
		// User defined id field.
		if typ.ID != nil && tf.Name == typ.ID.Name {
			switch {
			case tf.Optional:
				return nil, errors.New("id field cannot be optional")
			case f.ValueScanner:
				return nil, errors.New("id field cannot have an external ValueScanner")
			}
			typ.ID = tf
		} else {
			typ.Fields = append(typ.Fields, tf)
			typ.fields[f.Name] = tf
		}
	}
	return typ, nil
}

// =============================================================================
// Type methods
// =============================================================================

// IsView indicates if the type (schema) is a view.
func (t Type) IsView() bool {
	return t.schema != nil && t.schema.View
}

// IsEdgeSchema indicates if the type (schema) is used as an edge-schema.
// i.e. is being used by an edge (or its inverse) with edge.Through modifier.
func (t Type) IsEdgeSchema() bool {
	return t.EdgeSchema.To != nil || t.EdgeSchema.From != nil
}

// HasCompositeID indicates if the type has a composite ID field.
func (t Type) HasCompositeID() bool {
	return t.IsEdgeSchema() && len(t.EdgeSchema.ID) > 1
}

// HasOneFieldID indicates if the type has an ID with one field (not composite).
func (t Type) HasOneFieldID() bool {
	return !t.HasCompositeID() && t.ID != nil
}

// Label returns the label name of the node/type (snake_case).
func (t Type) Label() string {
	return snake(t.Name)
}

// Table returns SQL table name of the node/type.
func (t Type) Table() string {
	if ant := t.EntSQL(); ant != nil && ant.Table != "" {
		return ant.Table
	}
	if t.schema != nil && t.schema.Config.Table != "" {
		return t.schema.Config.Table
	}
	return snake(rules.Pluralize(t.Name))
}

// EntSQL returns the EntSQL annotation if exists.
func (t Type) EntSQL() *sqlschema.Annotation {
	return sqlAnnotate(t.Annotations)
}

// Package returns the package name of this node.
func (t Type) Package() string {
	if name := t.PackageAlias(); name != "" {
		return name
	}
	return t.PackageDir()
}

// PackageDir returns the name of the package directory.
func (t Type) PackageDir() string { return strings.ToLower(t.Name) }

// PackageAlias returns local package name of a type if there is one.
// A package has an alias if its generated name conflicts with
// one of the imports of the user-defined or ent builtin types.
func (t Type) PackageAlias() string { return t.alias }

// Receiver returns the receiver name of this node. It makes sure the
// receiver names doesn't conflict with import names.
func (t Type) Receiver() string {
	return "m"
}

// Pos returns the filename:line position information of this type in the schema.
func (t Type) Pos() string {
	return t.schema.Pos
}

// hasEdge returns true if this type as an edge (reverse or assoc)
// with the given name.
func (t Type) hasEdge(name string) bool {
	for _, e := range t.Edges {
		if name == e.Name {
			return true
		}
	}
	return false
}

// HasAssoc returns true if this type has an assoc-edge (edge.To)
// with the given name. faster than map access for most cases.
func (t Type) HasAssoc(name string) (*Edge, bool) {
	for _, e := range t.Edges {
		if name == e.Name && !e.IsInverse() {
			return e, true
		}
	}
	return nil, false
}

// HasValidators reports if any of the type's field has validators.
func (t Type) HasValidators() bool {
	fields := t.Fields
	if t.HasOneFieldID() && t.ID.UserDefined {
		fields = append(fields, t.ID)
	}
	for _, f := range fields {
		if f.Validators > 0 {
			return true
		}
	}
	return false
}

// HasDefault reports if any of this type's fields has default value on creation.
func (t Type) HasDefault() bool {
	fields := t.Fields
	if t.HasOneFieldID() && t.ID.UserDefined {
		fields = append(fields, t.ID)
	}
	for _, f := range fields {
		if f.Default {
			return true
		}
	}
	return false
}

// HasUpdateDefault reports if any of this type's fields has default value on update.
func (t Type) HasUpdateDefault() bool {
	for _, f := range t.Fields {
		if f.UpdateDefault {
			return true
		}
	}
	return false
}

// NeedsDefaults reports if this type needs a defaults() method.
// Returns true if any field needs a default value set:
// - Fields with explicit Default()
// - Optional fields (without Nillable) with standard Go types or custom types (TypeOther)
// Note: TypeEnum requires explicit Default() and won't use zero value.
func (t Type) NeedsDefaults() bool {
	fields := t.Fields
	if t.HasOneFieldID() && t.ID.UserDefined {
		fields = append(fields, t.ID)
	}
	for _, f := range fields {
		if f.Default {
			return true
		}
		// Optional fields with standard types or custom types (TypeOther) need zero value defaults
		// Only TypeEnum is excluded (requires explicit Default)
		if f.Optional && !f.Nillable && f.Type != nil && (f.Type.Type.IsStandardType() || f.Type.Type == field.TypeOther) {
			return true
		}
	}
	return false
}

// HasOptional reports if this type has an optional field.
func (t Type) HasOptional() bool {
	for _, f := range t.Fields {
		if f.Optional {
			return true
		}
	}
	return false
}

// HasNumeric reports if this type has a numeric field.
func (t Type) HasNumeric() bool {
	for _, f := range t.Fields {
		if f.Type.Numeric() {
			return true
		}
	}
	return false
}

// HasUpdateCheckers reports if this type has any checkers to run on update(one).
func (t Type) HasUpdateCheckers() bool {
	for _, f := range t.Fields {
		if (f.Validators > 0 || f.IsEnum()) && !f.Immutable {
			return true
		}
	}
	for _, e := range t.Edges {
		if e.Unique && !e.Optional {
			return true
		}
	}
	return false
}

// FKEdges returns all edges that reside on the type table as foreign-keys.
func (t Type) FKEdges() (edges []*Edge) {
	for _, e := range t.Edges {
		if e.OwnFK() {
			edges = append(edges, e)
		}
	}
	return
}

// EdgesWithID returns all edges that point to entities with non-composite identifiers.
// These types of edges can be created, updated and deleted by their identifiers.
func (t Type) EdgesWithID() (edges []*Edge) {
	for _, e := range t.Edges {
		if !e.Type.HasCompositeID() {
			edges = append(edges, e)
		}
	}
	return
}

// RuntimeMixin returns schema mixin that needs to be loaded at
// runtime. For example, for default values, validators or hooks.
func (t Type) RuntimeMixin() bool {
	return len(t.MixedInFields()) > 0 || len(t.MixedInHooks()) > 0 || len(t.MixedInPolicies()) > 0 || len(t.MixedInInterceptors()) > 0
}

// MixedInFields returns the indices of mixin holds runtime code.
func (t Type) MixedInFields() []int {
	idx := make(map[int]struct{})
	fields := t.Fields
	if t.HasOneFieldID() && t.ID.UserDefined {
		fields = append(fields, t.ID)
	}
	for _, f := range fields {
		if f.Position != nil && f.Position.MixedIn && (f.Default || f.UpdateDefault || f.Validators > 0) {
			idx[f.Position.MixinIndex] = struct{}{}
		}
	}
	return sortedKeys(idx)
}

// MixedInHooks returns the indices of mixin with hooks.
func (t Type) MixedInHooks() []int {
	if t.schema == nil {
		return nil
	}
	idx := make(map[int]struct{})
	for _, h := range t.schema.Hooks {
		if h.MixedIn {
			idx[h.MixinIndex] = struct{}{}
		}
	}
	return sortedKeys(idx)
}

// MixedInInterceptors returns the indices of mixin with interceptors.
func (t Type) MixedInInterceptors() []int {
	if t.schema == nil {
		return nil
	}
	idx := make(map[int]struct{})
	for _, h := range t.schema.Interceptors {
		if h.MixedIn {
			idx[h.MixinIndex] = struct{}{}
		}
	}
	return sortedKeys(idx)
}

// MixedInPolicies returns the indices of mixin with policies.
func (t Type) MixedInPolicies() []int {
	if t.schema == nil {
		return nil
	}
	idx := make(map[int]struct{})
	for _, h := range t.schema.Policy {
		if h.MixedIn {
			idx[h.MixinIndex] = struct{}{}
		}
	}
	return sortedKeys(idx)
}

// NumMixin returns the type's mixin count.
func (t Type) NumMixin() int {
	m := make(map[int]struct{})
	for _, f := range t.Fields {
		if p := f.Position; p != nil && p.MixedIn {
			m[p.MixinIndex] = struct{}{}
		}
	}
	return len(m)
}

// NumConstraint returns the type's constraint count. Used for slice allocation.
func (t Type) NumConstraint() int {
	var n int
	for _, f := range t.Fields {
		if f.Unique {
			n++
		}
	}
	for _, e := range t.Edges {
		if e.HasConstraint() {
			n++
		}
	}
	return n
}

// MutableFields returns all type fields that are mutable (on update).
func (t Type) MutableFields() []*Field {
	fields := make([]*Field, 0, len(t.Fields))
	for _, f := range t.Fields {
		if f.Immutable {
			continue
		}
		if e, err := f.Edge(); err == nil && e.Immutable {
			continue
		}
		fields = append(fields, f)
	}
	return fields
}

// ImmutableFields returns all type fields that are immutable (for update).
func (t Type) ImmutableFields() []*Field {
	fields := make([]*Field, 0, len(t.Fields))
	for _, f := range t.Fields {
		if f.Immutable {
			fields = append(fields, f)
		}
	}
	return fields
}

// MutationFields returns all the fields that are available on the typed-mutation.
func (t Type) MutationFields() []*Field {
	fields := make([]*Field, 0, len(t.Fields))
	for _, f := range t.Fields {
		if !f.IsEdgeField() {
			fields = append(fields, f)
		}
	}
	return fields
}

// EnumFields returns the enum fields of the schema, if any.
func (t Type) EnumFields() []*Field {
	var fields []*Field
	for _, f := range t.Fields {
		if f.IsEnum() {
			fields = append(fields, f)
		}
	}
	return fields
}

// FieldBy returns the first field that the given function returns true on it.
func (t Type) FieldBy(fn func(*Field) bool) (*Field, bool) {
	if fn(t.ID) {
		return t.ID, true
	}
	for _, f := range t.Fields {
		if fn(f) {
			return f, true
		}
	}
	return nil, false
}

// NumM2M returns the type's many-to-many edge count
func (t Type) NumM2M() int {
	var n int
	for _, e := range t.Edges {
		if e.M2M() {
			n++
		}
	}
	return n
}

// TagTypes returns all struct-tag types of the type fields.
// The result is sorted for deterministic output.
func (t Type) TagTypes() []string {
	tags := make(map[string]bool)
	for _, f := range t.Fields {
		tag := reflect.StructTag(f.StructTag)
		fields := strings.FieldsFunc(f.StructTag, func(r rune) bool {
			return r == ':' || unicode.IsSpace(r)
		})
		for _, name := range fields {
			_, ok := tag.Lookup(name)
			if ok && !tags[name] {
				tags[name] = true
			}
		}
	}
	r := make([]string, 0, len(tags))
	for tag := range tags {
		r = append(r, tag)
	}
	sort.Strings(r)
	return r
}

// AddIndex adds a new index for the type.
// It fails if the schema index is invalid.
func (t *Type) AddIndex(idx *load.Index) error {
	index := &Index{Name: idx.StorageKey, Unique: idx.Unique, Annotations: idx.Annotations}
	if len(idx.Fields) == 0 && len(idx.Edges) == 0 {
		return errors.New("missing fields or edges")
	}
	switch ant := sqlIndexAnnotate(idx.Annotations); {
	case ant == nil:
	case len(ant.PrefixColumns) != 0 && ant.Prefix != 0:
		return fmt.Errorf("index %q cannot contain both sqlschema.Prefix and sqlschema.PrefixColumn in annotation", index.Name)
	case ant.Prefix != 0 && len(idx.Fields)+len(idx.Edges) != 1:
		return fmt.Errorf("sqlschema.Prefix is used in a multicolumn index %q. Use sqlschema.PrefixColumn instead", index.Name)
	case len(ant.PrefixColumns) > len(idx.Fields)+len(idx.Fields):
		return fmt.Errorf("index %q has more sqlschema.PrefixColumn than column in its definitions", index.Name)
	}
	for _, name := range idx.Fields {
		var f *Field
		if t.HasOneFieldID() && name == t.ID.Name {
			f = t.ID
		} else if f = t.fields[name]; f == nil {
			return fmt.Errorf("unknown index field %q", name)
		}
		index.Columns = append(index.Columns, f.StorageKey())
	}
	for _, name := range idx.Edges {
		var ed *Edge
		for _, e := range t.Edges {
			if e.Name == name {
				ed = e
				break
			}
		}
		switch {
		case ed == nil:
			return fmt.Errorf("unknown index edge %q", name)
		case ed.Rel.Type == O2O && !ed.IsInverse():
			return fmt.Errorf("non-inverse edge (edge.From) for index %q on O2O relation", name)
		case ed.Rel.Type != M2O && ed.Rel.Type != O2O:
			return fmt.Errorf("relation %s for inverse edge %q is not one of (O2O, M2O)", ed.Rel.Type, name)
		default:
			index.Columns = append(index.Columns, ed.Rel.Column())
		}
	}
	// If no storage-key was defined for this index, generate one.
	if idx.StorageKey == "" {
		// Add the type name as a prefix to the index parts, because
		// multiple types can share the same index attributes.
		parts := append([]string{strings.ToLower(t.Name)}, index.Columns...)
		index.Name = strings.Join(parts, "_")
	}
	t.Indexes = append(t.Indexes, index)
	return nil
}

// setupFKs makes sure all edge-fks are created for the edges.
func (t *Type) setupFKs() error {
	for _, e := range t.Edges {
		if err := e.setStorageKey(); err != nil {
			return fmt.Errorf("%q edge: %w", e.Name, err)
		}
		if ef := e.def.Field; ef != "" && !e.OwnFK() {
			return fmt.Errorf("edge %q has a field %q but it is not holding a foreign key", e.Name, ef)
		}
		if e.IsInverse() || e.M2M() {
			continue
		}
		owner, refid := t, e.Type.ID
		if !e.OwnFK() {
			owner, refid = e.Type, t.ID
		}
		fk := &ForeignKey{
			Edge: e,
			Field: &Field{
				typ:         owner,
				Name:        builderField(e.Rel.Column()),
				Type:        refid.Type,
				Nillable:    e.Optional, // FK is nillable only if edge is optional
				Optional:    e.Optional,
				Unique:      e.Unique,
				UserDefined: refid.UserDefined,
			},
		}
		// Update the foreign-key/edge-field info of the assoc-edge.
		e.Rel.fk = fk
		if edgeField := e.def.Field; edgeField != "" {
			if err := owner.setupFieldEdge(fk, e, edgeField); err != nil {
				return err
			}
		}
		// Update inverse-edge info as well (optional).
		if ref := e.Ref; ref != nil {
			ref.Rel.fk = fk
			if edgeField := e.Ref.def.Field; edgeField != "" {
				if err := owner.setupFieldEdge(fk, e.Ref, edgeField); err != nil {
					return err
				}
			}
		}
		// Special case for checking if the FK is already defined as the ID field (Issue 1288).
		if key, _ := e.StorageKey(); key != nil && len(key.Columns) == 1 && key.Columns[0] == refid.StorageKey() {
			fk.Field = refid
			fk.UserDefined = true
		}
		owner.addFK(fk)
		// In case the user wants to set the column name using the StorageKey option, make sure they
		// do it using the edge-field option if both back-ref edge and field are defined (Issue 1288).
		if e.def.StorageKey != nil && len(e.def.StorageKey.Columns) > 0 && !e.OwnFK() && e.Ref != nil && e.Type.fields[e.Rel.Column()] != nil {
			return fmt.Errorf(
				"column %q definition on edge %[2]q should be replaced with Field(%[1]q) on its reference %[3]q",
				e.Rel.Column(), e.Name, e.Ref.Name,
			)
		}
	}
	return nil
}

// setupFieldEdge check the field-edge validity and configures it and its foreign-key.
func (t *Type) setupFieldEdge(fk *ForeignKey, fkOwner *Edge, fkName string) error {
	tf, ok := t.fields[fkName]
	if !ok {
		return fmt.Errorf("field %q was not found in %s.Fields() for edge %q", fkName, t.Name, fkOwner.Name)
	}
	switch fkField, ok := t.fields[fkName]; {
	case !ok:
		return fmt.Errorf("field %q was not found in %s.Fields() for edge %q", fkName, t.Name, fkOwner.Name)
	case fkField.Optional && !fkOwner.Optional:
		return fmt.Errorf("edge-field %q was set as Optional, but edge %q is not", fkName, fkOwner.Name)
	case !fkField.Optional && fkOwner.Optional:
		return fmt.Errorf("edge %q was set as Optional, but edge-field %q is not", fkOwner.Name, fkName)
	case fkField.Immutable && !fkOwner.Immutable:
		return fmt.Errorf("edge-field %q was set as Immutable, but edge %q is not", fkName, fkOwner.Name)
	case !fkField.Immutable && fkOwner.Immutable:
		return fmt.Errorf("edge %q was set as Immutable, but edge-field %q is not", fkOwner.Name, fkName)
	case fkField.HasValueScanner():
		return fmt.Errorf("edge-field %q cannot have an external ValueScanner", fkName)
	}
	if t1, t2 := tf.Type.Type, fkOwner.Type.ID.Type.Type; t1 != t2 {
		return fmt.Errorf("mismatch field type between edge field %q and id of type %q (%s != %s)", fkName, fkOwner.Type.Name, t1, t2)
	}
	fk.UserDefined = true
	tf.fk, fk.Field = fk, tf
	ekey, err := fkOwner.StorageKey()
	if err != nil {
		return err
	}
	if ekey != nil && len(ekey.Columns) == 1 {
		if fkey := tf.def.StorageKey; fkey != "" && fkey != ekey.Columns[0] {
			return fmt.Errorf("mismatch storage-key for edge %q and field %q", fkOwner.Name, fkName)
		}
		// Update the field storage key.
		tf.def.StorageKey = ekey.Columns[0]
	}
	fkOwner.Rel.Columns = []string{tf.StorageKey()}
	if ref := fkOwner.Ref; ref != nil {
		ref.Rel.Columns = []string{tf.StorageKey()}
	}
	return nil
}

// addFK adds a foreign-key for the type if it doesn't exist.
func (t *Type) addFK(fk *ForeignKey) {
	if _, ok := t.foreignKeys[fk.Field.Name]; ok {
		return
	}
	t.foreignKeys[fk.Field.Name] = struct{}{}
	t.ForeignKeys = append(t.ForeignKeys, fk)
}

// ClientName returns the struct name denoting the client of this type.
func (t Type) ClientName() string {
	return pascal(t.Name) + "Client"
}

// QueryName returns the struct name denoting the query-builder for this type.
func (t Type) QueryName() string {
	return pascal(t.Name) + "Query"
}

// QueryReceiver returns the receiver name of the query-builder for this type.
// Matches Ent's convention of using "_q" for all query builders.
func (t Type) QueryReceiver() string {
	return "_q"
}

// FilterName returns the struct name denoting the filter-builder for this type.
func (t Type) FilterName() string {
	return pascal(t.Name) + "Filter"
}

// CreateName returns the struct name denoting the create-builder for this type.
func (t Type) CreateName() string {
	return pascal(t.Name) + "Create"
}

// CreateReceiver returns the receiver name of the create-builder for this type.
// Matches Ent's convention of using "_c" for all create builders.
func (t Type) CreateReceiver() string {
	return "_c"
}

// CreateBulkName returns the struct name denoting the create-bulk-builder for this type.
func (t Type) CreateBulkName() string {
	return pascal(t.Name) + "CreateBulk"
}

// CreateBulReceiver returns the receiver name of the create-bulk-builder for this type.
// Matches Ent's convention of using "_c" for create-bulk (same as CreateReceiver).
func (t Type) CreateBulReceiver() string {
	return "_c"
}

// UpdateName returns the struct name denoting the update-builder for this type.
func (t Type) UpdateName() string {
	return pascal(t.Name) + "Update"
}

// UpdateReceiver returns the receiver name of the update-builder for this type.
// Matches Ent's convention of using "_u" for all update builders.
func (t Type) UpdateReceiver() string {
	return "_u"
}

// UpdateOneName returns the struct name denoting the update-one-builder for this type.
func (t Type) UpdateOneName() string {
	return pascal(t.Name) + "UpdateOne"
}

// UpdateOneReceiver returns the receiver name of the update-one-builder for this type.
// Matches Ent's convention of using "_u" for all update-one builders (same as UpdateReceiver).
func (t Type) UpdateOneReceiver() string {
	return "_u"
}

// DeleteName returns the struct name denoting the delete-builder for this type.
func (t Type) DeleteName() string {
	return pascal(t.Name) + "Delete"
}

// DeleteReceiver returns the receiver name of the delete-builder for this type.
// Matches Ent's convention of using "_d" for all delete builders.
func (t Type) DeleteReceiver() string {
	return "_d"
}

// DeleteOneName returns the struct name denoting the delete-one-builder for this type.
func (t Type) DeleteOneName() string {
	return pascal(t.Name) + "DeleteOne"
}

// DeleteOneReceiver returns the receiver name of the delete-one-builder for this type.
// Matches Ent's convention of using "_d" for delete-one (same as DeleteReceiver).
func (t Type) DeleteOneReceiver() string {
	return "_d"
}

// MutationName returns the struct name of the mutation builder for this type.
func (t Type) MutationName() string {
	return pascal(t.Name) + "Mutation"
}

// MutationOptionName returns the name of the mutation option type.
// Uses lowercase prefix like Ent (e.g., "abtesteventOption") since it's internal.
func (t Type) MutationOptionName() string {
	return strings.ToLower(t.Name) + "Option"
}

// GroupReceiver returns the receiver name of the group-by builder for this type.
// Matches Ent's convention of using "_g" for all group-by builders.
func (t Type) GroupReceiver() string {
	return "_g"
}

// SelectReceiver returns the receiver name of the selector builder for this type.
// Matches Ent's convention of using "_s" for all select builders.
func (t Type) SelectReceiver() string {
	return "_s"
}

// TypeName returns the constant name of the type defined in mutation.go.
func (t Type) TypeName() string {
	return "Type" + pascal(t.Name)
}

// ValueName returns the name of the value method for this type.
func (t Type) ValueName() string {
	if t.fields["Value"] == nil && t.fields["value"] == nil {
		return "Value"
	}
	return "GetValue"
}

// SiblingImports returns all sibling packages that are needed for the different builders.
func (t Type) SiblingImports() []struct{ Alias, Path string } {
	var (
		imports = []struct{ Alias, Path string }{{Alias: t.PackageAlias(), Path: path.Join(t.Config.Package, t.PackageDir())}}
		seen    = map[string]bool{imports[0].Path: true}
	)
	for _, e := range t.Edges {
		p := path.Join(t.Config.Package, e.Type.PackageDir())
		if !seen[p] {
			seen[p] = true
			imports = append(imports, struct{ Alias, Path string }{Alias: e.Type.PackageAlias(), Path: p})
		}
	}
	return imports
}

// NumHooks returns the number of hooks declared in the type schema.
func (t Type) NumHooks() int {
	if t.schema != nil {
		return len(t.schema.Hooks)
	}
	return 0
}

// HookPositions returns the position information of hooks declared in the type schema.
func (t Type) HookPositions() []*load.Position {
	if t.schema != nil {
		return t.schema.Hooks
	}
	return nil
}

// NumInterceptors returns the number of interceptors declared in the type schema.
func (t Type) NumInterceptors() int {
	if t.schema != nil {
		return len(t.schema.Interceptors)
	}
	return 0
}

// InterceptorPositions returns the position information of interceptors declared in the type schema.
func (t Type) InterceptorPositions() []*load.Position {
	if t.schema != nil {
		return t.schema.Interceptors
	}
	return nil
}

// NumPolicy returns the number of privacy-policy declared in the type schema.
func (t Type) NumPolicy() int {
	if t.schema != nil {
		return len(t.schema.Policy)
	}
	return 0
}

// PolicyPositions returns the position information of privacy policy declared in the type schema.
func (t Type) PolicyPositions() []*load.Position {
	if t.schema != nil {
		return t.schema.Policy
	}
	return nil
}

// RelatedTypes returns all the types (nodes) that
// are related (with edges) to this type.
func (t Type) RelatedTypes() []*Type {
	seen := make(map[string]struct{})
	related := make([]*Type, 0, len(t.Edges))
	for _, e := range t.Edges {
		if _, ok := seen[e.Type.Name]; !ok {
			related = append(related, e.Type)
			seen[e.Type.Name] = struct{}{}
		}
	}
	return related
}

// ValidSchemaName will determine if a name is going to conflict with any
// pre-defined names or contains unsafe characters.
func ValidSchemaName(name string) error {
	// Check for empty name.
	if name == "" {
		return errors.New("schema name cannot be empty")
	}
	// Check for path traversal characters to prevent directory escape attacks.
	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("schema name %q contains path separator characters", name)
	}
	if strings.Contains(name, "..") {
		return fmt.Errorf("schema name %q contains parent directory reference", name)
	}
	// Check for hidden files (names starting with dot).
	if strings.HasPrefix(name, ".") {
		return fmt.Errorf("schema name %q cannot start with a dot", name)
	}
	// Validate that the name is a valid Go identifier.
	if !token.IsIdentifier(name) {
		return fmt.Errorf("schema name %q is not a valid Go identifier", name)
	}
	// Schema package is lower-cased (see Type.Package).
	pkg := strings.ToLower(name)
	if token.Lookup(pkg).IsKeyword() {
		return fmt.Errorf("schema lowercase name conflicts with Go keyword %q", pkg)
	}
	if types.Universe.Lookup(pkg) != nil {
		return fmt.Errorf("schema lowercase name conflicts with Go predeclared identifier %q", pkg)
	}
	if _, ok := globalIdent[pkg]; ok {
		return fmt.Errorf("schema lowercase name conflicts ent predeclared identifier %q", pkg)
	}
	if _, ok := globalIdent[name]; ok {
		return fmt.Errorf("schema name conflicts with ent predeclared identifier %q", name)
	}
	return nil
}

// checkField checks the schema field.
func (t *Type) checkField(tf *Field, f *load.Field) (err error) {
	switch ant := tf.EntSQL(); {
	case f.Name == "":
		err = fmt.Errorf("field name cannot be empty")
	case f.Info == nil || !f.Info.Valid():
		err = fmt.Errorf("invalid type for field %s", f.Name)
	case f.Unique && f.Default && f.DefaultKind != reflect.Func:
		err = fmt.Errorf("unique field %q cannot have default value", f.Name)
	case t.fields[f.Name] != nil:
		err = fmt.Errorf("field %q redeclared for type %q", f.Name, t.Name)
	case f.Sensitive && f.Tag != "":
		err = fmt.Errorf("sensitive field %q cannot have struct tags", f.Name)
	case f.Info.Type == field.TypeEnum:
		if tf.Enums, err = tf.enums(f); err == nil && !tf.HasGoType() {
			// Enum types should be named as follows: typepkg.Field.
			f.Info.Ident = fmt.Sprintf("%s.%s", t.PackageDir(), pascal(f.Name))
		}
	case tf.Validators > 0 && !tf.ConvertedToBasic() && f.Info.Type != field.TypeJSON:
		err = fmt.Errorf("GoType %q for field %q must be converted to the basic %q type for validators", tf.Type, f.Name, tf.Type.Type)
	case ant != nil && ant.Default != "" && (ant.DefaultExpr != "" || ant.DefaultExprs != nil):
		err = fmt.Errorf("field %q cannot have both default value and default expression annotations", f.Name)
	case tf.HasValueScanner() && tf.IsJSON():
		err = fmt.Errorf("json field %q cannot have an external ValueScanner", f.Name)
	case f.Optional && !f.Nillable && !f.Default && f.Name != "id" && f.Info.Type == field.TypeEnum:
		// Enum fields require explicit Default value because empty string "" is not a valid enum value.
		// All other types (standard Go types and custom types) can use their zero values.
		// Note: id field has its own check in NewType ("id field cannot be optional").
		err = fmt.Errorf("optional enum field %q requires Default() value (empty string is not a valid enum value)", f.Name)
	}
	return err
}

// UnexportedForeignKeys returns all foreign-keys that belong to the type
// but are not exported (not defined with field). i.e. generated by velox.
func (t Type) UnexportedForeignKeys() []*ForeignKey {
	fks := make([]*ForeignKey, 0, len(t.ForeignKeys))
	for _, fk := range t.ForeignKeys {
		if !fk.UserDefined {
			fks = append(fks, fk)
		}
	}
	return fks
}

// aliases adds package aliases (local names) for all type-packages that
// their import identifier conflicts with user-defined packages (i.e. GoType).
func aliases(g *Graph) {
	mayAlias := make(map[string]*Type)
	for _, n := range g.Nodes {
		if pkg := n.PackageDir(); importPkg[pkg] != "" {
			// By default, a package named "pet" will be named as "entpet".
			n.alias = path.Base(g.Package) + pkg
		} else {
			mayAlias[n.PackageDir()] = n
		}
	}
	for _, n := range g.Nodes {
		for _, f := range n.Fields {
			if !f.HasGoType() {
				continue
			}
			name := f.Type.PkgName
			if name == "" && f.Type.PkgPath != "" {
				name = path.Base(f.Type.PkgPath)
			}
			// A user-defined type already uses the
			// package local name.
			if n, ok := mayAlias[name]; ok {
				// By default, a package named "pet" will be named as "entpet".
				n.alias = path.Base(g.Package) + name
			}
		}
	}
}

// sqlComment returns the SQL database comment for the node (table), if defined and enabled.
func (t Type) sqlComment() string {
	if ant := t.EntSQL(); ant == nil || ant.WithComments == nil || !*ant.WithComments {
		return ""
	}
	ant := &entschema.CommentAnnotation{}
	if t.Annotations == nil || t.Annotations[ant.Name()] == nil {
		return ""
	}
	if b, err := json.Marshal(t.Annotations[ant.Name()]); err == nil {
		_ = json.Unmarshal(b, &ant)
	}
	return ant.Text
}

// HasValueScanner reports if any of the fields has (an external) ValueScanner.
func (t Type) HasValueScanner() bool {
	for _, f := range t.Fields {
		if f.HasValueScanner() {
			return true
		}
	}
	return false
}

// DeprecatedFields returns all deprecated fields of the type.
func (t Type) DeprecatedFields() []*Field {
	fs := make([]*Field, 0, len(t.Fields))
	for _, f := range t.Fields {
		if f.IsDeprecated() {
			fs = append(fs, f)
		}
	}
	return fs
}

// EntSQL returns the EntSQL annotation if exists.
func (f Field) EntSQL() *sqlschema.Annotation {
	return sqlAnnotate(f.Annotations)
}
