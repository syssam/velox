package sdl

import (
	"fmt"
	"slices"
	"strings"

	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/plugin"
)

// The arguments a predicate takes.
const (
	takesValue = iota // the field's type
	takesList         // a list of it
	takesBool         // a Boolean
)

// predicates are the WhereOps, in the order a where input lists them, with
// the suffix of their names as entgql writes them.
var predicates = []struct {
	op     graphql.WhereOp
	name   string // the constant's name, for errors
	suffix string
	takes  int
}{
	{graphql.OpEQ, "OpEQ", "", takesValue},
	{graphql.OpNEQ, "OpNEQ", "NEQ", takesValue},
	{graphql.OpIn, "OpIn", "In", takesList},
	{graphql.OpNotIn, "OpNotIn", "NotIn", takesList},
	{graphql.OpGT, "OpGT", "GT", takesValue},
	{graphql.OpGTE, "OpGTE", "GTE", takesValue},
	{graphql.OpLT, "OpLT", "LT", takesValue},
	{graphql.OpLTE, "OpLTE", "LTE", takesValue},
	{graphql.OpContains, "OpContains", "Contains", takesValue},
	{graphql.OpHasPrefix, "OpHasPrefix", "HasPrefix", takesValue},
	{graphql.OpHasSuffix, "OpHasSuffix", "HasSuffix", takesValue},
	{graphql.OpIsNil, "OpIsNil", "IsNil", takesBool},
	{graphql.OpNotNil, "OpNotNil", "NotNil", takesBool},
}

// opNames is the constants' names of ops, for errors.
func opNames(ops graphql.WhereOp) string {
	var names []string
	for _, p := range predicates {
		if ops&p.op != 0 {
			names = append(names, p.name)
			ops &^= p.op
		}
	}
	if ops != 0 {
		names = append(names, fmt.Sprintf("WhereOp(%#x)", uint32(ops)))
	}
	return strings.Join(names, ", ")
}

// predicatesOf are the predicates a field of the type typ offers (has) and
// those WhereOps() gives it (defaults). typ is "ID" for the node key of an
// enum or uuid type, "orderedID" for that of an integer or string type, else
// the schema field type. A nullable field, one whose handle tests for NULL, also
// has OpsNullable.
func predicatesOf(typ string, nullable bool) (has, defaults graphql.WhereOp) {
	switch typ {
	case "string":
		has, defaults = graphql.OpsEquality|graphql.OpsComparison|graphql.OpsString, graphql.OpsEquality|graphql.OpsString
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64", "time":
		has, defaults = graphql.OpsEquality|graphql.OpsComparison, graphql.OpsEquality|graphql.OpsComparison
	case "bool":
		has, defaults = graphql.OpEQ|graphql.OpNEQ, graphql.OpEQ|graphql.OpNEQ
	case "orderedID":
		has, defaults = graphql.OpsEquality|graphql.OpsComparison, graphql.OpsEquality|graphql.OpsComparison
	case "ID", "enum", "uuid":
		has, defaults = graphql.OpsEquality, graphql.OpsEquality
	default: // bytes, json
		return 0, 0
	}
	if nullable {
		has |= graphql.OpsNullable
		defaults |= graphql.OpsNullable
	}
	return has, defaults
}

// resolveOps is the predicates ops gives a field of the type typ:
//   - OpsDefault stands for the type's defaults;
//   - OpsAll is every predicate the type has;
//   - any other op must be one the type has.
func resolveOps(ops graphql.WhereOp, typ string, nullable bool, where string) (graphql.WhereOp, error) {
	has, defaults := predicatesOf(typ, nullable)
	if has == 0 {
		return 0, fmt.Errorf("%s: graphql.WhereOps on a field of type %s, which has no predicates", where, typ)
	}
	var out graphql.WhereOp
	if ops&graphql.OpsDefault != 0 {
		out, ops = defaults, ops&^graphql.OpsDefault
	}
	if ops&graphql.OpsAll == graphql.OpsAll {
		return has, nil
	}
	if bad := ops &^ has; bad != 0 {
		kind := "a non-nullable"
		if nullable {
			kind = "a nullable"
		}
		if typ == "orderedID" { // an internal kind: the key is an ID to the user
			typ = "ID"
		}
		return 0, fmt.Errorf("%s: graphql.WhereOps(%s): not a predicate of %s %s field, which has %s", where, opNames(bad), kind, typ, opNames(has))
	}
	return out | ops, nil
}

