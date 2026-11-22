package sdl

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/plugin"
)

// graph is the GraphQL surface of a model.
type graph struct {
	objects []*object          // the entities with a type, in name order
	byName  map[string]*object // by entity name
	goEnums map[string]bool    // the enum types velox-gen declares
	shares  *enumShares        // the GraphQL enum of each of them, once a field claims it
	enumSet map[string]bool    // the GraphQL enums declared so far, which inputs may take
	appSet  map[string]bool    // the types graphql.Type names on fields, which the application may declare
	bytes   bool               // a field is of type bytes, whose scalar Bytes veloxe declared
	cfg     Config
	refused []error // nameBound's refusals under Config.GraphQLGo
	types   typeNames
	ents    map[string]*plugin.Entity // every entity, by name, join entities too
	query   []gqlField                // the QueryFields, in entity name order
	// warnings are the edges with an input that has no add, as their edge
	// schema has columns Add cannot fill; warned holds them by
	// "Entity.edge", so each is warned of once.
	warnings []string
	warned   map[string]bool
}

// object is an entity's type.
type object struct {
	ent    *plugin.Entity
	ann    graphql.Annotation
	fanns  []graphql.Annotation // the annotations of ent.Fields, by index
	eanns  []graphql.Annotation // the annotations of ent.Edges, by index
	name   string               // the GraphQL type name
	hasID  bool                 // its key is one column, of a type an ID holds: the field id
	key    string               // the base Go type of the key, for conversions; "" for no ID
	keyTyp string               // the key's own Go type, the Go type of its IDs; "" for no ID
	keyPkg string               // the import path keyTyp names; "" for none
	dirs   []directive          // the type's directives
	fields []gqlField           // fields, then edges
	plain  []Field              // the fields that are not edges, for the Go code
	enums  []gqlEnum
	fks    map[string]bool // the names of ent's foreign-key fields
	veloxe bool            // Config.Veloxe: veloxe's names, foreign keys in the type
	conn   bool            // it has a connection, XConnection and XEdge: always, but under Config.Veloxe only as veloxe had one (objectOf)

	getters  []Getter     // the getters gqlgen binds fields to
	edges    []edgeField  // the edge fields, in the model's edge order
	order    *order       // nil for none
	hasWhere bool         // it has a where input
	where    []gqlField   // the where input's fields
	whereGo  []WhereField // the where input's fields, for the Go code
	create   []gqlField   // the create input's fields; nil for none
	update   []gqlField   // the update input's fields; nil for none
	createGo []InputField // the create input's fields, for the Go code
	updateGo []InputField // the update input's fields, for the Go code
}

// edgeField is an edge field of an object.
type edgeField struct {
	edge   int    // the index in the entity's Edges
	name   string // the GraphQL field's name
	kind   EdgeKind
	target *object
}

type gqlField struct {
	name, args, typ, desc string
	def                   string // an input field's default, as " = true"; "" for none
	dirs                  []directive
	where                 string  // the element, for errors
	conn                  *object // for a connection, its nodes' type: args are set from it
	resolver              bool    // a graphql.Map's: its type may be the application's own, which the standalone check cannot see
}

// directive is a rendered directive.
type directive struct {
	name string // without the @
	text string // as written: @name(args)
}

type gqlEnum struct {
	name   string   // "" for a String: the field's graphql.Skip has SkipEnumField
	values []string // nil for a String
	field  string   // the field it is the type of
	model  string   // the Go type @goModel binds the enum to: the application's own enum type; "" for velox-gen's, which gqlgen binds by name
	goType string   // velox-gen's Go enum type, which name may be an alias of; "" for the application's
	shared bool     // another field of the same Go enum type declares the enum
}

// reserveGoNames records the Go names velox-gen declares in the generated
// package besides the ones the SDL derives its types from: each schema's
// handles variable (Users), each entity's type (join entities have no
// GraphQL type, and a schema's name is free only for itself), and each enum
// type, whether or not the SDL declares an enum for it.
func (g *graph) reserveGoNames(m *plugin.Model) {
	for _, e := range m.Entities {
		kind := "schema"
		if e.Join {
			kind = "join entity"
		}
		g.types.reserve(e.Name, "the Go type of "+kind+" "+e.Name)
		g.types.reserve(e.Handles, "the handles variable of "+kind+" "+e.Name)
	}
	for _, en := range m.Enums {
		g.types.reserve(en.Type, "the Go enum type "+en.Type+enumOwner(m, en.Type))
	}
}

