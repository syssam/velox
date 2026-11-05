package gogen

import (
	"cmp"
	"fmt"
	"strings"

	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/contrib/graphql/internal/sdl"
	"github.com/syssam/velox/plugin"
)

// predOps are, for each predicate, the field of gqlimpl.Ops that holds its
// value and the suffix of its name, as sdl names it ("titleNEQ"); whether it
// takes a Boolean, IsNil or NotNil, rather than a value of the field.
var predOps = map[graphql.WhereOp]struct {
	field, suffix string
	isBool        bool
}{
	graphql.OpEQ:        {"EQ", "", false},
	graphql.OpNEQ:       {"NEQ", "NEQ", false},
	graphql.OpIn:        {"In", "In", false},
	graphql.OpNotIn:     {"NotIn", "NotIn", false},
	graphql.OpGT:        {"GT", "GT", false},
	graphql.OpGTE:       {"GTE", "GTE", false},
	graphql.OpLT:        {"LT", "LT", false},
	graphql.OpLTE:       {"LTE", "LTE", false},
	graphql.OpContains:  {"Contains", "Contains", false},
	graphql.OpHasPrefix: {"HasPrefix", "HasPrefix", false},
	graphql.OpHasSuffix: {"HasSuffix", "HasSuffix", false},
	graphql.OpIsNil:     {"IsNil", "IsNil", true},
	graphql.OpNotNil:    {"NotNil", "NotNil", true},
}

// writePred writes the where input's methods Pred, which turns it into a
// velox.Pred, and PredicateGQL, the same condition untyped: a
// gqlimpl.Filter on the entity's velox.Table, and one call of a gqlimpl
// helper for each not, and, or and edge of the input and for each field's
// predicates together, which adds each predicate that is set. The helpers
// take columns and relations by index, so that none is compiled once per
// entity.
func (r *renderer) writePred(f *file, t *sdl.Type) error {
	n, e := t.Name, t.Entity
	in := n + "WhereInput"
	ctx, velox, impl := f.use("context", "context"), f.velox(), f.gqlimpl()
	f.p("")
	f.doc("", fmt.Sprintf("Pred is the condition w stands for on %s nodes, true for a nil or empty w. Its errors are gqlimpl.NewFilter's.", n))
	f.p("func (w *%s) Pred(ctx %s.Context) (%s.Pred[%s], error) {", in, ctx, velox, e.Name)
	f.p("	p, err := w.PredicateGQL(ctx)")
	f.p("	return %s.Pred[%s](p), err", velox, e.Name)
	f.p("}")
	f.p("")
	f.doc("", "PredicateGQL is Pred, untyped, for velox-graphql's runtime.")
	f.p("func (w *%s) PredicateGQL(ctx %s.Context) (%s.Predicate, error) {", in, ctx, velox)
	f.p("	f := %s.NewFilter(ctx, &%s)", impl, connVar(t))
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
	e, impl, velox := t.Entity, f.gqlimpl(), f.velox()
	field := fmt.Sprintf("%q, w.%s", w.Name, w.GoName) // the GraphQL name, for errors, and the value
	edge := func() string {
		ed := e.Edges[w.Edge]
		return fmt.Sprintf("%s, %s.TableOf[%s]()", edgeConst(e, ed), velox, ed.Target)
	}
	switch w.Kind {
	case sdl.WhereNot:
		return fmt.Sprintf("%s.Not(f, %s)", impl, field), nil
	case sdl.WhereAnd:
		return fmt.Sprintf("%s.And(f, %s)", impl, field), nil
	case sdl.WhereOr:
		return fmt.Sprintf("%s.Or(f, %s)", impl, field), nil
	case sdl.WhereHas:
		return fmt.Sprintf("%s.Has(f, %s, %s)", impl, field, edge()), nil
	case sdl.WhereHasWith:
		return fmt.Sprintf("%s.HasWith(f, %s, %s)", impl, field, edge()), nil
	}
	// The field's predicates: the value of each in its field of gqlimpl.Ops,
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
	set := strings.Join(ops, ", ")
	col := colConst(e, fd)
	if w.IDs() {
		// IDs: of t's key, or of the foreign key fd, which holds the keys of
		// the edge's target. Each ID is decoded with veloxgql.DecodeID as the
		// target's Key, which the helper converts to fd's Go type when that
		// is not the Key.
		tt := r.types[w.Target]
		if tt == nil || !tt.Node {
			return "", fmt.Errorf("schema %s, field %s: its predicates take IDs of %s, which is not a Node, which is a bug", e.Name, w.Name, w.Target)
		}
		if elem := cmp.Or(fd.ElemType, tt.Key); elem != tt.Key {
			if err := r.converts(e, fd, tt.Key); err != nil {
				return "", err
			}
		}
		if w.Edge >= 0 {
			// A foreign key: its target's Read policy holds of it, as of the edge.
			return fmt.Sprintf("%s.FKField[%s](f, %q, %s, %s, &%s, %s.Ops[string]{%s})", impl, tt.Key, base, col, edgeConst(e, e.Edges[w.Edge]), connVar(tt), impl, set), nil
		}
		return fmt.Sprintf("%s.IDField[%s](f, %q, %s, %s.Ops[string]{%s})", impl, tt.Key, base, col, impl, set), nil
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
	return fmt.Sprintf("%s.Field(f, %q, %s, %s.Ops[%s]{%s})", impl, base, col, impl, typ, set), nil
}

// whereConv checks that the where-input field w's values convert to the Go
// type of fd, its column, as velox.Table.Convert converts them: w holds
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