// wantsWhere reports whether o has a where input: a field with WhereOps or
// an edge with WhereEdge, and the entity does not skip it.
func (g *graph) wantsWhere(o *object) bool {
	if o.ann.Skip&graphql.SkipWhereInput != 0 {
		return false
	}
	for _, a := range o.fanns {
		if a.WhereOps != 0 && a.Skip&graphql.SkipWhereInput == 0 {
			return true
		}
	}
	for i, ed := range o.ent.Edges {
		if a := o.eanns[i]; a.WhereEdge && a.Skip&graphql.SkipWhereInput == 0 && g.byName[ed.Target] != nil {
			return true
		}
	}
	return false
}

// whereOf computes o's where input, XWhereInput, when o.hasWhere: not, and,
// or; each field's predicates; each edge's has and hasWith.
func (g *graph) whereOf(o *object) error {
	if !o.hasWhere {
		return nil
	}
	e := o.ent
	in := o.name + "WhereInput"
	if err := g.types.add(in, "schema "+e.Name); err != nil {
		return err
	}
	fs := newInputFields(in)
	for _, n := range []struct {
		name, typ, goName string
		kind              WhereKind
	}{
		{"not", in, "Not", WhereNot}, {"and", "[" + in + "!]", "And", WhereAnd}, {"or", "[" + in + "!]", "Or", WhereOr},
	} {
		fs.own(gqlField{name: n.name, typ: n.typ})
		o.whereGo = append(o.whereGo, WhereField{Name: n.name, GoName: n.goName, Type: n.typ, Kind: n.kind, Field: -1, Edge: -1, Target: o.name, On: o.name})
	}
	for i, f := range e.Fields {
		a := o.fanns[i]
		key := o.node && i == e.PrimaryKey[0]
		if key && a.WhereOps == 0 {
			// The node key filters by default; an explicit WhereOps wins.
			a.WhereOps = graphql.OpsDefault
		}
		if a.WhereOps == 0 || a.Skip&graphql.SkipWhereInput != 0 {
			continue
		}
		where := "schema " + e.Name + ", field " + f.Name
		var name, typ, kind, goBase, target string
		edge, v := -1, goValue{typ: "string"} // an ID is a string
		// The predicates carry the field's directives, and a foreign key's
		// its edge's too, so that what guards the field guards its filters.
		dirs, err := directives(a.Directives, where)
		if err != nil {
			return err
		}
		switch {
		case key:
			// Only an enum or a uuid key is unordered.
			kind = "orderedID"
			if f.Type == "enum" || f.Type == "uuid" {
				kind = "ID"
			}
			name, typ, goBase, target = "id", "ID", "ID", o.name
		case o.fks[f.Name] || f.Implicit != "":
			// A foreign key is never a raw scalar: its edge names it, and it
			// takes the IDs of the edge's target.
			j := g.keyEdge(o, f)
			if j < 0 {
				return fmt.Errorf("%s: graphql.WhereOps on a foreign key whose edge is not in GraphQL: an edge's ID names a foreign key's predicates", where)
			}
			en, err := o.edgeName(j)
			if err != nil {
				return err
			}
			ed := e.Edges[j]
			name, typ, kind, goBase, target, edge = en+"ID", "ID", "ID", goOf(en, ed.GoName)+"ID", g.byName[ed.Target].name, j
			eds, err := directives(o.eanns[j].Directives, "schema "+e.Name+", edge "+ed.Name)
			if err != nil {
				return err
			}
			dirs = unite(dirs, eds)
		default:
			n, err := o.fieldName(i)
			if err != nil {
				return err
			}
			t, err := g.fieldType(o, f, a, where)
			if err != nil {
				return err
			}
			name, typ, kind, goBase, v = n, t, f.Type, goOf(n, f.GoName), g.goValue(f, a)
		}
		on := name
		if edge >= 0 {
			on = strings.TrimSuffix(name, "ID")
		}
		// IsNil and NotNil are the handle's IsNull and NotNull, which only a
		// nullable field's Null* handle has: a Nillable field's. An Optional
		// JSON field's column holds NULL too, but JSON has no predicates.
		ops, err := resolveOps(a.WhereOps, kind, f.Nullable, where)
		if err != nil {
			return err
		}
		for _, p := range predicates {
			if ops&p.op == 0 {
				continue
			}
			pf := gqlField{name: name + p.suffix, typ: typ, dirs: dirs, where: where}
			switch p.takes {
			case takesList:
				pf.typ = "[" + typ + "!]"
			case takesBool:
				pf.typ = "Boolean"
			}
			if err := fs.add(pf); err != nil {
				return err
			}
			wf := WhereField{
				Name: pf.name, GoName: goBase + p.suffix, Type: pf.typ, Kind: WherePred, Op: p.op, Field: i, Edge: edge, Target: target, On: on,
				GoType: v.typ, GoPkg: v.pkg,
			}
			if p.takes == takesBool {
				wf.GoType, wf.GoPkg = "bool", ""
			}
			o.whereGo = append(o.whereGo, wf)
		}
	}
	for i, ed := range e.Edges {
		a, t := o.eanns[i], g.byName[ed.Target]
		if !a.WhereEdge || a.Skip&graphql.SkipWhereInput != 0 || t == nil {
			continue
		}
		where := "schema " + e.Name + ", edge " + ed.Name
		name, err := o.edgeName(i)
		if err != nil {
			return err
		}
		dirs, err := directives(a.Directives, where)
		if err != nil {
			return err
		}
		has := gqlField{name: "has" + upper(name), typ: "Boolean", dirs: dirs, where: where}
		if err := fs.add(has); err != nil {
			return err
		}
		goHas := "Has" + goOf(name, ed.GoName)
		o.whereGo = append(o.whereGo, WhereField{Name: has.name, GoName: goHas, Type: has.typ, Kind: WhereHas, Field: -1, Edge: i, Target: t.name, On: name, GoType: "bool"})
		// hasWith takes the target's where input; a target without one
		// offers has alone.
		if t.hasWhere {
			with := gqlField{name: "has" + upper(name) + "With", typ: "[" + t.name + "WhereInput!]", dirs: dirs, where: where}
			if err := fs.add(with); err != nil {
				return err
			}
			o.whereGo = append(o.whereGo, WhereField{Name: with.name, GoName: goHas + "With", Type: with.typ, Kind: WhereHasWith, Field: -1, Edge: i, Target: t.name, On: name})
		}
	}
	o.where = fs.list
	return nil
}

