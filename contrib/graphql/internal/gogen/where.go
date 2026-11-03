package gogen

import (
	"cmp"
	"fmt"

	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/contrib/graphql/internal/sdl"
	"github.com/syssam/velox/plugin"
)

// The kinds of value a predicate's helper takes.
const (
	takesValue = iota // one value: gqlimpl.Value
	takesList         // a list: gqlimpl.List
	takesBool         // a Boolean: gqlimpl.Is
)

// predOps are velox's operator of each predicate, and the kind of value it
// takes.
var predOps = map[graphql.WhereOp]struct {
	op    string
	takes int
}{
	graphql.OpEQ:        {"PredEQ", takesValue},
	graphql.OpNEQ:       {"PredNEQ", takesValue},
	graphql.OpIn:        {"PredIn", takesList},
	graphql.OpNotIn:     {"PredNotIn", takesList},
	graphql.OpGT:        {"PredGT", takesValue},
	graphql.OpGTE:       {"PredGTE", takesValue},
	graphql.OpLT:        {"PredLT", takesValue},
	graphql.OpLTE:       {"PredLTE", takesValue},
	graphql.OpContains:  {"PredContains", takesValue},
	graphql.OpHasPrefix: {"PredHasPrefix", takesValue},
	graphql.OpHasSuffix: {"PredHasSuffix", takesValue},
	graphql.OpIsNil:     {"PredIsNull", takesBool},
	graphql.OpNotNil:    {"PredNotNull", takesBool},
}

// writePred writes the where input's methods Pred, which turns it into a
// velox.Pred, and PredicateGQL, the same condition untyped: a
// gqlimpl.Filter on the entity's velox.Table, and one call of a gqlimpl
// helper for each field of the input, which adds the field's predicate when
// it is set. The helpers take columns and relations by index, so that none
// is compiled once per entity.
func (r *renderer) writePred(f *file, t *sdl.Type) error {
	n, e := t.Name, t.Entity
	in := n + "WhereInput"
	ctx, velox, impl := f.use("context", "context"), f.velox(), f.gqlimpl()
	f.p("")
	f.doc("", fmt.Sprintf("Pred is the condition w stands for, on %s nodes: every predicate w sets holds, and a w that sets none, or a nil w, holds for every node. It fails, before any query runs, on an ID of another type or a malformed one, and on a filter over the limits of veloxgql.LimitsFromContext(ctx): the errors are gqlimpl.NewFilter's.", n))
	f.p("func (w *%s) Pred(ctx %s.Context) (%s.Pred[%s], error) {", in, ctx, velox, e.Name)
	f.p("	p, err := w.PredicateGQL(ctx)")
	f.p("	return %s.Pred[%s](p), err", velox, e.Name)
	f.p("}")
	f.p("")
	f.doc("", "PredicateGQL is Pred, untyped, for velox-graphql's runtime.")
	f.p("func (w *%s) PredicateGQL(ctx %s.Context) (%s.Predicate, error) {", in, ctx, velox)
	f.p("	f := %s.NewFilter(ctx, %q, %s.TableOf[%s]())", impl, in, velox, e.Name)
	f.p("	if w == nil {")
	f.p("		return f.Pred()")
	f.p("	}")
	for _, w := range t.Where {
		call, err := r.predCall(f, t, w)
		if err != nil {
			return err
		}
		f.p("	%s", call)
	}
	f.p("	return f.Pred()")
	f.p("}")
	return nil
}

// predCall is the helper call that adds the predicate of the where-input
// field w of t.
func (r *renderer) predCall(f *file, t *sdl.Type, w sdl.WhereField) (string, error) {
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
	pm, ok := predOps[w.Op]
	if !ok {
		return "", fmt.Errorf("velox-graphql has no predicate WhereOp(%#x), which is a bug: schema %s, field %s", uint32(w.Op), e.Name, w.Name)
	}
	fd := e.Fields[w.Field]
	col := fmt.Sprintf("%s, %s.%s", colConst(e, fd), velox, pm.op)
	fk := ""
	if w.Edge >= 0 {
		// A foreign key: its target's Read policy holds of it, as of the edge.
		fk = ", " + edge()
	}
	if pm.takes == takesBool {
		if fk != "" {
			return fmt.Sprintf("%s.FKIs(f, %s, %s%s)", impl, field, col, fk), nil
		}
		return fmt.Sprintf("%s.Is(f, %s, %s)", impl, field, col), nil
	}
	if w.IDs() {
		return r.idCall(f, t, w, fd, field, col, fk, pm.takes)
	}
	if fk != "" {
		return "", fmt.Errorf("schema %s, field %s: a foreign key's predicate that takes no IDs, which is a bug", e.Name, w.Name)
	}
	if err := r.whereConv(e, fd, w); err != nil {
		return "", err
	}
	helper := "Value"
	if pm.takes == takesList {
		helper = "List"
	}
	return fmt.Sprintf("%s.%s(f, %s, %s)", impl, helper, field, col), nil
}

// idCall is the helper call of a predicate on IDs: of t's key, or of the
// foreign key fd, which holds the keys of the edge's target (fk, the edge's
// arguments, is then not ""). Each ID is decoded with veloxgql.DecodeID as
// the target's Key, which the helper converts to fd's Go type when that is
// not the Key.
func (r *renderer) idCall(f *file, t *sdl.Type, w sdl.WhereField, fd *plugin.Field, field, col, fk string, takes int) (string, error) {
	e, impl := t.Entity, f.gqlimpl()
	tt := r.types[w.Target]
	if tt == nil || !tt.Node {
		return "", fmt.Errorf("schema %s, field %s: its predicates take IDs of %s, which is not a Node, which is a bug", e.Name, w.Name, w.Target)
	}
	helper := "ID"
	if takes == takesList {
		helper = "IDList"
	}
	if fk != "" {
		helper = "FK" + helper
	}
	if elem := cmp.Or(fd.ElemType, tt.Key); elem != tt.Key {
		if err := r.converts(e, fd, tt.Key); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("%s.%s[%s](f, %s, %q, %s%s)", impl, helper, tt.Key, field, w.Target, col, fk), nil
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
