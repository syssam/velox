package sdl

import (
	"fmt"
	"slices"
	"strings"

	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/plugin"
)

// Surface is the GraphQL surface of a model: the types velox.graphql
// declares for its entities, with the names the SDL gives them. The Go code
// velox-graphql writes takes every name from here, so the Go and the SDL
// cannot drift.
type Surface struct {
	Types []*Type // the entities with a GraphQL type, in the SDL's order
	// Warnings say which M2M edges have a create or update input without
	// the field that adds links, as their edge schema has a column Add
	// cannot fill: "User.following: no add inputs; Follow.Note has no
	// default", once per edge.
	Warnings []string
}

// Type is an entity's GraphQL type, and the types named after it.
type Type struct {
	Entity *plugin.Entity
	// Name is the GraphQL type name. XConnection, XEdge, XOrder, XOrderField
	// and XWhereInput are named after it.
	Name string
	// HasID reports whether the type has the field id: its key is one
	// column, of a type an ID holds (an integer, a string, a UUID or an
	// enum). The ID is the key, in the key's own Go type.
	HasID bool
	// Key is, for a type with an ID, the base type of its key: the key
	// field's builtin type ("int64", "string"), or "[16]byte" for a UUID. A
	// column of another Go type that holds the key converts from it.
	Key string
	// KeyType is, for a type with an ID, the Go type of its IDs: the key
	// field's own ("int64", "uuid.UUID"), of the package KeyPkg.
	KeyType, KeyPkg string
	// Getters are the methods gqlgen binds fields to whose own Go type does
	// not bind to their scalar, in the model's field order.
	Getters []Getter
	// Enums are the enum fields in GraphQL, in the model's field order.
	Enums []Enum
	// Fields are the type's fields that are not edges, in the SDL's order:
	// id first for a type with an ID, then the entity's fields in the model's order.
	Fields []Field
	// Order are the values of XOrderField, in the model's field order; nil
	// when the type has no order. With MultiOrder, orderBy takes a list.
	Order      []OrderValue
	MultiOrder bool
	// Where are the fields of XWhereInput, in the SDL's order; nil when the
	// type has no where input.
	Where []WhereField
	// Edges are the type's edge fields, in the model's edge order: the edges
	// that are neither skipped nor to a type that is.
	Edges []Edge
	// Create and Update are the fields of CreateXInput and UpdateXInput, in
	// the SDL's order; nil when the type has no create or no update input.
	Create, Update []InputField
}

// InputKind is what a field of a mutation input does.
type InputKind int

// The kinds of mutation-input field.
const (
	InputValue     InputKind = iota // <field>: the field's value
	InputClear                      // clear<Field>: the field's column to NULL
	InputEdgeID                     // <edge>ID: a unique edge's node
	InputEdgeIDs                    // <edge>IDs, on create: a to-many edge's nodes
	InputAddIDs                     // add<Edge>IDs
	InputRemoveIDs                  // remove<Edge>IDs
	InputClearEdge                  // clear<Edge>
)

// InputField is a field of a mutation input.
type InputField struct {
	Name string // the GraphQL name, as "publishedAt"
	// GoName is the Go struct field gqlgen binds it to, which gqlgen matches
	// to Name ignoring case: goOf's rule, as "PublishedAt".
	GoName string
	Type   string // the GraphQL type, as "Time" or "[ID!]"
	Kind   InputKind
	Field  int    // InputValue, InputClear: the index in Entity.Fields; else -1
	Edge   int    // the edge kinds: the index in Entity.Edges; else -1
	Target string // the edge kinds: the GraphQL type of the IDs; else ""
	// GoType is an InputValue's Go type, as WhereField.GoType gives it, and
	// "veloxgql.JSON" for a JSON field; for the ID kinds, the Go type of
	// Target's IDs (Type.KeyType); "" for the other kinds.
	GoType   string
	GoPkg    string // the import path GoType names; "" for none
	Required bool   // non-null in GraphQL: a value in Go, not a pointer
}

// EdgeKind is how an edge is in GraphQL.
type EdgeKind int

// The kinds of edge field.
const (
	EdgeUnique     EdgeKind = iota // a unique edge: its node, or null
	EdgeList                       // an edge with graphql.Connection(false): a list of its nodes
	EdgeConnection                 // a connection to the edge's nodes
)

