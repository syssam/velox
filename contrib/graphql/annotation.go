package graphql

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"reflect"
	"slices"
	"strconv"

	"github.com/syssam/velox/schema"
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
// the directive's arguments, as JSON values. Directives, and Merge, copy each
// Args map but not the values in it: a nested map or slice stays shared with
// the caller's, so do not change it after.
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
	// OrderUnguarded allows OrderField on a field with an application
	// directive, which guards neither the cursors nor the order.
	OrderUnguarded bool `json:"order_unguarded,omitempty"`
	// OrderVia makes columns of a to-one edge's target orderable, on the
	// connections to the edge's entity.
	OrderVia []ViaOrder `json:"order_via,omitempty"`
	// WhereOps is the predicates the field offers.
	WhereOps WhereOp `json:"where_ops,omitempty"`
	// WhereEdge allows the has and hasWith filters on an edge.
	WhereEdge bool `json:"where_edge,omitempty"`
	// Connection says whether a to-many edge is a connection (the default) or
	// a plain list. Nil leaves the default.
	Connection *bool `json:"connection,omitempty"`
	// MaxList is the most rows a list edge reads for each parent, and what the cost
	// charges for each; zero leaves veloxgql.Limits.MaxList.
	MaxList int `json:"max_list,omitempty"`
	// CreateInput says whether a field is in the create input. Nil leaves the
	// default.
	CreateInput *bool `json:"create_input,omitempty"`
	// UpdateInput says whether a field is in the update input. Nil leaves the
	// default.
	UpdateInput *bool `json:"update_input,omitempty"`
	// CreateDefault is the value the create input gives a bool or number field
	// it leaves out, as JSON.
	CreateDefault json.RawMessage `json:"create_default,omitempty"`
	// EnumValues maps an enum field's database values to GraphQL value names.
	EnumValues map[string]string `json:"enum_values,omitempty"`
	// Directives are put on the generated element.
	Directives []Directive `json:"directives,omitempty"`
}

// ViaOrder is one column of an edge's target that connections to the edge's
// entity can be ordered by: the target's field, under this enum value name.
type ViaOrder struct {
	// Field is the schema name of the target's field, such as "name".
	Field string `json:"field"`
	// Value is the XOrderField enum value, such as "AUTHOR_NAME".
	Value string `json:"value"`
}

// Name returns "graphql".
func (Annotation) Name() string { return "graphql" }

// Merge combines the annotation with other, an earlier one of the same element
// (velox's loader calls later.Merge(earlier)), field by field:
//   - bitmasks (Skip, WhereOps) are ORed;
//   - lists (Mutations, Directives, OrderVia) are other's followed by the receiver's,
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
		Skip:           o.Skip | a.Skip,
		Type:           pick(a.Type, o.Type),
		QueryField:     pick(a.QueryField, o.QueryField),
		MultiOrder:     o.MultiOrder || a.MultiOrder,
		FieldName:      pick(a.FieldName, o.FieldName),
		OrderField:     pick(a.OrderField, o.OrderField),
		OrderUnguarded: o.OrderUnguarded || a.OrderUnguarded,
		OrderVia:       slices.Concat(o.OrderVia, a.OrderVia),
		WhereOps:       o.WhereOps | a.WhereOps,
		WhereEdge:      o.WhereEdge || a.WhereEdge,
		Connection:     pickPtr(a.Connection, o.Connection),
		MaxList:        cmp.Or(a.MaxList, o.MaxList),
		CreateInput:    pickPtr(a.CreateInput, o.CreateInput),
		UpdateInput:    pickPtr(a.UpdateInput, o.UpdateInput),
		CreateDefault:  pickRaw(a.CreateDefault, o.CreateDefault),
	}
	for _, k := range slices.Concat(o.Mutations, a.Mutations) {
		if !slices.Contains(out.Mutations, k) {
			out.Mutations = append(out.Mutations, k)
		}
	}
	out.Directives = slices.Concat(o.Directives, a.Directives)
	for i := range out.Directives {
		out.Directives[i].Args = maps.Clone(out.Directives[i].Args)
	}
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

