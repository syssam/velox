package sdl

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"

	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/plugin"
)

// fill computes o's fields, edges and enums.
func (g *graph) fill(o *object) error {
	e := o.ent
	seen := map[string]string{} // GraphQL field name → the element
	add := func(f gqlField, where string) error {
		if prev, ok := seen[f.name]; ok {
			return fmt.Errorf("%s and %s are both the GraphQL field %s.%s", prev, where, o.name, f.name)
		}
		seen[f.name] = where
		o.fields = append(o.fields, f)
		return nil
	}
	if o.hasID {
		pk := e.Fields[e.PrimaryKey[0]]
		// The ID is the key, in its own Go type: gqlgen binds id to the
		// struct's key field, by its name when it is not ID.
		where := "schema " + e.Name + ", field " + pk.Name
		id := gqlField{name: "id", typ: "ID!", desc: pk.Comment, where: where}
		if !strings.EqualFold(pk.GoName, "id") {
			if err := g.nameBound(where, "the field id binds to the key's Go field "+pk.GoName, "name the key field id"); err != nil {
				return err
			}
			id.dirs = []directive{bindTo(pk.GoName)}
		}
		if err := add(id, where); err != nil {
			return err
		}
		o.plain = append(o.plain, Field{Name: "id", Field: e.PrimaryKey[0]})
	}
	for i := range e.Fields {
		f, ok, err := g.plainField(o, i)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if err := add(f, f.where); err != nil {
			return err
		}
		o.plain = append(o.plain, Field{Name: f.name, Field: i})
	}
	for i := range e.Edges {
		f, ef, ok, err := g.edgeFieldOf(o, i)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if err := add(f, f.where); err != nil {
			return err
		}
		o.edges = append(o.edges, ef)
	}
	for _, m := range o.ann.Resolvers {
		f, err := resolverField(o, m)
		if err != nil {
			return err
		}
		if err := add(f, f.where); err != nil {
			return err
		}
	}
	if !slices.ContainsFunc(o.fields, func(f gqlField) bool { return !f.resolver }) {
		return fmt.Errorf("schema %s: its type would have no fields: each field is skipped, sensitive or a foreign key, and each edge is skipped or leads to a skipped schema; skip the schema with graphql.Skip()", e.Name)
	}
	return nil
}

// resolverField is the GraphQL field of o's graphql.Map m: its name, arguments
// and type as written, bound to a resolver method. The application's own
// types are not checked here: its SDL declares them. What is checked is that
// m is one field of one type and nothing more, since the plugin writes it as
// text.
func resolverField(o *object, m graphql.ResolverMapping) (gqlField, error) {
	where := fmt.Sprintf("schema %s, graphql.Map(%q, %q)", o.ent.Name, m.Field, m.Type)
	name := m.Name()
	if !validName(name) {
		return gqlField{}, fmt.Errorf("%s: %q is not a GraphQL field name", where, name)
	}
	var args string
	if _, rest, ok := strings.Cut(m.Field, "("); ok {
		args = "(" + rest
	}
	if strings.TrimSpace(m.Type) == "" {
		return gqlField{}, fmt.Errorf("%s: no type; give one as the SDL writes it, such as \"PublicUser\" or \"[Post!]!\"", where)
	}
	src := "type T { " + name + args + ": " + m.Type + " }"
	doc, err := parser.ParseSchema(&ast.Source{Name: "graphql.Map", Input: src})
	if err != nil {
		return gqlField{}, fmt.Errorf("%s: not a field of an SDL type: %v", where, err)
	}
	if len(doc.Definitions) != 1 || len(doc.Definitions[0].Fields) != 1 || doc.Definitions[0].Fields[0].Name != name || len(doc.Definitions[0].Fields[0].Directives) > 0 {
		return gqlField{}, fmt.Errorf("%s: it must be one field and its type, without directives", where)
	}
	return gqlField{
		name: name, args: args, typ: m.Type, desc: m.Comment, where: where, resolver: true,
		dirs: []directive{{name: "goField", text: "@goField(forceResolver: true)"}},
	}, nil
}

