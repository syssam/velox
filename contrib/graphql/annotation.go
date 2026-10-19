package graphql

import (
	"fmt"
	"maps"
	"slices"

	"github.com/syssam/velox2/schema"
)

// SkipMode is a set of the parts of the GraphQL surface an element leaves out.
type SkipMode uint16

// The parts that can be skipped. They combine with |, and merge by OR.
const (
	// SkipType leaves out the entity's type (or the field or edge on it).
	SkipType SkipMode = 1 << iota
	// SkipEnumField leaves out the enum a field's values make.
	SkipEnumField
	// SkipOrderField leaves out the field's order.
	SkipOrderField
	// SkipWhereInput leaves out the where input.
	SkipWhereInput
	// SkipMutationCreateInput leaves out the create input.
	SkipMutationCreateInput
	// SkipMutationUpdateInput leaves out the update input.
	SkipMutationUpdateInput

	// SkipAll leaves out everything.
	SkipAll = SkipType | SkipEnumField | SkipOrderField | SkipWhereInput |
		SkipMutationCreateInput | SkipMutationUpdateInput
)

// WhereOp is a set of the predicates a field offers in its where input.
type WhereOp uint32

// The predicates. They combine with |, and merge by OR.
const (
	OpEQ WhereOp = 1 << iota
	OpNEQ
	OpIn
	OpNotIn
	OpGT
	OpGTE
	OpLT
	OpLTE
	OpContains
	OpHasPrefix
	OpHasSuffix
	OpIsNil
	OpNotNil

	// OpsEquality is equal, not equal, in and not in.
	OpsEquality = OpEQ | OpNEQ | OpIn | OpNotIn
	// OpsComparison is greater and less, strict or not.
	OpsComparison = OpGT | OpGTE | OpLT | OpLTE
	// OpsString is contains, has prefix and has suffix.
	OpsString = OpContains | OpHasPrefix | OpHasSuffix
	// OpsNullable is is-nil and not-nil.
	OpsNullable = OpIsNil | OpNotNil
	// OpsAll is every predicate.
	OpsAll = OpsEquality | OpsComparison | OpsString | OpsNullable

	// OpsDefault is what WhereOps() with no arguments records: the plugin
	// replaces it with the defaults for the field's type. Merged (ORed) with
	// explicit ops, it means the field type's default predicates plus those
	// ops. It is not part of OpsAll.
	OpsDefault WhereOp = 1 << 31
)

// MutationKind is one of the mutations generated for an entity.
type MutationKind string

// Directive is a GraphQL directive to put on the generated element. Args are
// the directive's arguments, as JSON values.
type Directive struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args,omitempty"`
}

// Annotation is the GraphQL annotation of a schema, field or edge. Which of its
// parts apply depends on the element: the plugin names the schema and element
// of one that is misplaced. Build one with the constructors of this package;
// the zero Annotation says nothing.
//
// Its JSON is what the plugin reads, so its field names are a contract.
type Annotation struct {
	// Skip is the parts of the surface left out.
	Skip SkipMode `json:"skip,omitempty"`
	// Type renames the entity's GraphQL type.
	Type string `json:"type,omitempty"`
	// QueryField is the root Query field that returns the entity's connection.
	QueryField string `json:"query_field,omitempty"`
	// Mutations lists the mutations to generate for the entity.
	Mutations []MutationKind `json:"mutations,omitempty"`
	// MultiOrder lets a connection order by a list of fields.
	MultiOrder bool `json:"multi_order,omitempty"`
	// FieldName renames a field or an edge in GraphQL.
	FieldName string `json:"field_name,omitempty"`
	// OrderField makes a field orderable, under this enum value name.
	OrderField string `json:"order_field,omitempty"`
	// WhereOps is the predicates the field offers.
	WhereOps WhereOp `json:"where_ops,omitempty"`
	// WhereEdge allows the has and hasWith filters on an edge.
	WhereEdge bool `json:"where_edge,omitempty"`
	// Connection says whether a to-many edge is a connection (the default) or
	// a plain list. Nil leaves the default.
	Connection *bool `json:"connection,omitempty"`
	// CreateInput says whether a field is in the create input. Nil leaves the
	// default.
	CreateInput *bool `json:"create_input,omitempty"`
	// UpdateInput says whether a field is in the update input. Nil leaves the
	// default.
	UpdateInput *bool `json:"update_input,omitempty"`
	// EnumValues maps an enum field's database values to GraphQL value names.
	EnumValues map[string]string `json:"enum_values,omitempty"`
	// Directives are put on the generated element.
	Directives []Directive `json:"directives,omitempty"`
}

// Name returns "graphql".
func (Annotation) Name() string { return "graphql" }

