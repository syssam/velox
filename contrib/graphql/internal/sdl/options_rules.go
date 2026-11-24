package sdl

import (
	"cmp"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/plugin"
)

// This file holds the rules the options apply to the surface that are not
// about names: orderfields=all's order, jsontypes=named's types, idops= and
// the annotations that have no effect.

// orderable reports whether orderfields=all lists the field f by its type:
// a string that is not field.Text, a time, an int or an int64 (no other
// width), a bool, an enum or a float. The key is listed only with
// graphql.OrderField (allOrder).
func orderable(f *plugin.Field) bool {
	switch f.Type {
	case "string":
		return f.Size < math.MaxInt32
	case "time", "int", "int64", "bool", "enum", "float32", "float64":
		return true
	}
	return false
}

// orderName is orderfields=all's value of the field name in an order enum
// when no graphql.OrderField names it: the name in snake case, upper-cased,
// a capital starting a word ("created_at" and "createdAt" are CREATED_AT).
func orderName(name string) string {
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

// namedGo matches a named Go type, after at most one leading *: Name or
// pkg.Name.
var namedGo = regexp.MustCompile(`^\*?(?:[A-Za-z_][A-Za-z0-9_]*\.)?[A-Za-z_][A-Za-z0-9_]*$`)

// jsonType is jsontypes=named's GraphQL type of the json field f by its Go
// type, when that is one velox does not give it: a slice of a named type is
// a list of the name ([]*schematype.TaskAssignee is [TaskAssignee!]), and a
// named type, or a pointer to one, is its name (schematype.CRMSettings is
// CRMSettings). It is "" for any other Go type, which is velox's JSON or
// list of a scalar. The application declares the type.
func jsonType(f *plugin.Field) string {
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
		if t := goScalar(elem); t != "" && !builtinScalar[t] && validName(t) {
			return "[" + t + "!]"
		}
		return "" // a slice of a scalar: velox's list, or JSON
	}
	switch ident {
	case "any", "interface{}", "interface {}", "error":
		return ""
	}
	if !namedGo.MatchString(ident) {
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

// goScalar is the GraphQL type of a json slice's element Go type: its scalar
// for a builtin, the type's name for a named type of a package or one
// starting with a capital, "" for any other.
func goScalar(goType string) string {
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

// equalityDefault reports whether, under idops=equality, the defaults of a
// field's predicates (WhereOps() or WhereInput) are equality alone: for the
// key, for a field named like a key (id, *_id, *ID) of a string or integer
// kind, and for an Other field.
func equalityDefault(name, kind string) bool {
	return kind == "orderedID" || kind == "other" || keyLike(name) && keyKinds[kind]
}

// adjust is a, the annotation of o's field i, as the options read it, and
// the warnings of what it drops. An annotation that has no effect is dropped
// with a warning, as ent ignores one:
//   - an order (OrderField, OrderUnguarded) on a sensitive field, or on one
//     graphql.Skip leaves out of the type: its values would be in the
//     cursors; one that SkipOrderField leaves out says nothing;
//   - predicates (WhereOps) on a sensitive field, or on one graphql.Skip
//     leaves out of the type: a filter would reveal its values;
//   - default predicates (WhereInput, WhereOps()) on a json field that has
//     none: any json field but a slice of scalars.
//
// Under orderfields=all an order on a field of a type the order enum does
// not list (a decimal, a json field, a Text) is dropped with a warning too,
// and under jsontypes=named a json field of a named Go type takes the type's
// name (jsonType), unless graphql.Type gives one.
func (g *graph) adjust(o *object, i int, a graphql.Annotation) (_ graphql.Annotation, warnings []string) {
	f := o.ent.Fields[i]
	pk := o.hasID && i == o.ent.PrimaryKey[0]
	skipped := a.Skip&graphql.SkipType != 0
	field := o.ent.Name + "." + f.Name
	ordered := a.OrderField != "" || a.OrderUnguarded
	switch {
	case a.Skip&graphql.SkipOrderField != 0:
		a.OrderField, a.OrderUnguarded = "", false
	case ordered && f.Sensitive:
		warnings = append(warnings, field+": "+orderAsked(a)+" has no effect on a sensitive field, whose values would be in the cursors")
		a.OrderField, a.OrderUnguarded = "", false
	case ordered && skipped:
		warnings = append(warnings, field+": "+orderAsked(a)+" has no effect on a field graphql.Skip leaves out of the type, whose values would be in the cursors")
		a.OrderField, a.OrderUnguarded = "", false
	case ordered && g.cfg.orderAll() && !pk && !orderable(f):
		warnings = append(warnings, orderWarning(field, f, a))
		a.OrderField, a.OrderUnguarded = "", false
	}
	if a.WhereOps != 0 {
		switch {
		case f.Sensitive:
			warnings = append(warnings, field+": graphql.WhereOps has no effect on a sensitive field: a filter would reveal its values")
			a.WhereOps = 0
		case skipped && a.Skip&graphql.SkipWhereInput == 0:
			warnings = append(warnings, field+": graphql.WhereOps has no effect on a field graphql.Skip leaves out of the type: a filter would reveal its values")
			a.WhereOps = 0
		case skipped:
			a.WhereOps = 0
		case f.Type == "json" && jsonList[f.ElemType] == "" && a.WhereOps == graphql.OpsDefault:
			warnings = append(warnings, field+": graphql.WhereOps has no effect on a json field: only a slice of scalars has predicates")
			a.WhereOps = 0
		}
	}
	if g.cfg.namedJSON() && f.Type == "json" && a.Type == "" {
		a.Type = jsonType(f)
	}
	return a, warnings
}

// orderAsked is the order annotation a gives, as the schema wrote it.
func orderAsked(a graphql.Annotation) string {
	if a.OrderField != "" {
		return "graphql.OrderField(" + strconv.Quote(a.OrderField) + ")"
	}
	return "graphql.OrderUnguarded()"
}

// orderWarning is the warning that the order a asks for on f, the field
// named field (Entity.field) of a type orderfields=all does not order, is
// dropped:
//
//	Thing.amount: graphql.OrderField("AMOUNT") is dropped: orderfields=all
//	orders no field of type other (decimal.Decimal)
func orderWarning(field string, f *plugin.Field, a graphql.Annotation) string {
	typ := f.Type
	switch {
	case f.Type == "string":
		typ = "string of unbounded size (field.Text)"
	case f.ElemType != "" && f.ElemType != f.Type:
		typ += " (" + f.ElemType + ")"
	}
	return fmt.Sprintf("%s: %s is dropped: orderfields=all orders no field of type %s", field, orderAsked(a), typ)
}

// allOrder computes o's order under orderfields=all, when o has a
// connection and does not skip its order: the key first when its
// graphql.OrderField names it, then every orderable field, in the schema's
// order, each named by its graphql.OrderField or orderName. The fields are
// those of the entity's that are neither sensitive nor left out of the type
// or the order: a foreign key it declares too, and an implicit one never. No
// directive guards the order here: an application's directive there is its
// own.
func allOrder(o *object, ord *order, by map[string]string) error {
	e := o.ent
	add := func(i int) error {
		f, a := e.Fields[i], o.fanns[i]
		v := cmp.Or(a.OrderField, orderName(f.Name))
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
		case o.hasID && i == e.PrimaryKey[0], f.Implicit != "", f.Sensitive, !orderable(f),
			a.Skip&(graphql.SkipType|graphql.SkipOrderField) != 0:
			continue
		}
		if err := add(i); err != nil {
			return err
		}
	}
	return nil
}

// warnUnguarded warns of each field of o that carries an application
// directive and still has where predicates or an order value: the
// directive guards the field in the type, but a filter or an order would
// reveal its values, and neither carries the directive. An order value the
// field accepts with graphql.OrderUnguarded is not warned of. The key is
// left out: checkField warns that a directive on it has no effect.
func (o *object) warnUnguarded() []string {
	var warnings []string
	for i, f := range o.ent.Fields {
		a := o.fanns[i]
		d := appDirective(a)
		if d == "" || o.hasID && i == o.ent.PrimaryKey[0] {
			continue
		}
		var what, drop string
		switch where, ordered := o.filtersBy(i), !a.OrderUnguarded && o.ordersBy(i); {
		case where && ordered:
			what, drop = "its where predicates and order value are", "drop graphql.WhereOps or graphql.WhereInput and the order (graphql.OrderField, or graphql.Skip(graphql.SkipOrderField) under orderfields=all)"
		case where:
			what, drop = "its where predicates are", "drop graphql.WhereOps or graphql.WhereInput"
		case ordered:
			what, drop = "its order value is", "drop the order (graphql.OrderField, or graphql.Skip(graphql.SkipOrderField) under orderfields=all), add graphql.OrderUnguarded() to accept it"
		default:
			continue
		}
		warnings = append(warnings, fmt.Sprintf("%s.%s has @%s, but %s not guarded by it; %s, or guard the field another way", o.ent.Name, f.Name, d, what, drop))
	}
	return warnings
}

// filtersBy reports whether o's where input has predicates on field i.
func (o *object) filtersBy(i int) bool {
	for _, w := range o.whereGo {
		if w.Kind == WherePred && w.Field == i {
			return true
		}
	}
	return false
}

// ordersBy reports whether o's order has a value of its own field i.
func (o *object) ordersBy(i int) bool {
	if o.order == nil {
		return false
	}
	for j, fi := range o.order.fields {
		if fi == i && o.order.edges[j] == 0 && !o.order.counts[j] {
			return true
		}
	}
	return false
}
