package gen

import (
	"fmt"

	"github.com/syssam/velox/dialect/sqlschema"
	"github.com/syssam/velox/schema/edge"
)

// =============================================================================
// Edge methods
// =============================================================================

// Label returns the label name of the edge (owner_edgename format).
func (e Edge) Label() string {
	if e.IsInverse() {
		return fmt.Sprintf("%s_%s", e.Owner.Label(), snake(e.Inverse))
	}
	return fmt.Sprintf("%s_%s", e.Owner.Label(), snake(e.Name))
}

// Constant returns the constant name of the edge.
func (e Edge) Constant() string {
	return "Edge" + pascal(e.Name)
}

// M2M indicates if this edge is M2M edge.
func (e Edge) M2M() bool { return e.Rel.Type == M2M }

// M2O indicates if this edge is M2O edge.
func (e Edge) M2O() bool { return e.Rel.Type == M2O }

// O2M indicates if this edge is O2M edge.
func (e Edge) O2M() bool { return e.Rel.Type == O2M }

// O2O indicates if this edge is O2O edge.
func (e Edge) O2O() bool { return e.Rel.Type == O2O }

// IsInverse returns if this edge is an inverse edge.
func (e Edge) IsInverse() bool { return e.Inverse != "" }

// LabelConstant returns the constant name of the edge label.
// If the edge is inverse, it returns the constant name of the owner-edge (assoc-edge).
func (e Edge) LabelConstant() string {
	name := e.Name
	if e.IsInverse() {
		name = e.Inverse
	}
	return pascal(name) + "Label"
}

// InverseLabelConstant returns the inverse constant name of the edge.
func (e Edge) InverseLabelConstant() string { return pascal(e.Name) + "InverseLabel" }

// TableConstant returns the constant name of the relation table.
// The value id Edge.Rel.Table, which is table that holds the relation/edge.
func (e Edge) TableConstant() string { return pascal(e.Name) + "Table" }

// InverseTableConstant returns the constant name of the other/inverse type of the relation.
func (e Edge) InverseTableConstant() string { return pascal(e.Name) + "InverseTable" }

// ColumnConstant returns the constant name of the relation column.
func (e Edge) ColumnConstant() string { return pascal(e.Name) + "Column" }

// PKConstant returns the constant name of the primary key. Used for M2M edges.
func (e Edge) PKConstant() string { return pascal(e.Name) + "PrimaryKey" }

// HasConstraint indicates if this edge has a unique constraint check.
// We check uniqueness when both-directions are unique or one of them.
func (e Edge) HasConstraint() bool {
	return e.Rel.Type == O2O || e.Rel.Type == O2M
}

// BuilderField returns the struct member of the edge in the builder.
func (e Edge) BuilderField() string {
	return builderField(e.Name)
}

// EagerLoadField returns the struct field (of query builder)
// for storing the eager-loading info.
func (e Edge) EagerLoadField() string {
	return "with" + e.StructField()
}

// EagerLoadNamedField returns the struct field (of query builder)
// for storing the eager-loading info for named edges.
func (e Edge) EagerLoadNamedField() string {
	return "withNamed" + e.StructField()
}

// StructField returns the struct member of the edge in the model.
func (e Edge) StructField() string {
	return pascal(e.Name)
}

// OwnFK indicates if the foreign-key of this edge is owned by the edge
// column (reside in the type's table). Used by the SQL storage-driver.
func (e Edge) OwnFK() bool {
	switch {
	case e.M2O():
		return true
	case e.O2O() && (e.IsInverse() || e.Bidi):
		return true
	}
	return false
}

// ForeignKey returns the foreign-key of the inverse-field.
func (e *Edge) ForeignKey() (*ForeignKey, error) {
	if e.Rel.fk != nil {
		return e.Rel.fk, nil
	}
	return nil, fmt.Errorf("foreign-key was not found for edge %q of type %s", e.Name, e.Rel.Type)
}

