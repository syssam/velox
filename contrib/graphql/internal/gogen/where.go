package gogen

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/contrib/graphql/internal/sdl"
	"github.com/syssam/velox/plugin"
)

// predMethod is the method of a where input that is its Pred, untyped, and
// that gqlgraph.Predicate calls of a nested input.
const predMethod = "Predicate"

// predOps are, for each predicate, its bit in gqlgraph.WhereOp and the
// suffix of its name, as sdl names it ("titleNEQ"); whether it takes a
// Boolean, IsEmpty, IsNil or NotNil, rather than a value of the field.
var predOps = map[graphql.WhereOp]struct {
	name, suffix string
	isBool       bool
}{
	graphql.OpEQ:           {"OpEQ", "", false},
	graphql.OpNEQ:          {"OpNEQ", "NEQ", false},
	graphql.OpIn:           {"OpIn", "In", false},
	graphql.OpNotIn:        {"OpNotIn", "NotIn", false},
	graphql.OpGT:           {"OpGT", "GT", false},
	graphql.OpGTE:          {"OpGTE", "GTE", false},
	graphql.OpLT:           {"OpLT", "LT", false},
	graphql.OpLTE:          {"OpLTE", "LTE", false},
	graphql.OpContains:     {"OpContains", "Contains", false},
	graphql.OpHasPrefix:    {"OpHasPrefix", "HasPrefix", false},
	graphql.OpHasSuffix:    {"OpHasSuffix", "HasSuffix", false},
	graphql.OpEqualFold:    {"OpEqualFold", "EqualFold", false},
	graphql.OpContainsFold: {"OpContainsFold", "ContainsFold", false},
	graphql.OpHas:          {"OpHas", "Has", false},
	graphql.OpHasSome:      {"OpHasSome", "HasSome", false},
	graphql.OpHasEvery:     {"OpHasEvery", "HasEvery", false},
	graphql.OpIsEmpty:      {"OpIsEmpty", "IsEmpty", true},
	graphql.OpIsNil:        {"OpIsNil", "IsNil", true},
	graphql.OpNotNil:       {"OpNotNil", "NotNil", true},
}

// opSets are gqlgraph's names of sets of predicates, which opsExpr uses
// before the predicates' own.
var opSets = []struct {
	name string
	ops  graphql.WhereOp
}{
	{"OpsEquality", graphql.OpEQ | graphql.OpNEQ | graphql.OpIn | graphql.OpNotIn},
	{"OpsOrder", graphql.OpGT | graphql.OpGTE | graphql.OpLT | graphql.OpLTE},
	{"OpsText", graphql.OpContains | graphql.OpHasPrefix | graphql.OpHasSuffix | graphql.OpEqualFold | graphql.OpContainsFold},
	{"OpsNull", graphql.OpIsNil | graphql.OpNotNil},
}

// opsExpr is ops as a gqlgraph.WhereOp expression: its sets, then its
// other predicates, by name.
func opsExpr(impl string, ops graphql.WhereOp) string {
	var parts []string
	for _, set := range opSets {
		if ops&set.ops == set.ops {
			parts = append(parts, impl+"."+set.name)
			ops &^= set.ops
		}
	}
	for op := graphql.WhereOp(1); op != 0 && ops != 0; op <<= 1 {
		if ops&op != 0 {
			parts = append(parts, impl+"."+predOps[op].name)
			ops &^= op
		}
	}
	return strings.Join(parts, " | ")
}

