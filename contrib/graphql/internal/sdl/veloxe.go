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
	//     edge (customerID, customerIDIn).
	Veloxe bool
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