// Field returns the field that was referenced in the schema. For example:
//
//	edge.From("owner", User.Type).
//		Ref("pets").
//		Field("owner_id")
//
// Note that the zero value is returned if no field was defined in the schema.
func (e Edge) Field() *Field {
	if !e.OwnFK() {
		return nil
	}
	if fk, err := e.ForeignKey(); err == nil && fk.Field.IsEdgeField() {
		return fk.Field
	}
	return nil
}

// Comment returns the comment of the edge.
func (e Edge) Comment() string {
	if e.def != nil {
		return e.def.Comment
	}
	return ""
}

// HasFieldSetter reports if this edge already has a field-edge setters for its mutation API.
// It's used by the codegen templates to avoid generating duplicate setters for id APIs (e.g. SetOwnerID).
func (e Edge) HasFieldSetter() bool {
	if !e.OwnFK() {
		return false
	}
	fk, err := e.ForeignKey()
	if err != nil {
		return false
	}
	return fk.UserDefined && fk.Field.MutationSet() == e.MutationSet()
}

// MutationSet returns the method name for setting the edge id.
func (e Edge) MutationSet() string {
	return "Set" + pascal(e.Name) + "ID"
}

// MutationAdd returns the method name for adding edge ids.
func (e Edge) MutationAdd() string {
	return "Add" + pascal(rules.Singularize(e.Name)) + "IDs"
}

// MutationReset returns the method name for resetting the edge value.
// The default name is "Reset<EdgeName>". If the method conflicts
// with the mutation methods, suffix the method with "Edge".
func (e Edge) MutationReset() string {
	name := "Reset" + pascal(e.Name)
	if _, ok := mutMethods[name]; ok {
		name += "Edge"
	}
	return name
}

// MutationClear returns the method name for clearing the edge value.
// The default name is "Clear<EdgeName>". If the method conflicts
// with the mutation methods, suffix the method with "Edge".
func (e Edge) MutationClear() string {
	name := "Clear" + pascal(e.Name)
	if _, ok := mutMethods[name]; ok {
		name += "Edge"
	}
	return name
}

// MutationRemove returns the method name for removing edge ids.
func (e Edge) MutationRemove() string {
	return "Remove" + pascal(rules.Singularize(e.Name)) + "IDs"
}

// MutationCleared returns the method name for indicating if the edge
// was cleared in the mutation. The default name is "<EdgeName>Cleared".
// If the method conflicts with the mutation methods, add "Edge" the
// after the edge name.
func (e Edge) MutationCleared() string {
	name := pascal(e.Name) + "Cleared"
	if _, ok := mutMethods[name]; ok {
		return pascal(e.Name) + "EdgeCleared"
	}
	return name
}

// OrderCountName returns the function/option name for ordering by the edge count.
func (e Edge) OrderCountName() (string, error) {
	if e.Unique {
		return "", fmt.Errorf("edge %q is unique", e.Name)
	}
	return fmt.Sprintf("By%sCount", pascal(e.Name)), nil
}

// OrderTermsName returns the function/option name for ordering by any term.
func (e Edge) OrderTermsName() (string, error) {
	if e.Unique {
		return "", fmt.Errorf("edge %q is unique", e.Name)
	}
	return fmt.Sprintf("By%s", pascal(e.Name)), nil
}

// OrderFieldName returns the function/option name for ordering by edge field.
func (e Edge) OrderFieldName() (string, error) {
	if !e.Unique {
		return "", fmt.Errorf("edge %q is not-unique", e.Name)
	}
	return fmt.Sprintf("By%sField", pascal(e.Name)), nil
}