// Merge combines the annotation with other, an earlier one of the same element
// (velox2's loader calls later.Merge(earlier)), field by field:
//   - bitmasks (Skip, WhereOps) are ORed;
//   - lists (Mutations, Directives) are other's followed by the receiver's,
//     and a mutation kind listed twice is kept once;
//   - EnumValues keys are united;
//   - the receiver's non-zero scalars and non-nil pointers win over other's.
//
// Merge panics when other is not a graphql Annotation, which the loader turns
// into the schema's error: two annotations named "graphql" that are not this
// type cannot be combined.
func (a Annotation) Merge(other schema.Annotation) schema.Annotation {
	var o Annotation
	switch v := other.(type) {
	case Annotation:
		o = v
	case *Annotation:
		o = *v
	default:
		panic(fmt.Sprintf("graphql: cannot merge an annotation named \"graphql\" of type %T", other))
	}
	out := Annotation{
		Skip:        o.Skip | a.Skip,
		Type:        pick(a.Type, o.Type),
		QueryField:  pick(a.QueryField, o.QueryField),
		MultiOrder:  o.MultiOrder || a.MultiOrder,
		FieldName:   pick(a.FieldName, o.FieldName),
		OrderField:  pick(a.OrderField, o.OrderField),
		WhereOps:    o.WhereOps | a.WhereOps,
		WhereEdge:   o.WhereEdge || a.WhereEdge,
		Connection:  pickPtr(a.Connection, o.Connection),
		CreateInput: pickPtr(a.CreateInput, o.CreateInput),
		UpdateInput: pickPtr(a.UpdateInput, o.UpdateInput),
	}
	for _, k := range slices.Concat(o.Mutations, a.Mutations) {
		if !slices.Contains(out.Mutations, k) {
			out.Mutations = append(out.Mutations, k)
		}
	}
	out.Directives = slices.Concat(o.Directives, a.Directives)
	if len(o.EnumValues)+len(a.EnumValues) > 0 {
		out.EnumValues = make(map[string]string, len(o.EnumValues)+len(a.EnumValues))
		maps.Copy(out.EnumValues, o.EnumValues)
		maps.Copy(out.EnumValues, a.EnumValues)
	}
	return out
}

// pick is later if it is set, else earlier.
func pick(later, earlier string) string {
	if later != "" {
		return later
	}
	return earlier
}

func pickPtr(later, earlier *bool) *bool {
	var p *bool
	if later != nil {
		p = later
	} else {
		p = earlier
	}
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// Skip leaves out the given parts of the element's surface; with no
// arguments, all of it (SkipAll).
func Skip(modes ...SkipMode) Annotation {
	var m SkipMode
	for _, v := range modes {
		m |= v
	}
	if len(modes) == 0 {
		m = SkipAll
	}
	return Annotation{Skip: m}
}

// Type names the entity's GraphQL type.
func Type(name string) Annotation { return Annotation{Type: name} }

// QueryField adds a root Query field, with this name, that returns the
// entity's connection.
func QueryField(name string) Annotation { return Annotation{QueryField: name} }

// Mutations generates the listed mutations for the entity; with no arguments,
// create and update.
func Mutations(kinds ...MutationKind) Annotation {
	if len(kinds) == 0 {
		kinds = []MutationKind{Create(), Update()}
	}
	return Annotation{Mutations: slices.Clone(kinds)}
}

// Create is the create mutation.
func Create() MutationKind { return "create" }

// Update is the update mutation.
func Update() MutationKind { return "update" }

// Delete is the delete mutation.
func Delete() MutationKind { return "delete" }

// MultiOrder lets the entity's connection order by a list of fields.
func MultiOrder() Annotation { return Annotation{MultiOrder: true} }

// FieldName names a field or an edge in GraphQL.
func FieldName(name string) Annotation { return Annotation{FieldName: name} }

// OrderField makes a field orderable, with this name for its order enum value.
func OrderField(name string) Annotation { return Annotation{OrderField: name} }

// WhereOps offers the field these predicates in the entity's where input; with
// no arguments, the defaults for the field's type. Two WhereOps annotations on
// one field merge by OR, so WhereOps() together with WhereOps(OpsString)
// offers the type's default predicates plus OpsString.
func WhereOps(ops ...WhereOp) Annotation {
	var m WhereOp
	for _, op := range ops {
		m |= op
	}
	if len(ops) == 0 {
		m = OpsDefault
	}
	return Annotation{WhereOps: m}
}

// WhereEdge allows the has and hasWith filters on an edge.
func WhereEdge() Annotation { return Annotation{WhereEdge: true} }

// Connection makes a to-many edge a connection (true) or a plain list (false).
func Connection(on bool) Annotation { return Annotation{Connection: &on} }

// CreateInput puts the field in the create input (true) or leaves it out.
func CreateInput(on bool) Annotation { return Annotation{CreateInput: &on} }

// UpdateInput puts the field in the update input (true) or leaves it out.
func UpdateInput(on bool) Annotation { return Annotation{UpdateInput: &on} }

// EnumValues maps an enum field's database values to GraphQL value names.
func EnumValues(m map[string]string) Annotation {
	return Annotation{EnumValues: maps.Clone(m)}
}

// Directives puts these directives on the generated element.
func Directives(ds ...Directive) Annotation {
	return Annotation{Directives: slices.Clone(ds)}
}
