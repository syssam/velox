package sdl

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/plugin"
)

// ownDirective refuses @goField and @goModel in ds, the directives of the
// element where: they are velox-graphql's own, not the application's. The
// annotation checks call it for every element, since the directives of some
// (a skipped entity, a join entity, a primary key, a foreign key) reach no
// SDL field, and dropping them there would fail open.
func ownDirective(ds []graphql.Directive, where string) error {
	for _, d := range ds {
		switch d.Name {
		case "goField":
			return fmt.Errorf("%s: @goField is velox-graphql's own: it binds fields to the Go code velox generates", where)
		case "goModel":
			return fmt.Errorf("%s: @goModel is velox-graphql's own: it binds types to the Go code velox generates and the application's enum types", where)
		}
	}
	return nil
}

// ownDirectives refuses velox-graphql's own directives on the fields and
// edges of e, an entity with no GraphQL type, whose annotations are otherwise
// not checked.
func ownDirectives(e *plugin.Entity, where string) error {
	for _, f := range e.Fields {
		fw := where + ", field " + f.Name
		a, err := decode(f.Annotations, fw)
		if err != nil {
			return err
		}
		if err := ownDirective(a.Directives, fw); err != nil {
			return err
		}
	}
	for _, ed := range e.Edges {
		ew := where + ", edge " + ed.Name
		a, err := decode(ed.Annotations, ew)
		if err != nil {
			return err
		}
		if err := ownDirective(a.Directives, ew); err != nil {
			return err
		}
	}
	return nil
}

// directives renders ds, the directives of the element where.
func directives(ds []graphql.Directive, where string) ([]directive, error) {
	out := make([]directive, 0, len(ds))
	for _, d := range ds {
		if !validName(d.Name) {
			return nil, fmt.Errorf("%s: the directive name %q is not a GraphQL name", where, d.Name)
		}
		if err := ownDirective([]graphql.Directive{d}, where); err != nil {
			return nil, err
		}
		s := "@" + d.Name
		if len(d.Args) > 0 {
			args := make([]string, 0, len(d.Args))
			for _, k := range slices.Sorted(maps.Keys(d.Args)) {
				if !validName(k) {
					return nil, fmt.Errorf("%s: directive @%s: the argument name %q is not a GraphQL name", where, d.Name, k)
				}
				v, err := value(d.Args[k])
				if err != nil {
					return nil, fmt.Errorf("%s: directive @%s, argument %s: %w", where, d.Name, k, err)
				}
				args = append(args, k+": "+v)
			}
			s += "(" + strings.Join(args, ", ") + ")"
		}
		out = append(out, directive{name: d.Name, text: s})
	}
	return out, nil
}

// value is v, a JSON value, as a GraphQL value.
func value(v any) (string, error) {
	switch v := v.(type) {
	case nil:
		return "null", nil
	case bool:
		return strconv.FormatBool(v), nil
	case json.Number:
		return v.String(), nil // JSON's number syntax is GraphQL's
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), nil
	case string:
		return quote(v), nil
	case []any:
		items := make([]string, 0, len(v))
		for _, x := range v {
			s, err := value(x)
			if err != nil {
				return "", err
			}
			items = append(items, s)
		}
		return "[" + strings.Join(items, ", ") + "]", nil
	case map[string]any:
		items := make([]string, 0, len(v))
		for _, k := range slices.Sorted(maps.Keys(v)) {
			if !validName(k) {
				return "", fmt.Errorf("the object field name %q is not a GraphQL name", k)
			}
			s, err := value(v[k])
			if err != nil {
				return "", err
			}
			items = append(items, k+": "+s)
		}
		return "{" + strings.Join(items, ", ") + "}", nil
	default:
		return "", fmt.Errorf("a value of type %T", v)
	}
}

// quote is s as a GraphQL string.
func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
