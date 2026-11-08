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
	"PageInfo", "OrderDirection",
	"Time", "UUID", "Bytes", "JSON", "Uint64", "Cursor",
	"ID", "Int", "Float", "String", "Boolean",
}

const builtin = "velox-graphql"

// introspection lists GraphQL's own types and the models gqlgen gives them. Its
// autobind runs first, and looks in the generated package for a Go type of the
// name without the "__", so a Go type there named Type would take __Type, and
// the generated server would not build. (introspectionBinds.)
var introspection = []struct{ goName, typ, kw, model string }{
	{"Directive", "__Directive", "type", "github.com/99designs/gqlgen/graphql/introspection.Directive"},
	{"DirectiveLocation", "__DirectiveLocation", "enum", "github.com/99designs/gqlgen/graphql.String"},
	{"EnumValue", "__EnumValue", "type", "github.com/99designs/gqlgen/graphql/introspection.EnumValue"},
	{"Field", "__Field", "type", "github.com/99designs/gqlgen/graphql/introspection.Field"},
	{"InputValue", "__InputValue", "type", "github.com/99designs/gqlgen/graphql/introspection.InputValue"},
	{"Schema", "__Schema", "type", "github.com/99designs/gqlgen/graphql/introspection.Schema"},
	{"Type", "__Type", "type", "github.com/99designs/gqlgen/graphql/introspection.Type"},
	{"TypeKind", "__TypeKind", "enum", "github.com/99designs/gqlgen/graphql.String"},
}

// introspectionBinds is the SDL that keeps gqlgen's models on GraphQL's own
// types whose Go names the generated package takes: "" when it takes none.
func (n typeNames) introspectionBinds() string {
	var b strings.Builder
	for _, t := range introspection {
		_, typed := n.declared[t.goName] // a GraphQL type's Go name is its own
		if _, declared := n.goNames[t.goName]; !typed && !declared {
			continue
		}
		fmt.Fprintf(&b, "extend %s %s @goModel(model: %s)\n", t.kw, t.typ, quote(t.model))
	}
	return b.String()
}

// typeNames records which element declares each GraphQL type name, so that
// two that would declare one name fail with both named. It also knows the Go
// names velox-gen declares in the generated package, which gqlgen's autobind
// would find for a GraphQL type of that name: a type may take one only as the
// name of its own Go type (addGo).
type typeNames struct {
	declared map[string]string // GraphQL type name → what declares it
	goNames  map[string]string // Go name in the generated package → what it is
}

func newTypeNames() typeNames {
	n := typeNames{declared: make(map[string]string, len(builtins)), goNames: map[string]string{}}
	for _, b := range builtins {
		n.declared[b] = builtin
	}
	return n
}

// reserve records that name is a Go name velox-gen declares in the generated
// package, described by what. The first description stands.
func (n typeNames) reserve(name, what string) {
	if name != "" && n.goNames[name] == "" {
		n.goNames[name] = what
	}
}

// add records that owner declares the type name.
func (n typeNames) add(name, owner string) error {
	if err := n.taken(name, owner); err != nil {
		return err
	}
	if what, ok := n.goNames[name]; ok {
		return fmt.Errorf("%s: the GraphQL type %s is also %s in the generated package, where gqlgen would bind it; rename it with graphql.Type", owner, name, what)
	}
	n.declared[name] = owner
	return nil
}

// addGo is add for a type named after its own Go type in the generated
// package.
func (n typeNames) addGo(name, owner string) error {
	if err := n.taken(name, owner); err != nil {
		return err
	}
	n.declared[name] = owner
	return nil
}

// taken is the error for owner declaring a name that something else declares.
func (n typeNames) taken(name, owner string) error {
	switch prev, ok := n.declared[name]; {
	case !ok:
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
