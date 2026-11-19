package gogen

import (
	"fmt"

	"github.com/syssam/velox/contrib/graphql/internal/sdl"
	"github.com/syssam/velox/plugin"
)

func (r *renderer) writeOrder(f *file, t *sdl.Type) error {
	n, e := t.Name, t.Entity
	what := "schema " + e.Name
	for _, name := range []string{n + "Order", n + "OrderField"} {
		if err := r.declare(name, what); err != nil {
			return err
		}
	}
	f.p("")
	f.doc("", fmt.Sprintf("%sOrder is the GraphQL input %[1]sOrder, an order of connections to %[1]s.", n))
	f.p("type %sOrder struct {", n)
	f.p("\t// Direction is the direction of the order, ASC unless the input says.")
	f.p("\tDirection %s.OrderDirection", f.veloxgql())
	f.p("\t// Field is the field to order by.")
	f.p("\tField %sOrderField", n)
	f.p("}")
	f.p("")
	f.doc("", fmt.Sprintf("%sOrderField is the GraphQL enum %[1]sOrderField, the fields connections to %[1]s can be ordered by.", n))
	f.p("type %sOrderField string", n)
	f.p("")
	f.p("// The values of %sOrderField.", n)
	f.p("const (")
	for _, v := range t.Order {
		fd, via := orderField(e, v)
		c := n + "OrderField" + via + fd.GoName
		label := fd.Name
		if v.Edge != 0 {
			label = e.Edges[v.Edge-1].Name + "." + fd.Name
		}
		if err := r.declare(c, what+", field "+label); err != nil {
			return err
		}
		f.p("\t// %s orders by the field %s.", c, label)
		f.p("\t%s %sOrderField = %q", c, n, v.Value)
	}
	f.p(")")
	f.p("")
	f.p("// IsValid reports whether o is a value of %sOrderField.", n)
	f.p("func (o %sOrderField) IsValid() bool { return %s.IsOrderField(string(o)) }", n, typeVar(t))
	f.p("")
	f.p("// String returns o's value.")
	f.p("func (o %sOrderField) String() string { return string(o) }", n)
	f.p("")
	f.p("// MarshalGQL writes o as a value of the GraphQL enum %sOrderField.", n)
	f.p("func (o %sOrderField) MarshalGQL(w %s.Writer) { %s.WriteString(w, string(o)) }", n, f.use("io", "io"), f.gqlgraph())
	f.p("")
	f.p("// UnmarshalGQL sets o from v, a value of the GraphQL enum %sOrderField.", n)
	f.p("func (o *%sOrderField) UnmarshalGQL(v any) error {", n)
	f.p("\treturn %s.UnmarshalOrderField(o, v, &%s)", f.gqlgraph(), typeVar(t))
	f.p("}")
	return nil
}

// orderType is the Go type of t's orderBy argument: a slice where the SDL's
// orderBy is a list.
func orderType(t *sdl.Type) string {
	if t.MultiOrder {
		return "[]*" + t.Name + "Order"
	}
	return "*" + t.Name + "Order"
}

// orderField is the field an order value orders by, and the prefix of its
// Go names: the edge's name for a field of an edge's target ("Author" for
// AuthorName), so that a target's field and the entity's own do not clash.
func orderField(e *plugin.Entity, v sdl.OrderValue) (fd *plugin.Field, via string) {
	if v.Edge == 0 {
		return e.Fields[v.Field], ""
	}
	return v.Target.Fields[v.Field], e.Edges[v.Edge-1].GoName
}
