package gogen

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/contrib/graphql/internal/sdl"
	"github.com/syssam/velox/plugin"
)

// predMethod is the unexported method of a where input that is its Pred,
// untyped: the helpers that nest inputs take it as a method expression.
const predMethod = "predicate"

// predOps are, for each predicate, the field of gqlgraph.Ops that holds its
// value and the suffix of its name, as sdl names it ("titleNEQ"); whether it
// takes a Boolean, IsNil or NotNil, rather than a value of the field.
var predOps = map[graphql.WhereOp]struct {
	field, suffix string
	isBool        bool
}{
	graphql.OpEQ:           {"EQ", "", false},
	graphql.OpNEQ:          {"NEQ", "NEQ", false},
	graphql.OpIn:           {"In", "In", false},
	graphql.OpNotIn:        {"NotIn", "NotIn", false},
	graphql.OpGT:           {"GT", "GT", false},
	graphql.OpGTE:          {"GTE", "GTE", false},
	graphql.OpLT:           {"LT", "LT", false},
	graphql.OpLTE:          {"LTE", "LTE", false},
	graphql.OpContains:     {"Contains", "Contains", false},
	graphql.OpHasPrefix:    {"HasPrefix", "HasPrefix", false},
	graphql.OpHasSuffix:    {"HasSuffix", "HasSuffix", false},
	graphql.OpEqualFold:    {"EqualFold", "EqualFold", false},
	graphql.OpContainsFold: {"ContainsFold", "ContainsFold", false},
	graphql.OpIsNil:        {"IsNil", "IsNil", true},
	graphql.OpNotNil:       {"NotNil", "NotNil", true},
}

// writePred writes the where input's methods Pred, which turns it into a
// velox.Pred, and predicate, the same condition untyped: a gqlgraph.Filter
// on the entity's untyped.Table, and one call of a gqlgraph helper for each
// not, and, or and edge of the input and for each field's predicates
// together, which adds each predicate that is set. The helpers take columns
// and relations by index, and a nested input's predicate as a method
// expression, so that none is compiled once per entity.
func (r *renderer) writePred(f *file, t *sdl.Type) error {
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
	f.p("	f := %s.NewFilter(ctx, &%s)", impl, typeVar(t))
	f.p("	if w == nil {")
	f.p("		return f.Pred()")
	f.p("	}")
	ws := t.Where
	for i := 0; i < len(ws); {
		// A field's predicates are one run: sdl writes them together.
		j := i + 1
		for ws[i].Kind == sdl.WherePred && j < len(ws) && ws[j].Kind == sdl.WherePred && ws[j].Field == ws[i].Field && ws[j].Edge == ws[i].Edge {
			j++
		}
		call, err := r.predCall(f, t, ws[i:j])
		if err != nil {
			return err
		}
		f.p("	%s", call)
		i = j
	}
	f.p("	return f.Pred()")
	f.p("}")
	return nil
}

// predCall is the helper call that adds the predicates of ws, where-input
// fields of t: a not, an and, an or or an edge's, alone, or the predicates of
// one field, together.
func (r *renderer) predCall(f *file, t *sdl.Type, ws []sdl.WhereField) (string, error) {
	w := ws[0]
	e, impl := t.Entity, f.gqlgraph()
	field := fmt.Sprintf("%q, w.%s", w.Name, w.GoName) // the GraphQL name, for errors, and the value
	cond := func(of string) string { return "(*" + of + "WhereInput)." + predMethod }
	edge := func() string {
		ed := e.Edges[w.Edge]
		return fmt.Sprintf("%s, %s.TableOf[%s]()", edgeConst(e, ed), f.untyped(), ed.Target)
	}
	switch w.Kind {
	case sdl.WhereNot:
		return fmt.Sprintf("%s.Not(f, %s, %s)", impl, field, cond(t.Name)), nil
	case sdl.WhereAnd:
		return fmt.Sprintf("%s.And(f, %s, %s)", impl, field, cond(t.Name)), nil
	case sdl.WhereOr:
		return fmt.Sprintf("%s.Or(f, %s, %s)", impl, field, cond(t.Name)), nil
	case sdl.WhereHas:
		return fmt.Sprintf("%s.Has(f, %s, %s)", impl, field, edge()), nil
	case sdl.WhereHasWith:
		return fmt.Sprintf("%s.HasWith(f, %s, %s, %s)", impl, field, cond(w.Target), edge()), nil
	}
	// The field's predicates: the value of each in its field of gqlgraph.Ops,
	// and the field's GraphQL name, which the predicates' names begin with.
	fd := e.Fields[w.Field]
	var base string
	ops := make([]string, len(ws))
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
		ops[k] = pm.field + ": w." + x.GoName
	}
	col := colConst(e, fd)
	if w.IDs() {
		// IDs: of t's key, or of the foreign key fd, which holds the keys of
		// the edge's target. They are keys in the target's key Go type, which
		// gqlgen decoded; the helper converts them to fd's Go type when that
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
			return items(fmt.Sprintf("%s.EdgeIDField(f, %q, %s, %s, &%s, %s.Ops[%s]{", impl, base, col, edgeConst(e, e.Edges[w.Edge]), typeVar(tt), impl, k.typ), ops, "})"), nil
		}
		return items(fmt.Sprintf("%s.IDField(f, %q, %s, %s.Ops[%s]{", impl, base, col, impl, k.typ), ops, "})"), nil
	}
	if w.Edge >= 0 {
		return "", fmt.Errorf("schema %s, field %s: a foreign key's predicate that takes no IDs, which is a bug", e.Name, w.Name)
	}
	// The first predicate holds the field's Go type: sdl writes IsNil and
	// NotNil last, so a run that starts with one has no value.
	typ := "bool"
	if !predOps[w.Op].isBool {
		if err := r.whereConv(e, fd, w); err != nil {
			return "", err
		}
		var err error
		if typ, err = r.qualify(f, e, fd, w.GoType, w.GoPkg); err != nil {
			return "", err
		}
	}
	return items(fmt.Sprintf("%s.Field(f, %q, %s, %s.Ops[%s]{", impl, base, col, impl, typ), ops, "})"), nil
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
		if w.GoName == "Pred" {
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
		f.p("\t%s %s", w.GoName, typ)
	}
	f.p("}")
	return r.writePred(f, t)
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

// quoteList is vs as Go string literals, separated by commas.
func quoteList(vs []string) string {
	q := make([]string, len(vs))
	for i, v := range vs {
		q[i] = strconv.Quote(v)
	}
	return strings.Join(q, ", ")
}

// orList is vs in English, as "A, B or C".
func orList(vs []string) string {
	if len(vs) < 2 {
		return strings.Join(vs, "")
	}
	return strings.Join(vs[:len(vs)-1], ", ") + " or " + vs[len(vs)-1]
}