// plainField returns the GraphQL field of o's field i, and false when the
// type does not show it. It adds the getter the field binds to, if any, to
// o.
func (g *graph) plainField(o *object, i int) (gqlField, bool, error) {
	e, f, a := o.ent, o.ent.Fields[i], o.fanns[i]
	switch {
	case o.hasID && i == e.PrimaryKey[0], f.Sensitive, f.Implicit != "", o.fks[f.Name] && !o.veloxe, a.Skip&graphql.SkipType != 0:
		return gqlField{}, false, nil
	}
	where := "schema " + e.Name + ", field " + f.Name
	name, err := o.fieldName(i)
	if err != nil {
		return gqlField{}, false, err
	}
	typ, err := g.fieldType(o, f, a, where)
	if err != nil {
		return gqlField{}, false, err
	}
	if o.fks[f.Name] {
		// veloxe's: a foreign key is its target's ID, beside its edge.
		typ = "ID"
	}
	// veloxe's type left an Optional field nullable too, though its column is
	// NOT NULL and its Go value never nil: gqlgen writes a value as non-null.
	if !f.Nullable && (!o.veloxe || !f.Optional) {
		typ += "!"
	}
	var dirs []directive
	switch v := g.goValue(f, a); {
	case !v.own || f.Type == "bytes" && !f.Nullable:
		// gqlgen binds a field only to the Go type of its scalar: a getter
		// converts the field's own. An empty blob of a Bytes! needs one too:
		// modernc.org/sqlite reads it as nil, which gqlgen refuses in a T!.
		gt := Getter{Field: i, Method: f.GoName + "GQL", Returns: v.getter, Pkg: v.getterPkg}
		if err := g.nameBound(where, "the field is read through the method "+gt.Method+", which converts its Go type to its scalar's",
			"give it graphql.Type naming a type the application binds to its Go type, or leave it out with graphql.Skip()"); err != nil {
			return gqlField{}, false, err
		}
		o.getters = append(o.getters, gt)
		dirs = append(dirs, bindTo(gt.Method))
	case !strings.EqualFold(name, f.GoName):
		// gqlgen binds a field to the Go field of its name, ignoring case.
		if err := g.nameBound(where, renamed(name, a.FieldName, "Go field "+f.GoName), "drop the annotation"); err != nil {
			return gqlField{}, false, err
		}
		dirs = append(dirs, bindTo(f.GoName))
	}
	ds, err := directives(a.Directives, where)
	if err != nil {
		return gqlField{}, false, err
	}
	return gqlField{name: name, typ: typ, desc: f.Comment, dirs: append(dirs, ds...), where: where}, true, nil
}

