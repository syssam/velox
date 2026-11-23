package sdl

import (
	"strings"
	"unicode"
)

// Config holds velox-graphql's options for one model.
type Config struct {
	// Veloxe makes the API the one veloxe's generator made, for an
	// application whose clients are built against it (velox-graphql's
	// option compat=veloxe):
	//   - a field or edge is named by veloxeCamel of its schema name, so
	//     customer_id is customerId and image_url is imageUrl, where velox
	//     names it from its Go name (customerID, imageURL);
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
	//   - the interface Node, which every type with an ID implements, and
	//     node(id:) and nodes(ids:) first in the root query;
	//   - a where input has veloxe's semantics (gqlgraph.Type's Veloxe).
	Veloxe bool
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
	// its Noder with @goModel under Veloxe, and every type under
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
	// nests. The Go velox-graphql writes is the whole schema's.
	Entities []string
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