// writePred writes the where input's methods Pred, which turns it into a
// velox.Pred, and Predicate, the same condition untyped: gqlgraph.Predicate
// of the input, which reads its fields as the type's WhereFields
// (writeWhereFields) describe them.
func (r *renderer) writePred(f *file, t *sdl.Type) {
	n, e := t.Name, t.Entity
	in := n + "WhereInput"
	ctx, velox, impl := f.use("context", "context"), f.velox(), f.gqlgraph()
	f.p("")
	f.doc("", fmt.Sprintf("Pred is the condition w stands for on %s nodes, true for a nil or empty w. It fails on a filter over the limits of veloxgql.LimitsFromContext(ctx).", n))
	f.p("func (w *%s) Pred(ctx %s.Context) (%s.Pred[%s], error) {", in, ctx, velox, e.Name)
	f.p("	p, err := w.%s(ctx)", predMethod)
	f.p("	return %s.Pred[%s](p), err", velox, e.Name)
	f.p("}")
	f.p("")
	f.doc("", predMethod+" is Pred, untyped.")
	f.p("func (w *%s) %s(ctx %s.Context) (%s.Predicate, error) {", in, predMethod, ctx, velox)
	f.p("	return %s.Predicate(ctx, &%s, w)", impl, typeVar(t))
	f.p("}")
}

// writeWhereFields writes the WhereFields of t's gqlgraph.Type: one entry
// for each not, and, or and edge field of the where input, and one for the
// run of fields of each column's predicates, which sdl writes together.
func (r *renderer) writeWhereFields(f *file, t *sdl.Type) error {
	ws := t.Where
	f.p("\tWhereFields: []%s.WhereField{", f.gqlgraph())
	for i := 0; i < len(ws); {
		j := i + 1
		for ws[i].Kind == sdl.WherePred && j < len(ws) && ws[j].Kind == sdl.WherePred && ws[j].Field == ws[i].Field && ws[j].Edge == ws[i].Edge {
			j++
		}
		d, err := r.whereField(f, t, ws[i:j])
		if err != nil {
			return err
		}
		f.p("\t\t{%s},", d)
		i = j
	}
	f.p("\t},")
	return nil
}