// edgeFieldOf returns the GraphQL field of o's edge i and its edgeField,
// and false when the type does not show it: the edge or its target is
// skipped.
func (g *graph) edgeFieldOf(o *object, i int) (gqlField, edgeField, bool, error) {
	e, ed, a := o.ent, o.ent.Edges[i], o.eanns[i]
	t := g.byName[ed.Target]
	if a.Skip&graphql.SkipType != 0 || t == nil { // skipped, or its target is
		return gqlField{}, edgeField{}, false, nil
	}
	where := "schema " + e.Name + ", edge " + ed.Name
	name, err := o.edgeName(i)
	if err != nil {
		return gqlField{}, edgeField{}, false, err
	}
	f := gqlField{name: name, desc: ed.Comment, where: where}
	ef := edgeField{edge: i, name: name, target: t}
	switch {
	case a.MaxList < 0:
		return gqlField{}, edgeField{}, false, fmt.Errorf("%s: graphql.MaxList(%d): a list reads at least one row", where, a.MaxList)
	case a.MaxList != 0 && (ed.Unique || a.Connection == nil || *a.Connection):
		return gqlField{}, edgeField{}, false, fmt.Errorf("%s: graphql.MaxList on an edge that is no list, which first and last bound; add graphql.Connection(false) to a to-many edge", where)
	}
	switch {
	case ed.Unique && o.veloxe && ed.Required:
		// veloxe's: a Required unique edge is non-null, whatever may leave
		// the target unread (see below).
		f.typ, ef.kind = t.name+"!", EdgeUnique
	case ed.Unique:
		// Nullable even when Required: a row reads the edge's target in a
		// statement of its own, which a policy, a hook, a replica's
		// lag or a delete may leave empty, and a non-null field that read
		// null would null the page around it.
		f.typ, ef.kind = t.name, EdgeUnique
	case a.Connection != nil && !*a.Connection:
		f.typ, ef.kind = "["+t.name+"!]!", EdgeList
	case !t.conn && a.Connection != nil:
		return gqlField{}, edgeField{}, false, fmt.Errorf("%s: graphql.Connection(true) on an edge to %s, which has no connection: under compat=veloxe a schema with graphql.QueryField and without graphql.RelayConnection has none", where, ed.Target)
	case !t.conn:
		// veloxe's: a to-many edge to a type without a connection is a list.
		f.typ, ef.kind = "["+t.name+"!]!", EdgeList
	default:
		f.typ, f.conn, ef.kind = t.name+"Connection!", t, EdgeConnection
	}
	// The edge's method is its Go name, as entgql's is: the entity's
	// relations are in its Edges field, so the name is free. gqlgen binds
	// a field to the method its name matches, so @goField names the
	// method only when the field's name does not.
	ds, err := directives(a.Directives, where)
	if err != nil {
		return gqlField{}, edgeField{}, false, err
	}
	if !gqlgenMatches(f.name, ed.GoName) {
		if err := g.nameBound(where, renamed(f.name, a.FieldName, "method "+ed.GoName), "drop the annotation"); err != nil {
			return gqlField{}, edgeField{}, false, err
		}
	}
	f.dirs = append(edgeBind(f.name, ed.GoName, a.MaxList), ds...)
	return f, ef, true, nil
}

// nameBound is, under Config.GraphQLGo, the error for the element where,
// which gqlgen binds to Go through @goField(name:) for the reason why, with
// fix, what the schema can do instead: gqlc does not read the argument
// yet, and would bind the field to a Resolver stub. It is nil otherwise.
func (g *graph) nameBound(where, why, fix string) error {
	if !g.cfg.GraphQLGo {
		return nil
	}
	return fmt.Errorf("%s: engine=graphql-go: %s through @goField(name:), and gqlc does not yet honour @goField(name:); %s", where, why, fix)
}

// renamed says why the GraphQL field name binds to the Go name what
// ("Go field Name", "method Author"): the graphql.FieldName annotated, or
// else the name itself.
func renamed(name, annotated, what string) string {
	if annotated != "" {
		return fmt.Sprintf("graphql.FieldName(%q) binds it to its %s", annotated, what)
	}
	return fmt.Sprintf("its name %s binds it to its %s", name, what)
}

// edgeBind returns the @goField directive that binds the edge field name to
// its method goName, with maxList when it is not 0. It returns nil when
// gqlgen binds the field by its name alone.
func edgeBind(name, goName string, maxList int) []directive {
	if gqlgenMatches(name, goName) && maxList == 0 {
		return nil
	}
	b := bindTo(goName)
	if maxList != 0 {
		// In @goField, which gqlgen needs no configuration for and reads no further:
		// a directive of its own would have to be skip_runtime in gqlgen.yml.
		b.text = strings.TrimSuffix(b.text, ")") + ", maxList: " + strconv.Itoa(maxList) + ")"
	}
	return []directive{b}
}

// fieldName is the GraphQL name of o's field i.
func (o *object) fieldName(i int) (string, error) {
	f := o.ent.Fields[i]
	return fieldName(o.camel(f.GoName, f.Name), o.fanns[i].FieldName, "schema "+o.ent.Name+", field "+f.Name)
}

