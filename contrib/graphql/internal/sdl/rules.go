package sdl

import (
	"fmt"
	"strings"

	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/contrib/graphql/internal/english"
	"github.com/syssam/velox/plugin"
)

// The kinds of element an annotation is on.
const (
	onSchema = "schema"
	onField  = "field"
	onEdge   = "edge"
)

// misplaced is an error naming the first part of a that does not apply to an
// element of the kind on, or nil. Skip and Directives apply to every kind.
func misplaced(a graphql.Annotation, on, where string) error {
	for _, p := range []struct {
		name string
		set  bool
		on   string // the kinds it applies to
	}{
		{"graphql.Type", a.Type != "", onSchema + " " + onField},
		{"graphql.QueryField", a.HasQueryField(), onSchema},
		{"graphql.Mutations", len(a.Mutations) > 0, onSchema},
		{"graphql.MultiOrder", a.MultiOrder, onSchema},
		{"graphql.Resolvers", len(a.Resolvers) > 0, onSchema},
		{"graphql.FieldName", a.FieldName != "", onField + " " + onEdge},
		{"graphql.OrderField", a.OrderField != "", onField},
		{"graphql.OrderUnguarded", a.OrderUnguarded, onField},
		{"graphql.OrderVia", len(a.OrderVia) > 0, onEdge},
		{"graphql.WhereOps", a.WhereOps != 0, onField},
		{"graphql.WhereEdge", a.WhereEdge, onEdge},
		{"graphql.WhereInput", a.WhereInput, onField + " " + onEdge},
		{"graphql.WhereInputEdges", len(a.WhereInputEdges) > 0, onSchema},
		{"graphql.Connection", a.Connection != nil, onEdge},
		{"graphql.MaxList", a.MaxList != 0, onEdge},
		{"graphql.CreateInput", a.CreateInput != nil, onField},
		{"graphql.UpdateInput", a.UpdateInput != nil, onField},
		{"graphql.CreateDefault", a.CreateDefault != nil, onField},
		{"graphql.EnumValues", len(a.EnumValues) > 0, onField},
	} {
		if p.set && !strings.Contains(p.on, on) {
			kinds := strings.Fields(p.on)
			for i, k := range kinds {
				kinds[i] = article(k)
			}
			return fmt.Errorf("%s: %s does not apply to %s; it applies to %s", where, p.name, article(on), strings.Join(kinds, " or "))
		}
	}
	return nil
}

// article is kind with its indefinite article.
func article(kind string) string { return english.Article(kind) + " " + kind }

// checkSchema checks the annotation of an entity. It refuses an application
// directive; GraphQL's own are allowed. A join entity has no type, so nothing
// that needs one applies to it; a schema whose type is skipped has no root
// field and no mutation, so asking for one is an error.
func checkSchema(e *plugin.Entity, a graphql.Annotation, where string) error {
	if err := ownDirective(a.Directives, where); err != nil {
		return err
	}
	if err := misplaced(a, onSchema, where); err != nil {
		return err
	}
	// gqlgen runs a type's directives only on fields that return the type,
	// so one on an entity guards its objects and leaves its rows' values
	// readable through the cursors, the where inputs, the order and
	// totalCount: a guard that guards nothing.
	if d := appDirective(a); d != "" {
		return fmt.Errorf("%s: graphql.Directives with the directive @%s on a schema, which GraphQL runs on the type's objects only: its rows' values stay readable through the cursors, the filters, the order and totalCount; put the directive on the fields and edges it guards, or use a policy for row access", where, d)
	}
	if !e.Join {
		switch {
		case a.Skip&graphql.SkipType == 0:
		case a.HasQueryField():
			return fmt.Errorf("%s: graphql.QueryField on a schema whose type graphql.Skip leaves out", where)
		case len(a.Mutations) > 0:
			return fmt.Errorf("%s: graphql.Mutations on a schema whose type graphql.Skip leaves out", where)
		case len(a.Resolvers) > 0:
			return fmt.Errorf("%s: graphql.Resolvers on a schema whose type graphql.Skip leaves out", where)
		}
		return nil
	}
	for _, p := range []struct {
		name string
		set  bool
	}{
		{"graphql.Type", a.Type != ""},
		{"graphql.QueryField", a.HasQueryField()},
		{"graphql.Mutations", len(a.Mutations) > 0},
		{"graphql.MultiOrder", a.MultiOrder},
		{"graphql.Resolvers", len(a.Resolvers) > 0},
	} {
		if p.set {
			return fmt.Errorf("%s: %s on a join entity, which has no GraphQL type", where, p.name)
		}
	}
	return nil
}

