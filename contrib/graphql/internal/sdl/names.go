package sdl

import (
	"fmt"
	"strings"
	"unicode"
)

// camel is the GraphQL field name of a Go name: its leading capital, or its
// leading acronym, in lower case. "CreatedAt" is "createdAt", "UserID" is
// "userID", "URLPath" is "urlPath", "ID" is "id" and "IDs" is "ids".
func camel(goName string) string {
	r := []rune(goName)
	n := 0
	for n < len(r) && unicode.IsUpper(r[n]) {
		n++
	}
	switch {
	case n == 0:
		return goName
	case n == len(r) || n == 1:
		// all of it is the acronym, or only its first letter is a capital
	case r[n] == 's' && (n+1 == len(r) || unicode.IsUpper(r[n+1])):
		// a plural acronym, as in IDs
	case unicode.IsLower(r[n]):
		n-- // the last capital starts the next word, as in URLPath
	}
	return strings.ToLower(string(r[:n])) + string(r[n:])
}

// validName reports whether s is a GraphQL name.
func validName(s string) bool {
	if s == "" || strings.HasPrefix(s, "__") {
		return false
	}
	for i, c := range s {
		switch {
		case c == '_', 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z':
		case '0' <= c && c <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// validEnumValue reports whether s can be an enum value: a name other than
// true, false and null.
func validEnumValue(s string) bool {
	return validName(s) && s != "true" && s != "false" && s != "null"
}

// builtins are the type names the SDL declares or GraphQL defines, which no
// entity may take.
var builtins = []string{
	"Query", "Mutation", "Subscription",
	"Node", "PageInfo", "OrderDirection",
	"Time", "UUID", "Bytes", "JSON", "Uint64", "Cursor",
	"ID", "Int", "Float", "String", "Boolean",
}

const builtin = "velox-graphql"

// typeNames records which element declares each GraphQL type name, so that
// two that would declare one name fail with both named.
type typeNames map[string]string

func newTypeNames() typeNames {
	n := make(typeNames, len(builtins))
	for _, b := range builtins {
		n[b] = builtin
	}
	return n
}

// add records that owner declares the type name.
func (n typeNames) add(name, owner string) error {
	switch prev, ok := n[name]; {
	case !ok:
		n[name] = owner
		return nil
	case prev == builtin:
		return fmt.Errorf("%s: the GraphQL type %s is one velox-graphql declares itself; rename it with graphql.Type", owner, name)
	default:
		return fmt.Errorf("%s and %s both declare the GraphQL type %s", prev, owner, name)
	}
}

// upper is name with its first letter in upper case: the part of a
// generated name, as in has<Edge> or clear<Field>. GraphQL names are ASCII.
func upper(name string) string {
	if name == "" || name[0] < 'a' || name[0] > 'z' {
		return name
	}
	return string(name[0]-'a'+'A') + name[1:]
}
