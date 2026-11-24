package sdl

import (
	"fmt"
	"strings"
	"unicode"
)

// Config holds velox-graphql's options for one model. Each string option is
// "" for its default; ADR 0155 records why each default is what it is.
type Config struct {
	// Names is how fields and edges are named (velox-graphql's option
	// names=; ADR 0150): "go" by their Go names, so customer_id is
	// customerID and image_url is imageURL, as entgql does; "schema" by
	// schemaCamel of their schema names, customerId and imageUrl. A
	// predicate's name starts with its field's, imageUrlNEQ. Order values
	// are graphql.OrderField's either way. "" is "go" (SchemaNames).
	Names string
	// Filters is the semantics of the where inputs (velox-graphql's option
	// filters=; gqlgraph.Type's LenientFilters): "strict" is SQL's, so an
	// empty In matches no node and a false IsNil is IS NOT NULL; "lenient"
	// is entgql's, where both add nothing, an empty or adds nothing, a
	// nested empty input is an error and no width cap applies. "" is
	// "strict" (LenientFilters).
	Filters string
	// Node declares Relay's interface Node, which every type with an ID
	// implements, and node(id:) and nodes(ids:) first in the root query,
	// which is then written even with no QueryField; gogen then writes
	// noder_gql.go (velox-graphql's option node=; ADR 0152): "on" or
	// "off". An ID stays the entity's key (ADR 0061), so node(id:) needs
	// IDs unique across types, as snowflakes and UUIDs are. "" is "off"
	// (NodeOn).
	Node string
	// IDOps is the default predicates of a field named like a key (id,
	// *_id, *ID) of a string or integer type, of the key, and of an Other
	// field (velox-graphql's option idops=): "kind" gives each its kind's,
	// as any field; "equality" gives each EQ, NEQ, In and NotIn, and IsNil
	// and NotNil when it is nullable. A foreign key an edge stands for has
	// equality alone either way. "" is "kind".
	IDOps string
	// OrderFields is which fields the order enum lists (velox-graphql's
	// option orderfields=): "annotated" those graphql.OrderField names, as
	// entgql does; "all" every field of an orderable type, a string that is
	// not field.Text, a time, an int or an int64, a bool, an enum or a
	// float, named by graphql.OrderField or in upper snake case, and the
	// key only with graphql.OrderField. Under "all" an OrderField on a field
	// of another type is dropped with a warning, and no directive guards
	// the order: a field with an application directive is listed too. ""
	// is "annotated".
	OrderFields string
	// QueryFields is which types have a root Query field (velox-graphql's
	// option queryfields=): "annotated" those with graphql.QueryField, as
	// entgql does; "all" every type, named by QueryField or by default.
	// "" is "annotated".
	QueryFields string
	// Optional is the output type of an Optional field (velox-graphql's
	// option optional=): "nonnull", since its column is NOT NULL and always
	// holds a value (ADR 0003); "nullable" leaves it nullable. Either way
	// a create or update input leaves it optional. "" is "nonnull".
	Optional string
	// RequiredEdges is the output type of a Required unique edge
	// (velox-graphql's option requirededges=): "nullable", since a policy
	// may hide its target and a null in a non-null field nulls its parent;
	// "nonnull" makes it non-null. "" is "nullable".
	RequiredEdges string
	// InputKeys is the name of a create or update input's field for an
	// edge's implicit foreign key, which the type does not show
	// (velox-graphql's option inputkeys=): "edge" the edge's name and ID,
	// bestFriendID; "column" the key column's name, userBestFriend. A
	// declared key is named as the type shows it either way. "" is "edge".
	InputKeys string
	// JSONTypes is the type of a json field of a named Go type
	// (velox-graphql's option jsontypes=): "json" the JSON scalar; "named"
	// the Go type's name, CRMSettings, or a list of it for a slice,
	// [TaskAssignee!], which the application declares. graphql.Type wins
	// either way. "" is "json".
	JSONTypes string
	// WhereWhitelist leaves out of the where inputs the predicates of every
	// field but the key that lacks graphql.WhereInput, whatever its WhereOps
	// (velox-graphql's option where=whitelist): filters are opt-in field by
	// field.
	WhereWhitelist bool
	// GraphQLGo writes the SDL for github.com/syssam/graphql-go, whose
	// generator gqlc binds it to the generated Go (velox-graphql's option
	// engine=graphql-go). Every type the SDL declares but Query carries
	// @goModel naming its Go type in Package, or in veloxgql for PageInfo
	// and OrderDirection; each scalar names the Go type velox entities hold;
	// there are no introspection binds, which only gqlgen needs. gqlc does
	// not read @goField(name:) yet: a field gqlgen reads through a getter is
	// forced to a resolver, a non-null bytes field, a key not named ID and a
	// field or edge graphql.FieldName renames are errors, joined.
	GraphQLGo bool

	// Package is the generated package's import path, which binds Node to
	// its Noder with @goModel under NodeOn, and every type under
	// GraphQLGo; velox-graphql gives the request's.
	Package string

	// PerEntity writes the SDL as one file per entity and a shared file,
	// velox.graphql, in place of velox.graphql alone (velox-graphql's
	// option sdlfiles=entity; ADR 0125). An entity's file is
	// velox_<schema name in lower case>.graphql and holds its type,
	// connection, order, where and mutation inputs, and each enum only it
	// uses; the shared file holds the vocabulary, Node, the root Query and
	// each enum two entities use. gqlc makes a Go package of each file, so
	// Query's fields and the entities' fields name their argument structs
	// and resolver methods in packages of their own.
	PerEntity bool
	// Entities, when not empty, writes the SDL of these entities alone,
	// by schema name (velox-graphql's option entities=; ADR 0125): their
	// types and inputs and their Query fields. A field of theirs whose type
	// is an entity left out is left out too. A where input keeps every
	// predicate its Go struct has, so the where input of an entity left out
	// that one of theirs nests is written, with its enums, however far it
	// nests. Under PerEntity such an input goes in the shared file (ADR
	// 0130). The Go velox-graphql writes is the whole schema's.
	Entities []string
	// AppTypes are the types the application declares (velox-graphql's
	// option apptypes=; ADR 0130). A scalar of velox's vocabulary it lists,
	// such as JSON or UUID, is not declared by velox.graphql. Under
	// Entities, a field of a written type that names a type neither the
	// SDL declares nor AppTypes lists is left out, and a written input that
	// names one is refused, since graphql-go binds an input's every field;
	// when empty, every type graphql.Type and graphql.Map name stays.
	AppTypes []string
}