// pickRaw is a copy of later if it is set, else of earlier.
func pickRaw(later, earlier json.RawMessage) json.RawMessage {
	if later != nil {
		return slices.Clone(later)
	}
	return slices.Clone(earlier)
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

// Type names the entity's GraphQL type. An ID and a cursor hold the type's
// name, so renaming the type changes every ID and cursor that clients hold.
// veloxgql.DecodeID refuses an ID of the old name with
// veloxgql.ErrWrongIDType.
func Type(name string) Annotation { return Annotation{Type: name} }

// QueryField adds a root Query field, with this name, that returns the
// entity's connection.
func QueryField(name string) Annotation { return Annotation{QueryField: name} }

// Mutations generates the listed mutations for the entity; with no arguments,
// create and update.
func Mutations(kinds ...MutationKind) Annotation {
	if len(kinds) == 0 {
		kinds = []MutationKind{MutationCreate, MutationUpdate}
	}
	return Annotation{Mutations: slices.Clone(kinds)}
}

// The kinds of mutation.
const (
	MutationCreate MutationKind = "create"
	MutationUpdate MutationKind = "update"
	MutationDelete MutationKind = "delete"
)

// MultiOrder lets the entity's connection order by a list of fields.
func MultiOrder() Annotation { return Annotation{MultiOrder: true} }

// FieldName names a field or an edge in GraphQL.
func FieldName(name string) Annotation { return Annotation{FieldName: name} }

// OrderField makes a field orderable, with this name for its order enum value.
func OrderField(name string) Annotation { return Annotation{OrderField: name} }

// OrderUnguarded allows OrderField on a field with an application directive,
// one other than GraphQL's own. Such a directive guards the field and its
// filters, but not the order: the field's values would be in the cursors, and
// the order of the nodes would reveal them. Without OrderUnguarded the plugin
// refuses the pair. It applies to a field with OrderField only.
func OrderUnguarded() Annotation { return Annotation{OrderUnguarded: true} }

// OrderVia makes a field of a to-one edge's target orderable, on the
// connections to the edge's entity, under this enum value name:
//
//	edge.To("author", User.Type).Unique().Annotations(graphql.OrderVia("name", "AUTHOR_NAME"))
//
// orderBy: {field: AUTHOR_NAME} then orders a Post connection by its author's name,
// paged by cursors that hold it. The connection reads the edge with an inner JOIN, so
// a row whose target is missing, or that the target's Read policy hides from the
// caller, is not in the connection, nor in its totalCount, when it is ordered so. The
// plugin refuses it on a to-many edge, for a nullable, sensitive, json or bytes field
// or one the type leaves out, and for a field or an edge with an application directive,
// which guards neither the cursors nor the order. Call it once for each field.
func OrderVia(field, value string) Annotation {
	return Annotation{OrderVia: []ViaOrder{{Field: field, Value: value}}}
}

// WhereOps offers the field these predicates in the entity's where input; with
// no arguments, the defaults for the field's type. Two WhereOps annotations on
// one field merge by OR, so WhereOps() together with WhereOps(OpsString)
// offers the type's default predicates plus OpsString. On the node key it
// replaces the key's own predicates: WhereOps() gives the defaults of the
// key's kind (equality, and comparison for an integer or string key), and an
// op the key's kind lacks, OpsString or OpsComparison on a uuid key, is
// refused.
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

// MaxList lowers what a list edge (Connection(false)) reads for each parent from
// veloxgql.Limits.MaxList to n, and what the cost charges for each, so that lists nest
// under MaxCost. A parent with more fails the edge, as one over Limits.MaxList does, and
// no rows are dropped: give an edge that may hold more a Connection. Limits.MaxList still
// holds when it is the lower.
func MaxList(n int) Annotation { return Annotation{MaxList: n} }

// CreateInput puts the field in the create input (true) or leaves it out.
func CreateInput(on bool) Annotation { return Annotation{CreateInput: &on} }

// UpdateInput puts the field in the update input (true) or leaves it out.
func UpdateInput(on bool) Annotation { return Annotation{UpdateInput: &on} }

// CreateDefault is the value a create input gives a bool or number field that
// it leaves out: the SDL writes it as the input field's default, active:
// Boolean! = true, and the type keeps active: Boolean!. It is GraphQL's alone:
// the field has no Default, so a Go struct inserted without it stores false or
// 0, which a Default on a field that is not Nillable could not tell from
// unset. It applies to a bool or number field in the create input that is not
// nullable and has no Default.
func CreateDefault[T ~bool | ~int | ~int8 | ~int16 | ~int32 | ~int64 | ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~float32 | ~float64](v T) Annotation {
	r := reflect.ValueOf(v)
	var s string
	switch r.Kind() {
	case reflect.Bool:
		s = strconv.FormatBool(r.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		s = strconv.FormatInt(r.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		s = strconv.FormatUint(r.Uint(), 10)
	default: // a float
		f := r.Float()
		if math.IsNaN(f) || math.IsInf(f, 0) {
			s = strconv.Quote(strconv.FormatFloat(f, 'g', -1, 64)) // no number: the plugin refuses it
		} else {
			s = strconv.FormatFloat(f, 'g', -1, 64)
		}
	}
	return Annotation{CreateDefault: json.RawMessage(s)}
}

// EnumValues maps an enum field's database values to GraphQL value names.
func EnumValues(m map[string]string) Annotation {
	return Annotation{EnumValues: maps.Clone(m)}
}

// Directives puts these directives on the generated element: a field or an
// edge. An entity takes GraphQL's own directives only; the package doc's
// Directives section says why. It copies each Args map but not the values in
// it: a nested map or slice stays shared with the caller's, so do not change
// it after.
func Directives(ds ...Directive) Annotation {
	out := slices.Clone(ds)
	for i := range out {
		out[i].Args = maps.Clone(out[i].Args)
	}
	return Annotation{Directives: out}
}