// unite is ds followed by those of more whose names ds lacks: GraphQL allows
// a directive once where it is, unless it is repeatable, and the plugin
// cannot tell which are.
func unite(ds, more []directive) []directive {
	out := slices.Clone(ds)
	for _, d := range more {
		if !slices.ContainsFunc(out, func(x directive) bool { return x.name == d.name }) {
			out = append(out, d)
		}
	}
	return out
}

// keyEdge is the index in o.ent.Edges of the unique edge that f, a foreign
// key on o's rows, stands for, and that GraphQL can name: -1 for none. It is
// the edge that owns the key (OwnsKey). FieldOn cannot tell: on an edge
// from o to itself both edges of a pair name f on o, and only the owner's
// target is the row f points to.
func (g *graph) keyEdge(o *object, f *plugin.Field) int {
	for i, ed := range o.ent.Edges {
		t := g.byName[ed.Target]
		if ed.Unique && ed.OwnsKey && ed.Field == f.Name && t != nil && t.node && o.eanns[i].Skip&graphql.SkipWhereInput == 0 {
			return i
		}
	}
	return -1
}

// whereArg is the where argument of a connection to t's nodes, "" when t has
// no where input.
func whereArg(t *object) string {
	if !t.hasWhere {
		return ""
	}
	return ", where: " + t.name + "WhereInput"
}

// inputFields are the fields of an input type, with their names checked.
type inputFields struct {
	typ  string
	list []gqlField
	seen map[string]string // name → the element
}

func newInputFields(typ string) *inputFields {
	return &inputFields{typ: typ, seen: map[string]string{}}
}

// own adds a field of velox-graphql's own.
func (fs *inputFields) own(f gqlField) {
	fs.seen[f.name] = builtin
	fs.list = append(fs.list, f)
}

// add adds f, the field of the element f.where, unless another element
// already made a field of its name.
func (fs *inputFields) add(f gqlField) error {
	switch prev, ok := fs.seen[f.name]; {
	case !ok:
	case prev == builtin:
		return fmt.Errorf("%s: its field %s.%s would be one velox-graphql writes itself; rename it with graphql.FieldName", f.where, fs.typ, f.name)
	case prev == f.where:
		return fmt.Errorf("%s: it makes the field %s.%s twice", f.where, fs.typ, f.name)
	default:
		return fmt.Errorf("%s and %s both make the field %s.%s; rename one with graphql.FieldName", prev, f.where, fs.typ, f.name)
	}
	fs.seen[f.name] = f.where
	fs.list = append(fs.list, f)
	return nil
}

// input writes an input type.
func (w *writer) input(desc, name string, fields []gqlField) {
	w.line()
	w.desc("", desc)
	w.line("input ", name, " {")
	for _, f := range fields {
		w.field(f)
	}
	w.line("}")
}
