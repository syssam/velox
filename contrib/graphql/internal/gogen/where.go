package gogen

import (
	"cmp"
	"fmt"

	"github.com/syssam/velox2/contrib/graphql"
	"github.com/syssam/velox2/contrib/graphql/internal/sdl"
	"github.com/syssam/velox2/plugin"
)

// The kinds of value a predicate's helper takes.
const (
	takesValue = iota // one value: gqlimpl.Value
	takesList         // a list: gqlimpl.List
	takesBool         // a Boolean: gqlimpl.Is
)

// predMethods are the column handle's method of each predicate, and the
// kind of value it takes.
var predMethods = map[graphql.WhereOp]struct {
	method string
	takes  int
}{
	graphql.OpEQ:        {"EQ", takesValue},
	graphql.OpNEQ:       {"NEQ", takesValue},
	graphql.OpIn:        {"In", takesList},
	graphql.OpNotIn:     {"NotIn", takesList},
	graphql.OpGT:        {"GT", takesValue},
	graphql.OpGTE:       {"GTE", takesValue},
	graphql.OpLT:        {"LT", takesValue},
	graphql.OpLTE:       {"LTE", takesValue},
	graphql.OpContains:  {"Contains", takesValue},
	graphql.OpHasPrefix: {"HasPrefix", takesValue},
	graphql.OpHasSuffix: {"HasSuffix", takesValue},
	graphql.OpIsNil:     {"IsNull", takesBool},
	graphql.OpNotNil:    {"NotNull", takesBool},
}

// writePred writes the where input's method Pred, which turns it into a
// velox.Pred: a gqlimpl.Filter, and one call of a gqlimpl helper for each
// field of the input, which adds the field's predicate when it is set.
func (r *renderer) writePred(f *file, t *sdl.Type) error {
	n, e := t.Name, t.Entity
	in := n + "WhereInput"
	ctx, velox, impl := f.use("context", "context"), f.velox(), f.gqlimpl()
	f.p("")
	f.doc("", fmt.Sprintf("Pred is the condition w stands for, on %s nodes: every predicate w sets holds, and a w that sets none, or a nil w, holds for every node. depth is how deeply w is nested in the where argument, 0 at the top. It fails, before any query runs, on an ID of another type (veloxgql.ErrWrongIDType) or a malformed one (veloxgql.ErrBadID), on a nesting deeper than the MaxFilterDepth of veloxgql.LimitsFrom(ctx) (veloxgql.ErrFilterTooDeep), on an and, an or, a has…With, an In or a NotIn of more than its MaxFilterWidth entries (veloxgql.ErrFilterTooWide), and on a where argument of more than its MaxFilterNodes inputs, predicates and values in all (veloxgql.ErrFilterTooLarge).", n))
	f.p("func (w *%s) Pred(ctx %s.Context, depth int) (%s.Pred[%s], error) {", in, ctx, velox, e.Name)
	f.p("\tf := %s.NewFilter[%s](ctx, %q, depth)", impl, e.Name, in)
	f.p("\tif w == nil {")
	f.p("\t\treturn f.Pred()")
	f.p("\t}")
	for _, w := range t.Where {
		call, err := r.predCall(f, t, w)
		if err != nil {
			return err
		}
		f.p("\t%s", call)
	}
	f.p("\treturn f.Pred()")
	f.p("}")
	return nil
}

// predCall is the helper call that adds the predicate of the where-input
// field w of t.
func (r *renderer) predCall(f *file, t *sdl.Type, w sdl.WhereField) (string, error) {
	e, impl := t.Entity, f.gqlimpl()
	field := fmt.Sprintf("%q, w.%s", w.Name, w.GoName) // the GraphQL name, for errors, and the value
	switch w.Kind {
	case sdl.WhereNot:
		return fmt.Sprintf("%s.Not(f, %s)", impl, field), nil
	case sdl.WhereAnd:
		return fmt.Sprintf("%s.And(f, %s)", impl, field), nil
	case sdl.WhereOr:
		return fmt.Sprintf("%s.Or(f, %s)", impl, field), nil
	case sdl.WhereHas:
		return fmt.Sprintf("%s.Is(f, %s, %s.%s.Has)", impl, field, e.Handles, e.Edges[w.Edge].GoName), nil
	case sdl.WhereHasWith:
		return fmt.Sprintf("%s.HasWith(f, %s, %s.%s.HasWhere)", impl, field, e.Handles, e.Edges[w.Edge].GoName), nil
	}
	pm, ok := predMethods[w.Op]
	if !ok {
		return "", fmt.Errorf("velox-graphql has no predicate WhereOp(%#x), which is a bug: schema %s, field %s", uint32(w.Op), e.Name, w.Name)
	}
	fd := e.Fields[w.Field]
	op := e.Handles + "." + fd.GoName + "." + pm.method
	if pm.takes == takesBool {
		return fmt.Sprintf("%s.Is(f, %s, %s)", impl, field, op), nil
	}
	if w.IDs() {
		return r.idCall(f, t, w, fd, field, op, pm.takes)
	}
	conv, err := r.whereConv(f, e, fd, w)
	if err != nil {
		return "", err
	}
	helper := "Value"
	if pm.takes == takesList {
		helper = "List"
	}
	if conv == "" {
		return fmt.Sprintf("%s.%s(f, %s, %s)", impl, helper, field, op), nil
	}
	return fmt.Sprintf("%s.%sAs(f, %s, %s, %s)", impl, helper, field, conv, op), nil
}