// setStorageKey sets the storage-key option in the schema or fail.
func (e *Edge) setStorageKey() error {
	key, err := e.StorageKey()
	if err != nil || key == nil {
		return err
	}
	switch rel := e.Rel; {
	case key.Table != "" && rel.Type != M2M:
		return fmt.Errorf("StorageKey.Table is allowed only for M2M edges (got %s)", e.Rel.Type)
	case len(key.Columns) == 1 && rel.Type == M2M:
		return fmt.Errorf("%s edge have 2 columns. Use edge.Columns(to, from) instead", e.Rel.Type)
	case len(key.Columns) > 1 && rel.Type != M2M:
		return fmt.Errorf("%s edge does not have 2 columns. Use edge.Column(%s) instead", e.Rel.Type, key.Columns[0])
	}
	if key.Table != "" {
		e.Rel.Table = key.Table
	}
	// Safely update columns, ensuring the slice has enough capacity
	if len(key.Columns) > 0 {
		if len(e.Rel.Columns) == 0 {
			e.Rel.Columns = make([]string, 1)
		}
		e.Rel.Columns[0] = key.Columns[0]
	}
	if len(key.Columns) > 1 {
		if len(e.Rel.Columns) < 2 {
			// Ensure capacity for 2 columns (M2M edge)
			newCols := make([]string, 2)
			if len(e.Rel.Columns) > 0 {
				newCols[0] = e.Rel.Columns[0]
			}
			e.Rel.Columns = newCols
		}
		e.Rel.Columns[1] = key.Columns[1]
	}
	return nil
}

// StorageKey returns the storage-key defined on the schema if exists.
func (e Edge) StorageKey() (*edge.StorageKey, error) {
	key := e.def.StorageKey
	if !e.IsInverse() {
		return key, nil
	}
	assoc, ok := e.Owner.HasAssoc(e.Inverse)
	if !ok || assoc.def.StorageKey == nil {
		return key, nil
	}
	// Assoc/To edge found with storage-key configured.
	if key != nil {
		return nil, fmt.Errorf("multiple storage-keys defined for edge %q<->%q", e.Name, assoc.Name)
	}
	return assoc.def.StorageKey, nil
}

// EntSQL returns the EntSQL annotation if exists.
func (e Edge) EntSQL() *sqlschema.Annotation {
	return sqlAnnotate(e.Annotations)
}

// Index returns the index of the edge in the schema.
// Used mainly to extract its position in the "loadedTypes" array.
func (e Edge) Index() (int, error) {
	// "owner" is the type that holds the edge.
	owner := e.Owner
	if e.IsInverse() {
		owner = e.Ref.Type
	}
	for i, e1 := range owner.Edges {
		if e1.Name == e.Name {
			return i, nil
		}
	}
	return 0, fmt.Errorf("edge %q was not found in its owner schema %q", e.Name, e.Owner.Name)
}

// =============================================================================
// Relation methods
// =============================================================================

// Column returns the first element from the columns slice.
func (r Relation) Column() string {
	if len(r.Columns) == 0 {
		panic(fmt.Sprintf("velox/gen: missing column for Relation (table=%q, type=%v) - ensure edge has proper FK column defined", r.Table, r.Type))
	}
	return r.Columns[0]
}

// =============================================================================
// ForeignKey methods
// =============================================================================

// StructField returns the struct member of the foreign-key in the generated model.
func (f ForeignKey) StructField() string {
	if f.UserDefined {
		return f.Field.StructField()
	}
	return f.Field.Name
}

// =============================================================================
// Rel type
// =============================================================================

// Rel is a relation type of an edge.
type Rel int

// Relation types.
const (
	Unk Rel = iota // Unknown.
	O2O            // One to one / has one.
	O2M            // One to many / has many.
	M2O            // Many to one (inverse perspective for O2M).
	M2M            // Many to many.
)

// String returns the relation name.
func (r Rel) String() string {
	s := "Unknown"
	switch r {
	case O2O:
		s = "O2O"
	case O2M:
		s = "O2M"
	case M2O:
		s = "M2O"
	case M2M:
		s = "M2M"
	}
	return s
}