// camel is the GraphQL name of a field or edge of o by its Go name and its
// schema name: velox's, or veloxe's under Config.Veloxe.
func (o *object) camel(goName, name string) string {
	if o.veloxe {
		return veloxeCamel(name)
	}
	return camel(goName)
}

// edgeName is the GraphQL name of o's edge i.
func (o *object) edgeName(i int) (string, error) {
	ed := o.ent.Edges[i]
	return fieldName(o.camel(ed.GoName, ed.Name), o.eanns[i].FieldName, "schema "+o.ent.Name+", edge "+ed.Name)
}

// fieldName is the GraphQL name of a field or edge: the FieldName
// annotation's, or else camel, the name of its Go name.
func fieldName(camel, annotated, where string) (string, error) {
	switch {
	case annotated != "" && !validName(annotated):
		return "", fmt.Errorf("%s: graphql.FieldName(%q) is not a GraphQL name", where, annotated)
	case annotated != "" && annotated[0] == '_':
		// Its Go field in the inputs is the name with its first letter in
		// upper case: one starting with _ is unexported, which gqlgen cannot set.
		return "", fmt.Errorf("%s: graphql.FieldName(%q) names the inputs' Go fields too, so it starts with a letter", where, annotated)
	case annotated != "":
		return annotated, nil
	case !validName(camel):
		return "", fmt.Errorf("%s: %q is not a GraphQL name; name it with graphql.FieldName", where, camel)
	}
	return camel, nil
}

// fieldType is the GraphQL type of f, without its non-null mark. An enum
// field adds its enum to o, once: the type, a where input and the mutation
// inputs may each ask.
func (g *graph) fieldType(o *object, f *plugin.Field, a graphql.Annotation, where string) (string, error) {
	if a.Type != "" && (f.Type != "enum" || a.Skip&graphql.SkipEnumField != 0) {
		// The application's type, as veloxe took it: the application
		// declares it, and binds it to the field's Go type.
		name := a.Type
		if inner, ok := listOf(name); ok {
			name = inner
		}
		g.appSet[name] = true
		return a.Type, nil
	}
	switch f.Type {
	case "string":
		return "String", nil
	case "int", "int8", "int16", "int32", "int64", "uint8", "uint16", "uint32":
		return "Int", nil
	case "uint", "uint64": // a uint is 64 bits wide where velox runs
		return "Uint64", nil
	case "float32", "float64":
		return "Float", nil
	case "bool":
		return "Boolean", nil
	case "time":
		return "Time", nil
	case "uuid":
		return "UUID", nil
	case "bytes":
		g.bytes = true
		return "Bytes", nil
	case "json":
		if s := jsonList[f.ElemType]; s != "" {
			return "[" + s + "!]", nil
		}
		return "JSON", nil
	case "enum":
		if a.Skip&graphql.SkipEnumField != 0 {
			// No enum: the stored values, as they are. The Go code needs to
			// know, as the Go enum type binds to the String.
			if !slices.ContainsFunc(o.enums, func(en gqlEnum) bool { return en.field == f.Name }) {
				o.enums = append(o.enums, gqlEnum{field: f.Name})
			}
			return "String", nil
		}
		return g.enumType(o, f, a, where)
	default:
		return "", fmt.Errorf("%s: a field of type %s has no GraphQL type; leave it out with graphql.Skip()", where, f.Type)
	}
}