// enumOwner says whose field the enum type is, as ", of schema S, field f".
func enumOwner(m *plugin.Model, typ string) string {
	for _, e := range m.Entities {
		for _, f := range e.Fields {
			if f.Enum == typ {
				return " of schema " + e.Name + ", field " + f.Name
			}
		}
	}
	return ""
}

// connArgs are the arguments of a connection to t's nodes: paging, and
// orderBy and where when t has an order and a where input.
func connArgs(t *object) string {
	return "(after: Cursor, first: Int, before: Cursor, last: Int" + orderArg(t) + whereArg(t) + ")"
}

// build returns the GraphQL surface of m under cfg.
func build(m *plugin.Model, cfg Config) (*graph, error) {
	if m == nil {
		return nil, errors.New("no model")
	}
	if cfg.GraphQLGo && cfg.Package == "" {
		return nil, errors.New("velox-graphql: engine=graphql-go binds every type to the generated package, and the request names none")
	}
	g := newGraph(m)
	g.cfg = cfg
	ents := slices.SortedStableFunc(slices.Values(m.Entities), func(a, b *plugin.Entity) int { return cmp.Compare(a.Name, b.Name) })
	fks := foreignKeys(m)

	// The types first, so that edges know their targets' names.
	for _, e := range ents {
		o, err := g.objectOf(e, fks[e.Name])
		if err != nil {
			return nil, err
		}
		if o == nil {
			continue
		}
		g.objects = append(g.objects, o)
		g.byName[e.Name] = o
	}

	if err := g.oneKeyType(); err != nil {
		return nil, err
	}

	queryBy := map[string]string{}
	for _, o := range g.objects {
		if err := g.fill(o); err != nil {
			return nil, err
		}
		if err := g.addQueryField(o, queryBy); err != nil {
			return nil, err
		}
	}
	if err := errors.Join(g.refused...); err != nil {
		return nil, err // every element engine=graphql-go refuses, in one run
	}

	// The orders and where inputs, which connections to an entity take; then
	// the mutations.
	for _, o := range g.objects {
		if err := g.orderOf(o); err != nil {
			return nil, err
		}
		o.hasWhere = g.wantsWhere(o)
	}
	for _, o := range g.objects {
		if err := g.whereOf(o); err != nil {
			return nil, err
		}
	}
	for _, o := range g.objects {
		if err := g.mutationsOf(o); err != nil {
			return nil, err
		}
	}
	setConnArgs(g.query)
	for i := range g.query {
		if g.query[i].conn != nil {
			g.query[i].desc = queryDesc(g.query[i].conn)
		}
	}
	for _, o := range g.objects {
		setConnArgs(o.fields)
	}
	return g, nil
}

// newGraph returns the graph of m with no objects yet: its entities and Go
// enum types indexed, and the Go names velox-gen declares reserved.
func newGraph(m *plugin.Model) *graph {
	g := &graph{byName: map[string]*object{}, goEnums: map[string]bool{}, enumSet: map[string]bool{}, appSet: map[string]bool{}, shares: newEnumShares(m), types: newTypeNames(), ents: map[string]*plugin.Entity{}}
	for _, e := range m.Entities {
		g.ents[e.Name] = e
	}
	for _, en := range m.Enums {
		g.goEnums[en.Type] = true
	}
	g.reserveGoNames(m)
	return g
}

// foreignKeys returns, by entity name, the names of its foreign-key fields
// that an edge is bound to.
//
// A foreign-key field bound to an edge is in no type and no input: the
// edge stands for it. An edge names its field on whichever entity holds
// it.
func foreignKeys(m *plugin.Model) map[string]map[string]bool {
	fks := map[string]map[string]bool{}
	for _, e := range m.Entities {
		for _, ed := range e.Edges {
			if ed.Field == "" || ed.FieldOn == "" {
				continue
			}
			if fks[ed.FieldOn] == nil {
				fks[ed.FieldOn] = map[string]bool{}
			}
			fks[ed.FieldOn][ed.Field] = true
		}
	}
	return fks
}

