package sdl

import (
	"fmt"
	"strings"
	"unicode"
)

// Config holds velox-graphql's options for one model.
type Config struct {
	// Veloxe makes the API the one veloxe's generator made, for an
	// application whose clients are built against it (velox-graphql's
	// option compat=veloxe):
	//   - a foreign-key field an edge is bound to is in the type, as an ID,
	//     beside its edge;
	//   - its where predicates are named from the field, customerId,
	//     customerIdNEQ, customerIdIn..., where velox names them from the
	//     edge (customerID, customerIDIn);
	//   - the rest of veloxe's surface, which docs/decisions/0087 lists:
	//     every orderable field in the order enum, Optional fields nullable
	//     in the type, Required unique edges non-null, a root Query field for
	//     every type, veloxe's connection nullability and scalars, equality
	//     alone by default on keys and Other fields, explicit WhereOps over
	//     WhereInput's defaults, no directives on inputs, a named JSON type
	//     by its Go name, and silence where veloxe ignored an annotation.
	//   - a connection and a foreign key's predicates have veloxe's
	//     semantics (gqlgraph.Type's Veloxe).
	// Names, Filters and Node default by it (ADRs 0150 and 0152).
	Veloxe bool
	// Names is how fields and edges are named (velox-graphql's option
	// names=; ADR 0150): "go" by their Go names, so customer_id is
	// customerID and image_url is imageURL, as entgql does; "schema" by
	// veloxeCamel of their schema names, customerId and imageUrl. A
	// predicate's name starts with its field's, imageUrlNEQ, and an edge's
	// foreign-key predicates and input are named by the edge, customerId
	// and customerIdIn. Order values are graphql.OrderField's either way.
	// "" is "schema" under Veloxe and "go" otherwise (SchemaNames).
	Names string
	// Filters is the semantics of the where inputs (velox-graphql's option
	// filters=; gqlgraph.Type's VeloxeFilters; ADR 0150): "strict" fails
	// closed, so an empty In matches no node and a false IsNil is IS NOT
	// NULL; "veloxe" is veloxe's, where both add nothing, an empty or adds
	// nothing, a nested empty input is an error and no width cap applies.
	// "" is "veloxe" under Veloxe and "strict" otherwise (VeloxeFilters).
	Filters string
	// Node declares Relay's interface Node, which every type with an ID
	// implements, and node(id:) and nodes(ids:) first in the root query,
	// which is then written even with no QueryField; gogen then writes
	// noder_gql.go (velox-graphql's option node=; ADRs 0088 and 0152): "on"
	// or "off". An ID stays the entity's key (ADR 0061), so node(id:) needs
	// IDs unique across types, as snowflakes and UUIDs are. "" is "on"
	// under Veloxe and "off" otherwise (NodeOn).
	Node string
	// WhereWhitelist leaves out of the where inputs the predicates of every
	// field but the key that lacks graphql.WhereInput (the old velox's
	// EnableWhereInputs(true)), whatever its WhereOps (velox-graphql's option
	// where=whitelist). It is what an application's codegen hook did under
	// veloxe, so it is an option of its own, apart from Veloxe.
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
	// option sdlfiles=entity; ADR 0125). An entity's file holds its type,
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
	// AppTypes, when not empty, are the types the application declares that
	// the SDL of Entities may name (velox-graphql's option apptypes=; ADR
	// 0130). A field of a written type that names a type neither the SDL
	// declares nor AppTypes lists is left out. A written input that names
	// one is refused, since graphql-go binds an input's every field. When
	// empty, every type graphql.Type and graphql.Map name stays.
	AppTypes []string
}

// SchemaNames reports whether c names fields by their schema names: Names
// is "schema", or "" under Veloxe.
func (c Config) SchemaNames() bool {
	return c.Names == "schema" || c.Names == "" && c.Veloxe
}

// VeloxeFilters reports whether c gives the where inputs veloxe's
// semantics: Filters is "veloxe", or "" under Veloxe.
func (c Config) VeloxeFilters() bool {
	return c.Filters == "veloxe" || c.Filters == "" && c.Veloxe
}

// NodeOn reports whether c declares Node: Node is "on", or "" under Veloxe.
func (c Config) NodeOn() bool {
	return c.Node == "on" || c.Node == "" && c.Veloxe
}

// checkOptions refuses a Names, a Filters or a Node c does not know.
func (c Config) checkOptions() error {
	switch {
	case c.Names != "" && c.Names != "go" && c.Names != "schema":
		return fmt.Errorf("velox-graphql: names=%s: names is go, a field's Go name (imageURL), or schema, its schema name (imageUrl)", c.Names)
	case c.Filters != "" && c.Filters != "strict" && c.Filters != "veloxe":
		return fmt.Errorf("velox-graphql: filters=%s: filters is strict, where an empty In matches no node, or veloxe, where it adds nothing", c.Filters)
	case c.Node != "" && c.Node != "on" && c.Node != "off":
		return fmt.Errorf("velox-graphql: node=%s: node is on, which declares Relay's Node and node(id:), or off", c.Node)
	}
	return nil
}

// veloxeCamel is veloxe's GraphQL name of a schema name, as its generator's
// camel made it: the words of each underscore-separated part, split again at
// a capital that starts a word, the first in lower case and each other with
// only its first letter in upper case. "customer_id" is "customerId",
// "api_URL" is "apiUrl" and "HTTPServer" is "httpServer".
func veloxeCamel(s string) string {
	var words []string
	for part := range strings.SplitSeq(s, "_") {
		words = append(words, veloxeWords(part)...)
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

// veloxeWords splits a PascalCase word into words, as veloxe's generator
// did: "UserName" is User and Name, "HTTPServer" is HTTP and Server.
func veloxeWords(s string) []string {
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

// inputScalars are the scalars an input may take that veloxe knew: velox's
// own and the ones applications declared.
var inputScalars = map[string]bool{
	"ID": true, "String": true, "Int": true, "Float": true, "Boolean": true,
	"Time": true, "UUID": true, "JSON": true, "Bytes": true, "Uint64": true,
	"Decimal": true, "Map": true, "Any": true,
}

// inputable reports whether an input may take the type t that graphql.Type
// gives a field, as veloxe judged it: a scalar it knew, an input (a name
// ending in Input), an enum, or a list of one. Any other name is taken to be
// an object type.
func (g *graph) inputable(t string) bool {
	if inner, ok := listOf(t); ok {
		t = inner
	}
	return inputScalars[t] || strings.HasSuffix(t, "Input") || g.enumSet[t] || t == strings.ToUpper(t)
}

// veloxeIDName reports whether veloxe took the field name to be a key's:
// id, a snake *_id, or a camel *ID. veloxe's default predicates for one are
// equality alone.
func veloxeIDName(name string) bool {
	return strings.EqualFold(name, "id") || strings.HasSuffix(strings.ToLower(name), "_id") || len(name) > 2 && strings.HasSuffix(name, "ID")
}

// veloxeIDKinds are the field kinds whose equality is EQ, NEQ, In and NotIn.
var veloxeIDKinds = map[string]bool{
	"string": true, "int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
}
