package sdl

import (
	"slices"

	"github.com/syssam/velox2/contrib/graphql"
	"github.com/syssam/velox2/plugin"
)

// Surface is the GraphQL surface of a model: the types velox.graphqls
// declares for its entities, with the names the SDL gives them. The Go code
// velox-graphql writes takes every name from here, so the Go and the SDL
// cannot drift.
type Surface struct {
	Types []*Type // the entities with a GraphQL type, in the SDL's order
}

// Type is an entity's GraphQL type, and the types named after it.
type Type struct {
	Entity *plugin.Entity
	// Name is the GraphQL type name. XConnection, XEdge, XOrder, XOrderField
	// and XWhereInput are named after it.
	Name string
	// Node reports whether the type implements Node: its key is one column,
	// whose ID is the field id.
	Node bool
	// Enums are the enum fields in GraphQL, in the model's field order.
	Enums []Enum
	// Order are the values of XOrderField, in the model's field order; nil
	// when the type has no order. With MultiOrder, orderBy takes a list.
	Order      []OrderValue
	MultiOrder bool
	// Where are the fields of XWhereInput, in the SDL's order; nil when the
	// type has no where input.
	Where []WhereField
}

// Enum is an enum field in GraphQL: an enum type named after the field's Go
// enum type, or a String of its stored values.
type Enum struct {
	// Name is the GraphQL enum's name, the field's Go enum type's name
	// (plugin.Field.Enum); "" when the field is a String, as
	// graphql.Skip(graphql.SkipEnumField) asks.
	Name string
	// Field is the index of the field in Entity.Fields.
	Field int
	// Values are the GraphQL enum values, one for each of the field's stored
	// values (plugin.Field.EnumValues), in their order; nil for a String.
	Values []string
}

// OrderValue is a value of XOrderField: the field it orders by.
type OrderValue struct {
	Value string // the enum value, as "NAME"
	Field int    // the index of the field in Entity.Fields
}

// WhereKind is what a field of a where input does.
type WhereKind int

// The kinds of where-input field.
const (
	WhereNot     WhereKind = iota // not: the input that must not hold
	WhereAnd                      // and: inputs that must all hold
	WhereOr                       // or: inputs of which one must hold
	WherePred                     // a predicate on a field
	WhereHas                      // has<Edge>: whether the edge has a node
	WhereHasWith                  // has<Edge>With: whether a node of the edge matches
)

// WhereField is a field of a where input.
type WhereField struct {
	// Name is the GraphQL field name, as "titleNEQ".
	Name string
	// GoName is the name of the Go struct field gqlgen binds it to, which
	// gqlgen matches to Name ignoring case: "TitleNEQ", or "ID" for "id".
	GoName string
	// Type is the field's GraphQL type, as "[String!]".
	Type string
	Kind WhereKind
	// Op is a WherePred's predicate.
	Op graphql.WhereOp
	// Field is a WherePred's field, its index in Entity.Fields; -1 for the
	// other kinds.
	Field int
	// Edge is the index in Entity.Edges of a WhereHas's or a WhereHasWith's
	// edge, or of the edge a foreign key's WherePred stands for; else -1.
	Edge int
	// Target is the GraphQL type at the other end: of the IDs a predicate
	// on IDs takes (the type itself for its key, the edge's target for a
	// foreign key), of the nodes of a WhereHas's or a WhereHasWith's edge,
	// and the type itself for not, and and or; "" for a predicate on a
	// field that is not an ID.
	Target string
}

// IDs reports whether the field is a predicate on IDs.
func (w WhereField) IDs() bool { return w.Kind == WherePred && w.Target != "" }

// Build computes the GraphQL surface of m. It fails as Render does on a
// model Render refuses, except for the check of the SDL with gqlparser.
func Build(m *plugin.Model) (*Surface, error) {
	g, err := build(m)
	if err != nil {
		return nil, err
	}
	s := &Surface{Types: make([]*Type, 0, len(g.objects))}
	for _, o := range g.objects {
		t := &Type{Entity: o.ent, Name: o.name, Node: o.node, MultiOrder: o.ann.MultiOrder, Where: o.whereGo}
		for _, en := range o.enums {
			i := slices.IndexFunc(o.ent.Fields, func(f *plugin.Field) bool { return f.Name == en.field })
			t.Enums = append(t.Enums, Enum{Name: en.name, Field: i, Values: en.values})
		}
		// Enums come as the type, the where input and the mutation inputs
		// first ask for them: put them in the fields' order.
		slices.SortStableFunc(t.Enums, func(a, b Enum) int { return a.Field - b.Field })
		if o.order != nil {
			for i, v := range o.order.values {
				t.Order = append(t.Order, OrderValue{Value: v, Field: o.order.fields[i]})
			}
		}
		s.Types = append(s.Types, t)
	}
	return s, nil
}

// goOf is the Go name gqlgen binds the GraphQL name to: goName, the Go name
// the GraphQL name was made from, when it was (camel(goName) is name);
// otherwise name with its first letter in upper case, as for a name
// graphql.FieldName gives.
func goOf(name, goName string) string {
	if camel(goName) == name {
		return goName
	}
	return upper(name)
}