// objectOf returns e's type, whose foreign-key fields are fks, with its
// name, directives and annotations, and reserves its type names. It
// returns nil for a join entity or a skipped type.
func (g *graph) objectOf(e *plugin.Entity, fks map[string]bool) (*object, error) {
	where := "schema " + e.Name
	a, err := decode(e.Annotations, where)
	if err != nil {
		return nil, err
	}
	if err := checkSchema(e, a, g.cfg.Veloxe, where); err != nil {
		return nil, err
	}
	if e.Join || a.Skip&graphql.SkipType != 0 {
		// No SDL field comes of the entity's fields and edges, but
		// velox-graphql's own directives are refused on them as on
		// every element (ownDirective).
		return nil, ownDirectives(e, where)
	}
	o := &object{ent: e, ann: a, name: e.Name, key: keyBase(e), fks: fks, veloxe: g.cfg.Veloxe, conn: true}
	if o.veloxe {
		// veloxe's hasRelayConnection: a schema with a QueryField has a
		// connection only with RelayConnection; any other, always.
		o.conn = a.RelayConnection || !a.HasQueryField()
	}
	o.hasID = o.key != ""
	if o.hasID {
		o.keyTyp, o.keyPkg = keyGo(e)
	}
	if o.name, err = typeName(e, a, where); err != nil {
		return nil, err
	}
	if err := g.addTypeNames(o, where); err != nil {
		return nil, err
	}
	if o.dirs, err = directives(a.Directives, where); err != nil {
		return nil, err
	}
	if o.fanns, err = g.fieldAnnotations(o, where); err != nil {
		return nil, err
	}
	if o.eanns, err = edgeAnnotations(e, where); err != nil {
		return nil, err
	}
	for _, name := range a.WhereInputEdges {
		i := slices.IndexFunc(e.Edges, func(ed *plugin.Edge) bool { return ed.Name == name })
		if i < 0 {
			return nil, fmt.Errorf("%s: graphql.WhereInputEdges(%q): the schema has no such edge", where, name)
		}
		o.eanns[i].WhereEdge = true
	}
	return o, nil
}

// typeName returns the GraphQL name of e's type, which a names: the
// graphql.Type annotation's, or else e's name.
func typeName(e *plugin.Entity, a graphql.Annotation, where string) (string, error) {
	switch {
	case a.Type != "" && !validName(a.Type):
		return "", fmt.Errorf("%s: graphql.Type(%q) is not a GraphQL name", where, a.Type)
	case a.Type != "" && (a.Type[0] < 'A' || a.Type[0] > 'Z'):
		// The name is the Go alias's too, and its connection's and edge's: an
		// unexported one gqlgen cannot bind, and error or type do not build.
		return "", fmt.Errorf("%s: graphql.Type(%q) names Go types too, so it starts with an upper-case letter", where, a.Type)
	case a.Type != "":
		return a.Type, nil
	case !validName(e.Name):
		return "", fmt.Errorf("%s: %q is not a GraphQL name; name the type with graphql.Type", where, e.Name)
	}
	return e.Name, nil
}

// addTypeNames reserves the names of o's type, connection and edge types.
func (g *graph) addTypeNames(o *object, where string) error {
	for i, n := range []string{o.name, o.name + "Connection", o.name + "Edge"} {
		add := g.types.add
		if i == 0 && o.name == o.ent.Name {
			add = g.types.addGo // the schema's own Go type
		}
		if err := add(n, where); err != nil {
			return err
		}
	}
	return nil
}

// fieldAnnotations returns the checked annotations of o's entity's fields,
// by index. WhereInput is WhereOps(), added to the field's own WhereOps;
// under compat=veloxe an explicit WhereOps wins over it, as in veloxe, and
// the annotation is read as veloxe read it (veloxeAnnotation). With
// Config.WhereWhitelist a field other than the key without WhereInput has
// no predicates.
func (g *graph) fieldAnnotations(o *object, where string) ([]graphql.Annotation, error) {
	var anns []graphql.Annotation
	for i, f := range o.ent.Fields {
		fw := where + ", field " + f.Name
		fa, err := decode(f.Annotations, fw)
		if err != nil {
			return nil, err
		}
		pk := o.hasID && i == o.ent.PrimaryKey[0]
		switch {
		case g.cfg.WhereWhitelist && !fa.WhereInput && !pk:
			fa.WhereOps = 0
		case fa.WhereInput && (!o.veloxe || fa.WhereOps == 0):
			fa.WhereOps |= graphql.OpsDefault
		}
		if o.veloxe {
			fa = veloxeAnnotation(o, i, fa)
		}
		if err := checkField(o.ent, i, fa, o.fks[f.Name], o.veloxe, fw); err != nil {
			return nil, err
		}
		anns = append(anns, fa)
	}
	return anns, nil
}

