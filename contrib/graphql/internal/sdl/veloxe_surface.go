package sdl

import (
	"cmp"
	"math"
	"regexp"
	"strings"
	"unicode"

	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/plugin"
)

// This file holds the rules of compat=veloxe that are not about names: what
// veloxe's generator put in its surface, so that velox can make the same.

// veloxeOrderable reports whether veloxe's order enum listed the field f by
// its type: a string that is not field.Text, a time, an int or an int64 (no
// other width), a bool, an enum or a float. The key is listed only with
// graphql.OrderField (veloxeOrder).
func veloxeOrderable(f *plugin.Field) bool {
	switch f.Type {
	case "string":
		return f.Size < math.MaxInt32
	case "time", "int", "int64", "bool", "enum", "float32", "float64":
		return true
	}
	return false
}

// veloxeOrderName is veloxe's value of the field name in an order enum when
// no graphql.OrderField names it: the name in snake case, upper-cased, a
// capital starting a word ("created_at" and "createdAt" are CREATED_AT).
func veloxeOrderName(name string) string {
	var b strings.Builder
	for i, r := range name {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('_')
			}
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return strings.ToUpper(b.String())
}

// veloxeNamedGo matches a named Go type as veloxe's generator took one, after
// at most one leading *: Name or pkg.Name.
var veloxeNamedGo = regexp.MustCompile(`^\*?(?:[A-Za-z_][A-Za-z0-9_]*\.)?[A-Za-z_][A-Za-z0-9_]*$`)

// veloxeJSONType is the GraphQL type veloxe's generator gave the json field
// f by its Go type, when that is one velox does not give it: a slice of a
// named type is a list of the name ([]*schematype.TaskAssignee is
// [TaskAssignee!]), and a named type, or a pointer to one, is its name
// (schematype.CRMSettings is CRMSettings). It is "" for any other Go type,
// which is velox's JSON or list of a scalar. The application declares the
// type, as it did under veloxe, whose SDL only named it.
func veloxeJSONType(f *plugin.Field) string {
	ident := strings.TrimSpace(cmp.Or(f.ElemType, f.GoType)) // the schema's Go type, without Nillable's pointer
	if ident == "" || f.Type != "json" {
		return ""
	}
	if strings.HasPrefix(ident, "[]") || f.Kind == "slice" || f.Kind == "array" {
		elem := strings.TrimPrefix(strings.TrimPrefix(ident, "[]"), "*")
		switch {
		case strings.HasPrefix(elem, "[]"), strings.HasPrefix(elem, "map["), elem == "interface{}", elem == "any":
			return ""
		}
		if t := veloxeGoScalar(elem); t != "" && !builtinScalar[t] && validName(t) {
			return "[" + t + "!]"
		}
		return "" // a slice of a scalar: velox's list, or JSON
	}
	switch ident {
	case "any", "interface{}", "interface {}", "error":
		return ""
	}
	if !veloxeNamedGo.MatchString(ident) {
		return ""
	}
	name := strings.TrimPrefix(ident, "*")
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	if builtinScalar[name] || !validName(name) {
		return ""
	}
	return name
}

// builtinScalar are the scalars GraphQL defines and those velox gives a
// field by itself, which a json field's derived type never names: velox's
// own type serves the field.
var builtinScalar = map[string]bool{
	"String": true, "Int": true, "Float": true, "Boolean": true, "ID": true,
	"Time": true, "UUID": true, "JSON": true, "Bytes": true, "Uint64": true,
}

// veloxeGoScalar is veloxe's GraphQL type of a json slice's element Go type:
// its scalar for a builtin, the type's name for a named type of a package or
// one starting with a capital, "" for any other.
func veloxeGoScalar(goType string) string {
	switch goType {
	case "string":
		return "String"
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64":
		return "Int"
	case "float32", "float64":
		return "Float"
	case "bool":
		return "Boolean"
	case "time.Time":
		return "Time"
	}
	switch {
	case goType == "uuid.UUID" || strings.HasSuffix(goType, ".UUID"):
		return "UUID"
	case strings.Contains(goType, "."):
		return goType[strings.LastIndex(goType, ".")+1:]
	case goType != "" && goType[0] >= 'A' && goType[0] <= 'Z':
		return goType
	}
	return ""
}

// veloxeEqualityDefault reports whether, under compat=veloxe, the defaults of
// a field's predicates (WhereOps() or WhereInput) are equality alone, as
// veloxe's were: for the key, for a field named as a key (id, *_id, *ID) of a
// string or integer kind, and for an Other field, which veloxe did not take
// to be ordered.
func veloxeEqualityDefault(name, kind string) bool {
	return kind == "orderedID" || kind == "other" || veloxeIDName(name) && veloxeIDKinds[kind]
}

// veloxeAnnotation is a, the annotation of o's field i, as veloxe read it:
// what veloxe ignored is dropped, silently, where velox refuses it.
//   - An order (OrderField, OrderUnguarded) on a field veloxe's order enum
//     does not list: one not veloxeOrderable but the key, a sensitive one,
//     or one Skip leaves out of the type or the order. veloxe lists every
//     orderable field, and an OrderField only names one.
//   - Predicates (WhereOps) on a sensitive field, on one Skip leaves out of
//     the type, or on a json field veloxe gave none: any json field but a
//     slice of scalars with a JSON array op.
//   - A json field of a named Go type takes the type's name (veloxeJSONType)
//     as graphql.Type would give it, unless graphql.Type gives one.
func veloxeAnnotation(o *object, i int, a graphql.Annotation) graphql.Annotation {
	f := o.ent.Fields[i]
	pk := o.hasID && i == o.ent.PrimaryKey[0]
	skipped := a.Skip&graphql.SkipType != 0
	if f.Sensitive || skipped || a.Skip&graphql.SkipOrderField != 0 || !pk && !veloxeOrderable(f) {
		a.OrderField, a.OrderUnguarded = "", false
	}
	switch {
	case f.Sensitive, skipped:
		a.WhereOps = 0
	case f.Type == "json" && (jsonList[f.ElemType] == "" || a.WhereOps&graphql.OpsJSONArray == 0):
		a.WhereOps = 0
	}
	if f.Type == "json" && a.Type == "" {
		a.Type = veloxeJSONType(f)
	}
	return a
}

// veloxeOrder computes o's order as veloxe's generator made it, when o has a
// connection and does not skip its order: the key first when its
// graphql.OrderField names it, then every field veloxe's order enum listed,
// in the schema's order, each named by its graphql.OrderField or
// veloxeOrderName. The fields are those of the entity's that are neither
// sensitive nor left out of the type or the order: a foreign key it declares
// too, and an implicit one never. No directive guards the order here, as
// none did in veloxe: an application's directive there is its own.
func veloxeOrder(o *object, ord *order, by map[string]string) error {
	e := o.ent
	add := func(i int) error {
		f, a := e.Fields[i], o.fanns[i]
		v := cmp.Or(a.OrderField, veloxeOrderName(f.Name))
		return ord.add(v, i, "schema "+e.Name+", field "+f.Name, by)
	}
	if o.hasID && o.fanns[e.PrimaryKey[0]].OrderField != "" {
		if err := add(e.PrimaryKey[0]); err != nil {
			return err
		}
	}
	for i, f := range e.Fields {
		a := o.fanns[i]
		switch {
		case o.hasID && i == e.PrimaryKey[0], f.Implicit != "", f.Sensitive, !veloxeOrderable(f),
			a.Skip&(graphql.SkipType|graphql.SkipOrderField) != 0:
			continue
		}
		if err := add(i); err != nil {
			return err
		}
	}
	return nil
}
