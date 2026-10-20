package sdl

import (
	"fmt"
	"strings"

	"github.com/syssam/velox2/contrib/graphql"
	"github.com/syssam/velox2/plugin"
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
		{"graphql.FieldName", a.FieldName != "", onField + " " + onEdge},
		{"graphql.OrderField", a.OrderField != "", onField},
		{"graphql.WhereOps", a.WhereOps != 0, onField},
		{"graphql.WhereEdge", a.WhereEdge, onEdge},
		{"graphql.Connection", a.Connection != nil, onEdge},
		{"graphql.CreateInput", a.CreateInput != nil, onField},
		{"graphql.UpdateInput", a.UpdateInput != nil, onField},
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
func article(kind string) string {
	if kind == onEdge {
		return "an " + kind
	}
	return "a " + kind
}

// checkSchema checks the annotation of an entity. A join entity has no
// type, so nothing that needs one applies to it; a schema whose type is
// skipped has no root field and no mutation, so asking for one is an error.
func checkSchema(e *plugin.Entity, a graphql.Annotation, where string) error {
	if err := misplaced(a, onSchema, where); err != nil {
		return err
	}
	if !e.Join {
		switch {
		case a.Skip&graphql.SkipType == 0:
		case a.QueryField != "":
			return fmt.Errorf("%s: graphql.QueryField on a schema whose type graphql.Skip leaves out", where)
		case len(a.Mutations) > 0:
			return fmt.Errorf("%s: graphql.Mutations on a schema whose type graphql.Skip leaves out", where)
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
	if err := misplaced(a, onField, where); err != nil {
		return err
	}
	f := e.Fields[i]
	// The tenant and a foreign key are never inputs, so CreateInput and
	// UpdateInput say nothing on them, whatever their value; the primary key
	// is never one either, and CreateInput(true) on it asks for it.
	var never string
	switch {
	case i == e.Tenant:
		never = "the tenant is never an input: the tenant scope fills it"
	case fk || f.Implicit != "":
		never = "a foreign key is never an input: its edge stands for it"
	}
	pk := len(e.PrimaryKey) == 1 && i == e.PrimaryKey[0]
	isTrue := func(p *bool) bool { return p != nil && *p }
	switch {
	case len(a.EnumValues) > 0 && f.Type != "enum":
		return fmt.Errorf("%s: graphql.EnumValues on a field of type %s, which is not an enum", where, f.Type)
	case a.OrderField != "" && f.Nullable:
		return fmt.Errorf("%s: graphql.OrderField on an optional field: keyset paging needs an order column without NULLs", where)
	case a.OrderField != "" && f.Sensitive:
		return fmt.Errorf("%s: graphql.OrderField on a sensitive field: its values would be in the cursors", where)
	case a.OrderField != "" && f.Type == "json":
		return fmt.Errorf("%s: graphql.OrderField on a json field, which has no order", where)
	case a.OrderField != "" && f.Type == "bytes":
		return fmt.Errorf("%s: graphql.OrderField on a bytes field: keyset paging cannot continue after a bytes value, since velox's After refuses a slice column, which a nil []byte stores as NULL", where)
	case a.OrderField != "" && a.Skip&graphql.SkipType != 0 && a.Skip&graphql.SkipOrderField == 0:
		return fmt.Errorf("%s: graphql.OrderField on a field graphql.Skip leaves out of the type: its values would be in the cursors", where)
	case a.WhereOps != 0 && f.Sensitive:
		return fmt.Errorf("%s: graphql.WhereOps on a sensitive field: a filter would reveal its values", where)
	case a.WhereOps != 0 && a.Skip&graphql.SkipType != 0 && a.Skip&graphql.SkipWhereInput == 0:
		return fmt.Errorf("%s: graphql.WhereOps on a field graphql.Skip leaves out of the type: a filter would reveal its values", where)
	case a.WhereOps != 0 && i == e.Tenant:
		return fmt.Errorf("%s: graphql.WhereOps on the tenant: the tenant scope filters on it", where)
	case a.CreateInput != nil && never != "":
		return fmt.Errorf("%s: graphql.CreateInput: %s", where, never)
	case a.UpdateInput != nil && never != "":
		return fmt.Errorf("%s: graphql.UpdateInput: %s", where, never)
	case (isTrue(a.CreateInput) || isTrue(a.UpdateInput)) && pk:
		return fmt.Errorf("%s: graphql.CreateInput(true) or UpdateInput(true): the primary key is never an input", where)
	case isTrue(a.UpdateInput) && f.Immutable:
		return fmt.Errorf("%s: graphql.UpdateInput(true) on an immutable field, which no update may set", where)
	}
	return nil
}