// edgeAnnotations returns the checked annotations of e's edges, by index.
func edgeAnnotations(e *plugin.Entity, where string) ([]graphql.Annotation, error) {
	var anns []graphql.Annotation
	for _, ed := range e.Edges {
		ew := where + ", edge " + ed.Name
		ea, err := decode(ed.Annotations, ew)
		if err != nil {
			return nil, err
		}
		ea.WhereEdge = ea.WhereEdge || ea.WhereInput
		if err := ownDirective(ea.Directives, ew); err != nil {
			return nil, err
		}
		if err := misplaced(ea, onEdge, ew); err != nil {
			return nil, err
		}
		anns = append(anns, ea)
	}
	return anns, nil
}

// addQueryField adds o's QueryField, if it has one, to the root Query.
// queryBy holds the element that declares each Query field so far.
//
// Under compat=veloxe every type has one, as veloxe gave every type: named
// as QueryField names it or else by default, and a list of the type's
// objects, with no arguments, when the type has no connection.
func (g *graph) addQueryField(o *object, queryBy map[string]string) error {
	q := o.ann.QueryField
	if q == "" && (o.ann.QueryFieldDefault || o.veloxe) {
		q = queryName(o.name)
	}
	if q == "" {
		return nil
	}
	where := "schema " + o.ent.Name
	if !validName(q) {
		return fmt.Errorf("%s: graphql.QueryField(%q) is not a GraphQL name", where, q)
	}
	switch prev := queryBy[q]; prev {
	case "":
	case builtin:
		return fmt.Errorf("%s: the Query field %s is one velox-graphql declares itself", where, q)
	default:
		return fmt.Errorf("%s and %s both declare the Query field %s", prev, where, q)
	}
	queryBy[q] = where
	if !o.conn {
		g.query = append(g.query, gqlField{name: q, typ: "[" + o.name + "!]!", where: where,
			desc: "The " + o.name + " nodes, as the application's resolver returns them."})
		return nil
	}
	g.query = append(g.query, gqlField{name: q, typ: o.name + "Connection!", where: where, conn: o})
	return nil
}

// setConnArgs sets the arguments of each connection among fs.
func setConnArgs(fs []gqlField) {
	for i := range fs {
		if fs[i].conn != nil {
			fs[i].args = connArgs(fs[i].conn)
		}
	}
}

// queryDesc returns the description of a Query field that is the
// connection to o's nodes: what PaginateQuery's Go doc says of it.
func queryDesc(o *object) string {
	how := ""
	if o.hasWhere {
		how += ", filtered by where"
	}
	if orderArg(o) != "" {
		how += ", ordered by orderBy and then the key"
	} else {
		how += ", ordered by the key"
	}
	return "The connection to the " + o.name + " nodes" + how + ", and paged by the other arguments."
}

// decode is the graphql annotation among an element's annotations.
func decode(anns map[string]json.RawMessage, where string) (graphql.Annotation, error) {
	var a graphql.Annotation
	raw, ok := anns[graphql.Annotation{}.Name()]
	if !ok {
		return a, nil
	}
	// UseNumber keeps a directive argument's number as written: an integer
	// above 2^53 has no float64.
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&a); err != nil {
		return a, fmt.Errorf("%s: the graphql annotation: %w", where, err)
	}
	return a, nil
}

// veloxEnum reports whether the enum field f is of a Go enum type velox-gen
// declares. An application's type names its package, and may share its
// name with one of velox-gen's.
func (g *graph) veloxEnum(f *plugin.Field) bool { return g.goEnums[f.Enum] && f.PkgPath == "" }