// SchemaNames reports whether c names fields by their schema names: Names
// is "schema".
func (c Config) SchemaNames() bool { return c.Names == "schema" }

// LenientFilters reports whether c gives the where inputs entgql's lenient
// semantics: Filters is "lenient".
func (c Config) LenientFilters() bool { return c.Filters == "lenient" }

// NodeOn reports whether c declares Node: Node is "on".
func (c Config) NodeOn() bool { return c.Node == "on" }

// idEquality reports whether a field named like a key defaults to equality
// alone: IDOps is "equality".
func (c Config) idEquality() bool { return c.IDOps == "equality" }

// orderAll reports whether the order enum lists every orderable field:
// OrderFields is "all".
func (c Config) orderAll() bool { return c.OrderFields == "all" }

// queryAll reports whether every type has a root Query field: QueryFields
// is "all".
func (c Config) queryAll() bool { return c.QueryFields == "all" }

// optionalNullable reports whether an Optional field is nullable in its
// type: Optional is "nullable".
func (c Config) optionalNullable() bool { return c.Optional == "nullable" }

// requiredNonNull reports whether a Required unique edge is non-null:
// RequiredEdges is "nonnull".
func (c Config) requiredNonNull() bool { return c.RequiredEdges == "nonnull" }

// columnKeys reports whether an implicit foreign key's input is named by
// its column: InputKeys is "column".
func (c Config) columnKeys() bool { return c.InputKeys == "column" }

// namedJSON reports whether a json field of a named Go type takes its name:
// JSONTypes is "named".
func (c Config) namedJSON() bool { return c.JSONTypes == "named" }

// options are the string options of Config: the plugin's key, the field,
// and its values, the default first, each with what it does.
func (c Config) options() []option {
	return []option{
		{"names", c.Names, [][2]string{{"go", "a field's Go name (imageURL)"}, {"schema", "its schema name (imageUrl)"}}},
		{"filters", c.Filters, [][2]string{{"strict", "an empty In matches no node"}, {"lenient", "it adds nothing"}}},
		{"node", c.Node, [][2]string{{"off", "no Node"}, {"on", "Relay's Node and node(id:)"}}},
		{"idops", c.IDOps, [][2]string{{"kind", "a field named like a key gets its kind's predicates"}, {"equality", "equality alone"}}},
		{"orderfields", c.OrderFields, [][2]string{{"annotated", "the fields graphql.OrderField names"}, {"all", "every orderable field"}}},
		{"queryfields", c.QueryFields, [][2]string{{"annotated", "the types with graphql.QueryField"}, {"all", "every type"}}},
		{"optional", c.Optional, [][2]string{{"nonnull", "an Optional field is non-null"}, {"nullable", "nullable"}}},
		{"requirededges", c.RequiredEdges, [][2]string{{"nullable", "a Required unique edge is nullable"}, {"nonnull", "non-null"}}},
		{"inputkeys", c.InputKeys, [][2]string{{"edge", "an implicit foreign key's input is named by its edge"}, {"column", "by its column"}}},
		{"jsontypes", c.JSONTypes, [][2]string{{"json", "a json field is JSON"}, {"named", "its Go type's name"}}},
	}
}

// option is one of Config's string options.
type option struct {
	key, value string
	values     [][2]string // each value and what it does, the default first
}