// Field is a field of a type that is not an edge: id, whose value is the
// key's, or one of the entity's fields, through a getter or not.
type Field struct {
	Name  string // the GraphQL name
	Field int    // the index in Entity.Fields: the key's for id
}

// Edge is an edge field of a type, which gqlgen binds to the entity's method
// Method, as the SDL's @goField says. A connection's arguments are its
// Target's: paging, then orderBy when Target has an Order and where when it
// has a where input.
type Edge struct {
	Edge   int    // the index of the edge in Entity.Edges
	Name   string // the GraphQL name of the field
	Kind   EdgeKind
	Target *Type  // the type of the edge's nodes
	Method string // the edge's Go name, the method gqlgen binds the field to
	// MaxList is a list edge's graphql.MaxList; 0 when it has none.
	MaxList int
}

// Enum is an enum field in GraphQL: an enum type named after the field's Go
// enum type, or a String of its stored values.
type Enum struct {
	// Name is the GraphQL enum's name: for velox-gen's Go enum type, that
	// type's name (plugin.Field.Enum); for an application's own Go enum
	// type, which @goModel binds, <Schema><Field>, the name velox-gen's would
	// have; "" when the field is a String, as
	// graphql.Skip(graphql.SkipEnumField) asks.
	Name string
	// Field is the index of the field in Entity.Fields.
	Field int
	// Values are the GraphQL enum values, one for each stored value of
	// velox-gen's Go enum type (plugin.Enum.Values), in its order, or of the
	// field's (plugin.Field.EnumValues) for an application's own type; nil
	// for a String.
	Values []string
	// Shared is set when an earlier field of the same velox-gen Go enum type
	// declares the GraphQL enum and its marshalers: velox-gen gives two
	// fields one type when their enum types get one name and hold the same
	// values.
	Shared bool
}

// Getter is a method that returns a field's value as the Go type its scalar
// binds to: the SDL binds the field to it with @goField(name: Method). A
// JSON field's getter marshals the value to veloxgql.JSON; any other
// converts it to Returns.
type Getter struct {
	Field   int    // the index of the field in Entity.Fields
	Method  string // <GoName>GQL: the struct has a field of the GoName
	Returns string // the Go type it returns, as "veloxgql.Time" or "string"
	Pkg     string // the import path of Returns' package; "" for a builtin type
}

// JSON reports whether the getter marshals a JSON field.
func (g Getter) JSON() bool { return g.Returns == "veloxgql.JSON" }

// OrderValue is a value of XOrderField: the field it orders by.
type OrderValue struct {
	Value string // the enum value, as "NAME"
	Field int    // the index of the field in Entity.Fields, or in Target.Fields when Edge is set
	// Edge is the index in Entity.Edges, plus one, of the to-one edge whose
	// target's field this orders by (graphql.OrderVia); 0 for the entity's own.
	Edge int
	// Target is that edge's target entity; nil when Edge is 0.
	Target *plugin.Entity
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
	// On is the GraphQL name of the field or edge the field is about, as
	// "displayName" for displayNameNEQ and "writer" for writerIDIn or
	// hasWriter; the type's name for not, and and or.
	On string
	// GoType is the Go type of a WherePred's values, as the generated
	// package names it once GoPkg is imported: the field's own Go type when
	// it binds to the scalar ("time.Time", "int8", "PostStatus"), else the
	// one the scalar binds to ("string" for a string field of the
	// application's own Go type); the Go type of Target's IDs for IDs
	// (Type.KeyType), and "bool" for IsNil, NotNil and has<Edge>.
	GoType string
	GoPkg  string // the import path GoType names; "" for none
}

// IDs reports whether the field is a predicate on IDs.
func (w WhereField) IDs() bool { return w.Kind == WherePred && w.Target != "" }

// Build computes the GraphQL surface of m. It fails as Render does on a
// model Render refuses, except for the check of the SDL with gqlparser.
func Build(m *plugin.Model) (*Surface, error) { return BuildConfig(m, Config{}) }