// enumType is the GraphQL enum of the enum field f, which it declares once.
func (g *graph) enumType(o *object, f *plugin.Field, a graphql.Annotation, where string) (string, error) {
	values, err := enumValues(f, a, where)
	if err != nil {
		return "", err
	}
	// The enum is named after velox-gen's Go enum type, <Schema><Field>,
	// so that gqlgen binds it whatever graphql.Type names the type. An enum
	// of the application's own Go type gets the same name; @goModel binds it.
	name := o.ent.Name + f.GoName
	if g.veloxEnum(f) {
		name = f.Enum
	}
	if a.Type != "" {
		// veloxe's: the enum's name, shared by the fields of one Go type
		// that give it. The Go code aliases velox-gen's own type to it.
		name = a.Type
	}
	if slices.ContainsFunc(o.enums, func(en gqlEnum) bool { return en.name == name && en.field == f.Name }) {
		return name, nil
	}
	if !validName(name) {
		return "", fmt.Errorf("%s: the enum name %q is not a GraphQL name", where, name)
	}
	add := g.types.add
	if g.veloxEnum(f) && name == f.Enum || f.PkgPath != "" {
		// velox-gen's own Go enum type, or the application's, which @goModel
		// binds whatever the generated package declares.
		add = g.types.addGo
	}
	if g.veloxEnum(f) || a.Type != "" {
		goType := f.Enum
		if !g.veloxEnum(f) {
			goType = f.PkgPath + "." + f.Enum
		}
		ordered, shared, err := g.shares.claimAs(name+" "+goType, f.Enum, where, f.EnumValues, values)
		if err != nil {
			return "", err
		}
		values = ordered
		if shared {
			o.enums = append(o.enums, gqlEnum{name: name, values: values, field: f.Name, shared: true})
			return name, nil
		}
	}
	g.enumSet[name] = true
	if err := add(name, where); err != nil {
		return "", err
	}
	if g.veloxEnum(f) && name != f.Enum {
		// The Go code declares the name an alias of velox-gen's type, which
		// gqlgen binds by name.
		g.types.reserve(name, "the alias of the enum type "+f.Enum)
	}
	en := gqlEnum{name: name, values: values, field: f.Name}
	if g.veloxEnum(f) {
		en.goType = f.Enum
	}
	if !g.veloxEnum(f) && f.PkgPath != "" {
		en.model = f.PkgPath + "." + f.Enum
	}
	o.enums = append(o.enums, en)
	return name, nil
}

// enumValues are the GraphQL values of an enum field: its stored values, upper
// cased, or as graphql.EnumValues names them.
func enumValues(f *plugin.Field, a graphql.Annotation, where string) ([]string, error) {
	for _, k := range slices.Sorted(maps.Keys(a.EnumValues)) {
		if !slices.Contains(f.EnumValues, k) {
			return nil, fmt.Errorf("%s: graphql.EnumValues names %q, which is not a value of the field", where, k)
		}
	}
	if len(f.EnumValues) == 0 {
		return nil, fmt.Errorf("%s: an enum field with no values", where)
	}
	values := make([]string, 0, len(f.EnumValues))
	by := map[string]string{}
	for _, v := range f.EnumValues {
		gv, renamed := a.EnumValues[v]
		if !renamed {
			gv = strings.ToUpper(v)
		}
		switch {
		case !validEnumValue(gv) && renamed:
			return nil, fmt.Errorf("%s: graphql.EnumValues names the value %q %q, which is not a GraphQL enum value", where, v, gv)
		case !validEnumValue(gv):
			return nil, fmt.Errorf("%s: the value %q makes %q, which is not a GraphQL enum value; name it with graphql.EnumValues", where, v, gv)
		case by[gv] != "":
			return nil, fmt.Errorf("%s: the values %q and %q are both the GraphQL enum value %s; name one with graphql.EnumValues", where, by[gv], v, gv)
		}
		by[gv] = v
		values = append(values, gv)
	}
	return values, nil
}

// bindTo is the directive that binds a field to the Go field or method name.
// gqlgenMatches reports whether gqlgen binds the GraphQL field name to the
// Go method goName by itself: it compares a method's name ignoring case.
func gqlgenMatches(name, goName string) bool { return strings.EqualFold(name, goName) }

func bindTo(name string) directive {
	return directive{name: "goField", text: "@goField(name: " + quote(name) + ")"}
}