// checkOptions refuses a value of a string option c does not know.
func (c Config) checkOptions() error {
	for _, o := range c.options() {
		if o.value == "" {
			continue
		}
		var known []string
		ok := false
		for _, v := range o.values {
			ok = ok || o.value == v[0]
			known = append(known, v[0]+", "+v[1])
		}
		if !ok {
			return fmt.Errorf("velox-graphql: %s=%s: %s is %s", o.key, o.value, o.key, strings.Join(known, "; or "))
		}
	}
	return nil
}

// OptionHelp names each of Config's string options with its values, the
// default first: "names=go or names=schema".
func OptionHelp() []string {
	var out []string
	for _, o := range (Config{}).options() {
		var vs []string
		for _, v := range o.values {
			vs = append(vs, o.key+"="+v[0])
		}
		out = append(out, strings.Join(vs, " or "))
	}
	return out
}

// SetOption sets the string option key to v, and reports whether key is
// one of Config's string options.
func (c *Config) SetOption(key, v string) bool {
	p := map[string]*string{
		"names": &c.Names, "filters": &c.Filters, "node": &c.Node, "idops": &c.IDOps,
		"orderfields": &c.OrderFields, "queryfields": &c.QueryFields, "optional": &c.Optional,
		"requirededges": &c.RequiredEdges, "inputkeys": &c.InputKeys, "jsontypes": &c.JSONTypes,
	}[key]
	if p == nil {
		return false
	}
	*p = v
	return true
}

// schemaCamel is the GraphQL name of a schema name under names=schema: the
// words of each underscore-separated part, split again at a capital that
// starts a word, the first in lower case and each other with only its first
// letter in upper case. "customer_id" is "customerId",
// "api_URL" is "apiUrl" and "HTTPServer" is "httpServer".
func schemaCamel(s string) string {
	var words []string
	for part := range strings.SplitSeq(s, "_") {
		words = append(words, schemaWords(part)...)
	}
	var b strings.Builder
	first := true
	for _, w := range words {
		if w == "" {
			continue
		}
		lower := strings.ToLower(w)
		if first {
			b.WriteString(lower)
			first = false
			continue
		}
		r := []rune(lower)
		b.WriteString(string(unicode.ToUpper(r[0])) + string(r[1:]))
	}
	return b.String()
}

// schemaWords splits a PascalCase word into words: "UserName" is User and
// Name, "HTTPServer" is HTTP and Server.
func schemaWords(s string) []string {
	runes := []rune(s)
	var words []string
	var w strings.Builder
	upper := func(r rune) bool { return r >= 'A' && r <= 'Z' }
	lower := func(r rune) bool { return r >= 'a' && r <= 'z' }
	for i, r := range runes {
		if i > 0 && upper(r) && w.Len() > 0 {
			switch prev := runes[i-1]; {
			case !upper(prev):
				words = append(words, w.String())
				w.Reset()
			case i+1 < len(runes) && lower(runes[i+1]):
				words = append(words, w.String()) // the end of an acronym
				w.Reset()
			}
		}
		w.WriteRune(r)
	}
	if w.Len() > 0 {
		words = append(words, w.String())
	}
	return words
}

// validTypeRef reports whether s is a type graphql.Type gives a field: a
// name, or a list of one, whose elements may be non-null. The field's own
// nullability adds the outer non-null mark.
func validTypeRef(s string) bool {
	if inner, ok := listOf(s); ok {
		return validName(inner)
	}
	return validName(s)
}

// listOf returns the element type of the list type s, [T] or [T!], and
// whether s is one.
func listOf(s string) (string, bool) {
	if !strings.HasPrefix(s, "[") || !strings.HasSuffix(s, "]") {
		return "", false
	}
	return strings.TrimSuffix(s[1:len(s)-1], "!"), true
}

// inputScalars are the scalars an input may take: velox's own and those
// applications commonly declare.
var inputScalars = map[string]bool{
	"ID": true, "String": true, "Int": true, "Float": true, "Boolean": true,
	"Time": true, "UUID": true, "JSON": true, "Bytes": true, "Uint64": true,
	"Decimal": true, "Map": true, "Any": true,
}

// inputable reports whether an input may take the type t that graphql.Type
// gives a field: a scalar inputScalars lists, an input (a name ending in
// Input), an enum, or a list of one. Any other name is taken to be an object
// type.
func (g *graph) inputable(t string) bool {
	if inner, ok := listOf(t); ok {
		t = inner
	}
	return inputScalars[t] || strings.HasSuffix(t, "Input") || g.enumSet[t] || t == strings.ToUpper(t)
}

// keyLike reports whether the field name is named like a key's: id, a snake
// *_id, or a camel *ID. Under idops=equality its default predicates are
// equality alone.
func keyLike(name string) bool {
	return strings.EqualFold(name, "id") || strings.HasSuffix(strings.ToLower(name), "_id") || len(name) > 2 && strings.HasSuffix(name, "ID")
}

// keyKinds are the field kinds whose equality is EQ, NEQ, In and NotIn.
var keyKinds = map[string]bool{
	"string": true, "int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
}