// BuildConfig is Build under the options cfg.
func BuildConfig(m *plugin.Model, cfg Config) (*Surface, error) {
	g, err := build(m, cfg)
	if err != nil {
		return nil, err
	}
	s := &Surface{Types: make([]*Type, 0, len(g.objects)), Warnings: g.warnings}
	types := make(map[*object]*Type, len(g.objects))
	for _, o := range g.objects {
		types[o] = &Type{}
	}
	for _, o := range g.objects {
		t := types[o]
		*t = Type{Entity: o.ent, Name: o.name, HasID: o.hasID, Key: o.key, KeyType: o.keyTyp, KeyPkg: o.keyPkg, Getters: o.getters, MultiOrder: o.ann.MultiOrder, Where: o.whereGo,
			Create: o.createGo, Update: o.updateGo, Fields: o.plain}
		for _, en := range o.enums {
			i := slices.IndexFunc(o.ent.Fields, func(f *plugin.Field) bool { return f.Name == en.field })
			t.Enums = append(t.Enums, Enum{Name: en.name, Field: i, Values: en.values, Shared: en.shared})
		}
		// Enums come as the type, the where input and the mutation inputs
		// first ask for them: put them in the fields' order.
		slices.SortStableFunc(t.Enums, func(a, b Enum) int { return a.Field - b.Field })
		if o.order != nil {
			for i, v := range o.order.values {
				t.Order = append(t.Order, OrderValue{Value: v, Field: o.order.fields[i], Edge: o.order.edges[i], Target: o.order.targets[i]})
			}
		}
		for _, ef := range o.edges {
			t.Edges = append(t.Edges, Edge{Edge: ef.edge, Name: ef.name, Kind: ef.kind, Target: types[ef.target], Method: o.ent.Edges[ef.edge].GoName, MaxList: o.eanns[ef.edge].MaxList})
		}
		s.Types = append(s.Types, t)
	}
	return s, nil
}

// goOf is the Go name gqlgen binds the GraphQL name to: goName, the Go name
// the GraphQL name was made from, when it was (camel(goName) is name, but
// for case);
// otherwise name with its first letter in upper case, as for a name
// graphql.FieldName gives.
func goOf(name, goName string) string {
	// Ignoring case, so that veloxe's customerId is CustomerID, as gqlgen
	// binds it.
	if strings.EqualFold(camel(goName), name) {
		return goName
	}
	return upper(name)
}

// The import paths of the Go types a scalar binds to.
const (
	veloxgqlPath = "github.com/syssam/velox/contrib/graphql/veloxgql"
	uuidPath     = "github.com/google/uuid"
)

// keyBase is the base Go type of the key of e, when e has an ID: its key is
// one column, of an integer, a string, a UUID or an enum type; "" when it
// has none.
func keyBase(e *plugin.Entity) string {
	if len(e.PrimaryKey) != 1 {
		return ""
	}
	switch k := e.Fields[e.PrimaryKey[0]]; k.Type {
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64":
		return k.Type
	case "string", "enum":
		return "string"
	case "uuid":
		return "[16]byte"
	}
	return ""
}

// keyGo is the Go type of the key of e, which has an ID, and the import path
// it names: the key field's own Go type, which gqlgen binds its IDs to.
func keyGo(e *plugin.Entity) (typ, pkg string) {
	k := e.Fields[e.PrimaryKey[0]]
	switch {
	case k.ElemType != "":
		return k.ElemType, k.PkgPath
	case k.Type == "uuid":
		return "uuid.UUID", uuidPath
	case k.Type == "enum":
		return k.Enum, ""
	}
	return k.Type, ""
}

// oneKeyType refuses a model whose types with an ID have keys of more than
// one Go type. An ID is the key in its own Go type, and gqlgen builds every
// argument of type ID from the first model gqlgen.yml lists for ID, so one
// Go type serves them all. An edge's IDs are its target's, a type with an
// ID, so the types cover them.
func (g *graph) oneKeyType() error {
	var first *object
	for _, o := range g.objects {
		if !o.hasID {
			continue
		}
		if first == nil {
			first = o
			continue
		}
		if o.keyTyp != first.keyTyp || o.keyPkg != first.keyPkg {
			return fmt.Errorf("schema %s's key is %s and schema %s's is %s: a GraphQL ID is the entity's key in its own Go type, and gqlgen binds every ID argument to one Go type, the first model gqlgen.yml lists for ID; give every schema with a GraphQL type keys of one Go type, or leave one out of GraphQL with graphql.Skip()",
				first.ent.Name, keyName(first), o.ent.Name, keyName(o))
		}
	}
	return nil
}

