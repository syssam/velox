package sdl

import (
	"fmt"
	"strings"

	"github.com/syssam/velox2/contrib/graphql"
	"github.com/syssam/velox2/plugin"
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
// those WhereOps() gives it (defaults). typ is "ID" for a key, else the
// schema field type. A nullable field also has OpsNullable.
func predicatesOf(typ string, nullable bool) (has, defaults graphql.WhereOp) {
	switch typ {
	case "string":
		has, defaults = graphql.OpsEquality|graphql.OpsComparison|graphql.OpsString, graphql.OpsEquality|graphql.OpsString
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64", "time":
		has, defaults = graphql.OpsEquality|graphql.OpsComparison, graphql.OpsEquality|graphql.OpsComparison
	case "bool":
		has, defaults = graphql.OpEQ|graphql.OpNEQ, graphql.OpEQ|graphql.OpNEQ
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
		kind := "a required"
		if nullable {
			kind = "an optional"
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
	for _, n := range []string{"not: " + in, "and: [" + in + "!]", "or: [" + in + "!]"} {
		name, typ, _ := strings.Cut(n, ": ")
		fs.own(gqlField{name: name, typ: typ})
	}
	for i, f := range e.Fields {
		a := o.fanns[i]
		if a.WhereOps == 0 || a.Skip&graphql.SkipWhereInput != 0 {
			continue
		}
		where := "schema " + e.Name + ", field " + f.Name
		var name, typ, kind string
		switch {
		case o.node && i == e.PrimaryKey[0]:
			name, typ, kind = "id", "ID", "ID"
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
			name, typ, kind = en+"ID", "ID", "ID"
		default:
			n, err := o.fieldName(i)
			if err != nil {
				return err
			}
			t, err := g.fieldType(o, f, a, where)
			if err != nil {
				return err
			}
			name, typ, kind = n, t, f.Type
		}
		ops, err := resolveOps(a.WhereOps, kind, f.Nullable, where)
		if err != nil {
			return err
		}
		for _, p := range predicates {
			if ops&p.op == 0 {
				continue
			}
			pf := gqlField{name: name + p.suffix, typ: typ, where: where}
			switch p.takes {
			case takesList:
				pf.typ = "[" + typ + "!]"
			case takesBool:
				pf.typ = "Boolean"
			}
			if err := fs.add(pf); err != nil {
				return err
			}
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
		if err := fs.add(gqlField{name: "has" + upper(name), typ: "Boolean", where: where}); err != nil {
			return err
		}
		// hasWith takes the target's where input; a target without one
		// offers has alone.
		if t.hasWhere {
			if err := fs.add(gqlField{name: "has" + upper(name) + "With", typ: "[" + t.name + "WhereInput!]", where: where}); err != nil {
				return err
			}
		}
	}
	o.where = fs.list
	return nil
}

// keyEdge is the index in o.ent.Edges of the unique edge that f, a foreign
// key of o, stands for, and that GraphQL can name: -1 for none.
func (g *graph) keyEdge(o *object, f *plugin.Field) int {
	for i, ed := range o.ent.Edges {
		t := g.byName[ed.Target]
		if ed.Unique && ed.Field == f.Name && ed.FieldOn == o.ent.Name && t != nil && t.node && o.eanns[i].Skip&graphql.SkipWhereInput == 0 {
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