// whereField is the gqlgraph.WhereField of ws, where-input fields of t: a
// not, an and, an or or an edge's, alone, or the predicates of one field,
// together.
func (r *renderer) whereField(f *file, t *sdl.Type, ws []sdl.WhereField) (string, error) {
	w := ws[0]
	e, impl := t.Entity, f.gqlgraph()
	edge := func() string {
		ed := e.Edges[w.Edge]
		return fmt.Sprintf("Edge: %s, Target: %s.TableOf[%s]", edgeConst(e, ed), f.untyped(), ed.Target)
	}
	kind := func(k string) string { return fmt.Sprintf("Name: %q, Kind: %s.Where%s", w.Name, impl, k) }
	switch w.Kind {
	case sdl.WhereNot:
		return kind("Not"), nil
	case sdl.WhereAnd:
		return kind("And"), nil
	case sdl.WhereOr:
		return kind("Or"), nil
	case sdl.WhereHas:
		return kind("Has") + ", " + edge(), nil
	case sdl.WhereHasWith:
		return kind("HasWith") + ", " + edge(), nil
	}
	// The field's predicates: their set, in the order of their bits, which
	// gqlgraph reads the run's fields in, and the field's GraphQL name,
	// which the predicates' names begin with.
	fd := e.Fields[w.Field]
	var base string
	var set graphql.WhereOp
	for k, x := range ws {
		pm, ok := predOps[x.Op]
		if !ok {
			return "", fmt.Errorf("velox-graphql has no predicate WhereOp(%#x), which is a bug: schema %s, field %s", uint32(x.Op), e.Name, x.Name)
		}
		if k == 0 {
			base = strings.TrimSuffix(x.Name, pm.suffix)
		}
		if x.Name != base+pm.suffix {
			return "", fmt.Errorf("schema %s: the where field %s is not %s%s, which is a bug", e.Name, x.Name, base, pm.suffix)
		}
		if x.Op&^(x.Op-1) != x.Op || set&x.Op != 0 || k > 0 && !fieldBefore(ws[k-1].Op, x.Op) {
			return "", fmt.Errorf("schema %s: the where fields of %s are not in gqlgraph's order, which is a bug", e.Name, base)
		}
		set |= x.Op
	}
	col := colConst(e, fd)
	ops := opsExpr(impl, set)
	if w.IDs() {
		// IDs: of t's key, or of the foreign key fd, which holds the keys of
		// the edge's target. They are keys in the target's key Go type, which
		// gqlgen decoded; gqlgraph converts them to fd's Go type when that
		// differs.
		tt := r.types[w.Target]
		k, err := r.idKeyOf(f, e, tt)
		if err != nil {
			return "", fmt.Errorf("schema %s, field %s: its predicates take IDs of %s: %w", e.Name, w.Name, w.Target, err)
		}
		if !sameGoType(fd, k.fd) {
			if err := r.converts(e, fd, tt.Key); err != nil {
				return "", err
			}
		}
		if w.Edge >= 0 {
			// A foreign key: its target's Read policy holds of it, as of the edge.
			ed := e.Edges[w.Edge]
			return fmt.Sprintf("Name: %q, Kind: %s.WhereEdgeID, Ops: %s, Col: %s, Edge: %s, Target: %s.TableOf[%s]", base, impl, ops, col, edgeConst(e, ed), f.untyped(), tt.Entity.Name), nil
		}
		return fmt.Sprintf("Name: %q, Kind: %s.WhereID, Ops: %s, Col: %s", base, impl, ops, col), nil
	}
	if w.Edge >= 0 {
		return "", fmt.Errorf("schema %s, field %s: a foreign key's predicate that takes no IDs, which is a bug", e.Name, w.Name)
	}
	if fd.Type == "json" { // only an array has predicates: on its elements, which need no conversion
		return fmt.Sprintf("Name: %q, Kind: %s.WhereArray, Ops: %s, Col: %s", base, impl, ops, col), nil
	}
	if !predOps[w.Op].isBool {
		if err := r.whereConv(e, fd, w); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("Name: %q, Ops: %s, Col: %s", base, ops, col), nil
}

// fieldBefore reports whether sdl writes the field of predicate a before
// that of b: the order gqlgraph reads a run's fields in.
func fieldBefore(a, b graphql.WhereOp) bool { return fieldRank(a) < fieldRank(b) }

// fieldRank is op's place in gqlgraph's order of a run's fields, which puts
// IsNil and NotNil last.
func fieldRank(op graphql.WhereOp) int {
	switch op {
	case graphql.OpIsNil:
		return 1 << 20
	case graphql.OpNotNil:
		return 1<<20 + 1
	}
	return int(op)
}

// whereConv checks that the where-input field w's values convert to the Go
// type of fd, its column, as untyped.Table.Convert converts them: w holds
// fd's own Go type, as when the field binds to its scalar as it is, or the
// base type the scalar binds to of a field of the application's own Go type.
func (r *renderer) whereConv(e *plugin.Entity, fd *plugin.Field, w sdl.WhereField) error {
	// A model that gives no ElemType (one written by hand) means velox's
	// own, and no PkgPath a package the type's qualifier names.
	if fd.ElemType == "" || fd.ElemType == w.GoType && (fd.PkgPath == w.GoPkg || fd.PkgPath == "") {
		return nil
	}
	var base string
	switch fd.Type {
	case "string", "enum":
		base = "string"
	case "bool":
		base = "bool"
	case "float32", "float64", "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64":
		base = fd.Type
	case "uuid":
		base = "[16]byte"
	case "time":
		// A time field of another Go type: converts refuses it when the
		// model gives its Kind, and this when it does not (a model written
		// by hand).
		if err := r.converts(e, fd, "time.Time"); err != nil {
			return err
		}
		return fmt.Errorf("schema %s, field %s: velox-graphql converts a time field only from time.Time, and its Go type is %s; give the field time.Time", e.Name, fd.Name, fd.ElemType)
	default:
		return fmt.Errorf("schema %s, field %s: velox-graphql cannot convert a where predicate's %s to its Go type %s", e.Name, fd.Name, w.GoType, fd.ElemType)
	}
	return r.converts(e, fd, base)
}

func (r *renderer) writeWhere(f *file, t *sdl.Type) error {
	n, e := t.Name, t.Entity
	in := n + "WhereInput"
	if err := r.declare(in, "schema "+e.Name); err != nil {
		return err
	}
	f.p("")
	f.doc("", fmt.Sprintf("%s is the GraphQL input %[1]s, a filter of %s nodes: a node matches when every predicate given holds.", in, n))
	f.p("type %s struct {", in)
	seen := map[string]string{} // as gqlgen matches names → the Go name
	group := ""
	for _, w := range t.Where {
		key := strings.ToLower(strings.ReplaceAll(w.GoName, "_", ""))
		if prev, ok := seen[key]; ok {
			return fmt.Errorf("schema %s: the fields %s and %s of %s are one Go field to gqlgen, which ignores case and underscores; rename one with graphql.FieldName", e.Name, prev, w.GoName, in)
		}
		seen[key] = w.GoName
		if w.GoName == "Pred" || w.GoName == predMethod {
			return fmt.Errorf("schema %s: the field %s of %s is the Go field %s, which is the input's method; rename it with graphql.FieldName", e.Name, w.Name, in, w.GoName)
		}
		typ, err := r.whereType(f, t, w)
		if err != nil {
			return err
		}
		var g string
		switch w.Kind {
		case sdl.WhereNot, sdl.WhereAnd, sdl.WhereOr:
			g = "logic"
		case sdl.WhereHas, sdl.WhereHasWith:
			g = "edge " + strconv.Itoa(w.Edge)
		default:
			g = "field " + strconv.Itoa(w.Field)
		}
		if g != group {
			if group != "" {
				f.p("")
			}
			group = g
			switch {
			case g == "logic":
				f.doc("\t", "Not, And and Or combine filters: a node matches when it does not match Not, matches each of And, and matches one of Or.")
			case w.Kind == sdl.WhereHas || w.Kind == sdl.WhereHasWith:
				f.doc("\t", fmt.Sprintf("The edge %s: whether it has a node, or one that matches.", named(e.Edges[w.Edge].Name, w.On)))
			case w.Edge >= 0:
				f.doc("\t", fmt.Sprintf("Predicates on the edge %s, by the ID of its node.", named(e.Edges[w.Edge].Name, w.On)))
			case w.IDs():
				f.p("\t// Predicates on the ID.")
			default:
				f.doc("\t", fmt.Sprintf("Predicates on the field %s.", named(e.Fields[w.Field].Name, w.On)))
			}
		}
		f.p("\t%s %s%s", w.GoName, typ, r.graphqlTag(w.GoName, w.Name))
	}
	f.p("}")
	r.writePred(f, t)
	return nil
}

// whereType is the Go type of the where-input field w: an input for not and
// has<Edge>With, else w.GoType, a pointer for one value and a slice for a
// list.
func (r *renderer) whereType(f *file, t *sdl.Type, w sdl.WhereField) (string, error) {
	switch w.Kind {
	case sdl.WhereNot:
		return "*" + t.Name + "WhereInput", nil
	case sdl.WhereAnd, sdl.WhereOr:
		return "[]*" + t.Name + "WhereInput", nil
	case sdl.WhereHasWith:
		return "[]*" + w.Target + "WhereInput", nil
	}
	var fd *plugin.Field
	if w.Field >= 0 {
		fd = t.Entity.Fields[w.Field]
	} else {
		fd = &plugin.Field{Name: w.On}
	}
	elem, err := r.qualify(f, t.Entity, fd, w.GoType, w.GoPkg)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(w.Type, "[") {
		return "[]" + elem, nil
	}
	return "*" + elem, nil
}

// orList is vs in English, as "A, B or C".
func orList(vs []string) string {
	if len(vs) < 2 {
		return strings.Join(vs, "")
	}
	return strings.Join(vs[:len(vs)-1], ", ") + " or " + vs[len(vs)-1]
}