// checkField checks the annotation of e's field i. fk says whether the field
// is a foreign key, which an edge stands for.
func checkField(e *plugin.Entity, i int, a graphql.Annotation, fk bool, where string) error {
	if err := ownDirective(a.Directives, where); err != nil {
		return err
	}
	if err := misplaced(a, onField, where); err != nil {
		return err
	}
	f := e.Fields[i]
	// A foreign key is never an input, so CreateInput and UpdateInput say
	// nothing on it, whatever their value; the primary key is never one
	// either, and CreateInput(true) on it asks for it.
	var never string
	if fk || f.Implicit != "" {
		never = "a foreign key is never an input: its edge stands for it"
	}
	pk := len(e.PrimaryKey) == 1 && i == e.PrimaryKey[0]
	isTrue := func(p *bool) bool { return p != nil && *p }
	switch {
	case a.Type != "" && !validTypeRef(a.Type):
		return fmt.Errorf("%s: graphql.Type(%q) on a field is not a GraphQL type: it is a name, or a list of one such as [ID!], and the field's nullability adds its non-null mark", where, a.Type)
	case a.Type != "" && pk && a.Type != "ID":
		return fmt.Errorf("%s: graphql.Type(%q) on the key: the key is an ID", where, a.Type)
	case a.Type != "" && f.Type == "enum" && a.Skip&graphql.SkipEnumField == 0 && !validName(a.Type):
		return fmt.Errorf("%s: graphql.Type(%q) on an enum field names its enum, so it is a name", where, a.Type)
	case len(a.EnumValues) > 0 && f.Type != "enum":
		return fmt.Errorf("%s: graphql.EnumValues on a field of type %s, which is not an enum", where, f.Type)
	case a.OrderField != "" && f.Sensitive:
		return fmt.Errorf("%s: graphql.OrderField on a sensitive field: its values would be in the cursors", where)
	case a.OrderField != "" && f.Type == "json":
		return fmt.Errorf("%s: graphql.OrderField on a json field, which has no order", where)
	case a.OrderField != "" && f.Type == "bytes":
		return fmt.Errorf("%s: graphql.OrderField on a bytes field: keyset paging cannot continue after a bytes value, since velox's After refuses a slice column, which a nil []byte stores as NULL", where)
	case a.OrderField != "" && a.Skip&graphql.SkipType != 0 && a.Skip&graphql.SkipOrderField == 0:
		return fmt.Errorf("%s: graphql.OrderField on a field graphql.Skip leaves out of the type: its values would be in the cursors", where)
	case a.OrderField != "" && a.Skip&graphql.SkipOrderField == 0 && !a.OrderUnguarded && appDirective(a) != "":
		return fmt.Errorf("%s: graphql.OrderField on a field with the directive @%s: its values would be in the cursors and its order would reveal them, which no directive guards; add graphql.OrderUnguarded() to allow it", where, appDirective(a))
	case a.OrderUnguarded && a.OrderField == "":
		return fmt.Errorf("%s: graphql.OrderUnguarded without graphql.OrderField says nothing", where)
	case (fk || f.Implicit != "") && appDirective(a) != "":
		return fmt.Errorf("%s: graphql.Directives with the directive @%s on a foreign key, which its edge stands for in GraphQL; put the directive on the edge, which guards the edge's field, its has and hasWith filters and the key's predicates", where, appDirective(a))
	case pk && keyBase(e) != "" && appDirective(a) != "":
		return fmt.Errorf("%s: graphql.Directives with the directive @%s on the primary key: the id is readable through the cursors and every edge to the type, so the directive would guard nothing", where, appDirective(a))
	case a.WhereOps != 0 && f.Sensitive:
		return fmt.Errorf("%s: graphql.WhereOps on a sensitive field: a filter would reveal its values", where)
	case a.WhereOps != 0 && a.Skip&graphql.SkipType != 0 && a.Skip&graphql.SkipWhereInput == 0:
		return fmt.Errorf("%s: graphql.WhereOps on a field graphql.Skip leaves out of the type: a filter would reveal its values", where)
	case a.CreateInput != nil && never != "":
		return fmt.Errorf("%s: graphql.CreateInput: %s", where, never)
	case a.UpdateInput != nil && never != "":
		return fmt.Errorf("%s: graphql.UpdateInput: %s", where, never)
	case a.CreateDefault != nil && never != "":
		return fmt.Errorf("%s: graphql.CreateDefault: %s", where, never)
	case a.CreateDefault != nil && pk:
		return fmt.Errorf("%s: graphql.CreateDefault: the primary key is never an input", where)
	case (isTrue(a.CreateInput) || isTrue(a.UpdateInput)) && pk:
		return fmt.Errorf("%s: graphql.CreateInput(true) or UpdateInput(true): the primary key is never an input", where)
	case isTrue(a.UpdateInput) && f.Immutable:
		return fmt.Errorf("%s: graphql.UpdateInput(true) on an immutable field, which no update may set", where)
	}
	return nil
}

// zeroIsUnset reports whether a field of the type typ is a bool or a number,
// whose zero value an insert reads as unset when the field is not nullable.
func zeroIsUnset(typ string) bool {
	switch typ {
	case "bool", "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "float32", "float64":
		return true
	}
	return false
}

// appDirective is the name of a's first application directive, one other
// than GraphQL's own: "" for none. GraphQL's own guard nothing. (@goField and
// @goModel, velox-graphql's own, are refused by ownDirective before this
// runs.)
func appDirective(a graphql.Annotation) string {
	for _, d := range a.Directives {
		if !prelude[d.Name] {
			return d.Name
		}
	}
	return ""
}
