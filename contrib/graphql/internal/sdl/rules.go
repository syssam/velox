package sdl

import (
	"encoding/json"
	"fmt"
	"strconv"
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
		{"graphql.Type", a.Type != "", onSchema},
		{"graphql.QueryField", a.QueryField != "", onSchema},
		{"graphql.Mutations", len(a.Mutations) > 0, onSchema},
		{"graphql.MultiOrder", a.MultiOrder, onSchema},
		{"graphql.Resolvers", len(a.Resolvers) > 0, onSchema},
		{"graphql.FieldName", a.FieldName != "", onField + " " + onEdge},
		{"graphql.OrderField", a.OrderField != "", onField},
		{"graphql.OrderUnguarded", a.OrderUnguarded, onField},
		{"graphql.OrderVia", len(a.OrderVia) > 0, onEdge},
		{"graphql.WhereOps", a.WhereOps != 0, onField},
		{"graphql.WhereEdge", a.WhereEdge, onEdge},
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
		case a.QueryField != "":
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
		{"graphql.QueryField", a.QueryField != ""},
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
	case f.HasDefault && !f.Nullable && zeroIsUnset(f.Type) && !zeroLiteral(f.Default) && (f.Type == "bool" || !zeroInvalid(f.Checks)):
		// An insert fills the Default into a zero value, so the field could
		// never store false or 0, and a create input could not set it. A
		// zero Default stores what unset does, and so does any on a number a
		// check refuses 0 for.
		return fmt.Errorf("%s: a Default on a bool or number that is not Nillable cannot be told from unset, unless it is the zero value or a check refuses 0; velox-gen refuses it too", where)
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

// zeroLiteral reports whether d, a field's literal Default, is false or a
// number equal to 0; a func Default, which the model gives none of, is not.
func zeroLiteral(d json.RawMessage) bool {
	switch s := strings.TrimSpace(string(d)); s {
	case "":
		return false
	case "false":
		return true
	default:
		n, err := strconv.ParseFloat(s, 64)
		return err == nil && n == 0
	}
}

// zeroInvalid reports whether a check of a number refuses 0: Positive,
// Negative, a Min above 0, a Max below 0, or a Range that excludes it. It is
// velox-gen's rule of the same name, read from the model's checks.
func zeroInvalid(checks []plugin.Check) bool {
	arg := func(c plugin.Check, i int) (float64, bool) {
		var v float64
		return v, i < len(c.Args) && json.Unmarshal(c.Args[i], &v) == nil
	}
	for _, c := range checks {
		lo, okLo := arg(c, 0)
		hi, okHi := arg(c, 1)
		switch {
		case c.Kind == "Positive" || c.Kind == "Negative":
			return true
		case c.Kind == "Min" && okLo && lo > 0, c.Kind == "Max" && okLo && lo < 0:
			return true
		case c.Kind == "Range" && okLo && okHi && (lo > 0 || hi < 0):
			return true
		}
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