// keyName is the Go type of o's key, with its import path.
func keyName(o *object) string {
	if o.keyPkg == "" {
		return o.keyTyp
	}
	_, name, _ := strings.Cut(o.keyTyp, ".")
	return o.keyPkg + "." + name
}

// noID says why e has no ID.
func noID(e *plugin.Entity) string {
	if len(e.PrimaryKey) != 1 {
		return fmt.Sprintf("its key is %d columns", len(e.PrimaryKey))
	}
	return fmt.Sprintf("its key is of type %s, which an ID does not hold", e.Fields[e.PrimaryKey[0]].Type)
}

// goValue is how the values of a field are in Go where gqlgen binds them to
// the field's GraphQL type.
type goValue struct {
	typ, pkg string // the Go type, and the import path it names
	// own reports whether that is the field's own Go type, which binds to
	// its scalar through the models gqlgen.yml lists: velox's standard Go
	// types, velox-gen's enum types, and the application's enum types, which
	// bind with @goModel. A field of another Go type of the application's
	// binds through a getter, which returns getter, of the package
	// getterPkg.
	own               bool
	getter, getterPkg string
}

// goValue is the goValue of f, with the annotation a.
func (g *graph) goValue(f *plugin.Field, a graphql.Annotation) goValue {
	// A model that gives no ElemType (one written by hand) means velox's own.
	is := func(typ string) bool { return f.ElemType == typ || f.ElemType == "" }
	own := func(typ string) goValue { return goValue{typ: typ, own: is(typ)} }
	var v goValue
	switch f.Type {
	case "string", "bool", "int", "int8", "int16", "int32", "int64", "uint8", "uint16", "uint32", "float32", "float64":
		v = own(f.Type)
		v.getter = f.Type
	case "uint", "uint64":
		v = own(f.Type)
		v.getter, v.getterPkg = "veloxgql.Uint64", veloxgqlPath
	case "time":
		v = goValue{typ: "time.Time", pkg: "time", own: is("time.Time") && (f.PkgPath == "time" || f.PkgPath == "")}
		v.getter, v.getterPkg = "veloxgql.Time", veloxgqlPath
	case "uuid":
		// velox's UUID type is github.com/google/uuid's; a model from before
		// PkgPath names none.
		v = goValue{typ: "uuid.UUID", pkg: uuidPath, own: is("uuid.UUID") && (f.PkgPath == uuidPath || f.PkgPath == "")}
		v.getter, v.getterPkg = "veloxgql.UUID", veloxgqlPath
	case "bytes":
		v = own("[]byte")
		v.getter, v.getterPkg = "veloxgql.Bytes", veloxgqlPath
	case "json":
		v = goValue{getter: "veloxgql.JSON", getterPkg: veloxgqlPath}
		if jsonList[f.ElemType] != "" {
			v = goValue{typ: f.ElemType, own: true}
		}
	case "enum":
		switch {
		case g.veloxEnum(f):
			v = goValue{typ: f.Enum, own: true}
		case a.Skip&graphql.SkipEnumField != 0:
			// A String of the application's enum type, which has no
			// marshalers of velox's.
			v = goValue{typ: "string", getter: "string"}
		default:
			v = goValue{typ: f.ElemType, pkg: f.PkgPath, own: true}
		}
	}
	if a.Type != "" && !v.own && f.ElemType != "" {
		// graphql.Type: the application's own type, which its models bind
		// to the field's Go type, as veloxe had it.
		switch {
		case f.PkgPath != "":
			v = goValue{typ: f.ElemType, pkg: f.PkgPath, own: true}
		case len(f.Imports) == 0:
			v = goValue{typ: f.ElemType, own: true}
		case len(f.Imports) == 1:
			v = goValue{typ: f.ElemType, pkg: f.Imports[0], own: true}
		}
	}
	return v
}