// idCall is the helper call of a predicate on IDs: of t's key, or of the
// foreign key fd, which holds the keys of the edge's target. Each ID is
// decoded with veloxgql.DecodeID as the target's Key and converted to fd's
// Go type when that is not the Key.
func (r *renderer) idCall(f *file, t *sdl.Type, w sdl.WhereField, fd *plugin.Field, field, op string, takes int) (string, error) {
	e, impl := t.Entity, f.gqlimpl()
	tt := r.types[w.Target]
	if tt == nil || !tt.Node {
		return "", fmt.Errorf("schema %s, field %s: its predicates take IDs of %s, which is not a Node, which is a bug", e.Name, w.Name, w.Target)
	}
	helper := "ID"
	if takes == takesList {
		helper = "IDList"
	}
	elem := cmp.Or(fd.ElemType, tt.Key)
	if elem == tt.Key {
		return fmt.Sprintf("%s.%s(f, %s, %q, %s)", impl, helper, field, w.Target, op), nil
	}
	if err := r.converts(e, fd, tt.Key); err != nil {
		return "", err
	}
	col, err := r.qualify(f, e, fd, elem, fd.PkgPath)
	if err != nil {
		return "", err
	}
	var as string
	switch tt.Key {
	case "string":
		as = "AsString"
	case "[16]byte":
		as = "AsUUID"
	default:
		as = "AsInt"
	}
	return fmt.Sprintf("%s.%sAs(f, %s, %q, %s.%s[%s, %s], %s)", impl, helper, field, w.Target, impl, as, col, tt.Key, op), nil
}

// whereConv is the conversion of the where-input field w's values to the Go
// type of fd, its column, as a Go expression: "" when w holds fd's own Go
// type, as when the field binds to its scalar as it is; else an instance of
// a gqlimpl conversion, as gqlimpl.AsString[ids.Email, string], for a
// field of the application's own Go type, whose values the input holds as
// the base type the scalar binds to.
func (r *renderer) whereConv(f *file, e *plugin.Entity, fd *plugin.Field, w sdl.WhereField) (string, error) {
	// A model that gives no ElemType (one written by hand) means velox's
	// own, and no PkgPath a package the type's qualifier names.
	if fd.ElemType == "" || fd.ElemType == w.GoType && (fd.PkgPath == w.GoPkg || fd.PkgPath == "") {
		return "", nil
	}
	var as, base string
	switch fd.Type {
	case "string", "enum":
		as, base = "AsString", "string"
	case "bool":
		as, base = "AsBool", "bool"
	case "float32", "float64":
		as, base = "AsFloat", fd.Type
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64":
		as, base = "AsInt", fd.Type
	case "uuid":
		as, base = "AsUUID", "[16]byte"
	case "time":
		as, base = "", "time.Time"
	default:
		return "", fmt.Errorf("schema %s, field %s: velox-graphql cannot convert a where predicate's %s to its Go type %s", e.Name, fd.Name, w.GoType, fd.ElemType)
	}
	if err := r.converts(e, fd, base); err != nil {
		return "", err
	}
	if as == "" {
		// A time field of another Go type: converts refuses it when the
		// model gives its Kind, and this when it does not (a model written
		// by hand).
		return "", fmt.Errorf("schema %s, field %s: velox-graphql converts a time field only from time.Time, and its Go type is %s; give the field time.Time", e.Name, fd.Name, fd.ElemType)
	}
	col, err := r.qualify(f, e, fd, fd.ElemType, fd.PkgPath)
	if err != nil {
		return "", err
	}
	from, err := r.qualify(f, e, fd, w.GoType, w.GoPkg)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s.%s[%s, %s]", f.gqlimpl(), as, col, from), nil
}
