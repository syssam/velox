package gogen

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/syssam/velox/contrib/graphql/internal/english"
	"github.com/syssam/velox/contrib/graphql/internal/sdl"
	"github.com/syssam/velox/plugin"
)

// lowerFirst is name with its first letter in lower case, as velox-gen names
// an entity's unexported functions (userDescriptor).
func lowerFirst(name string) string {
	return strings.ToLower(name[:1]) + name[1:]
}

// typeVar is the unexported variable that describes t to gqlgraph,
// postType.
func typeVar(t *sdl.Type) string { return lowerFirst(t.Entity.Name) + "Type" }

// optType is the type of the options of a query's Paginate to t's nodes,
// PostPaginateOption, and withOrder and withFilter its constructors,
// WithPostOrder and WithPostFilter, as ent names them.
func optType(t *sdl.Type) string    { return t.Name + "PaginateOption" }
func withOrder(t *sdl.Type) string  { return "With" + t.Name + "Order" }
func withFilter(t *sdl.Type) string { return "With" + t.Name + "Filter" }

// newConnImpl is the unexported function PaginateQuery and the edge
// methods to t's nodes call, newPostConnection: it pages a gqlgraph.Source.
func newConnImpl(t *sdl.Type) string { return "new" + t.Name + "Connection" }

// labelCamel is an entity's label as velox-gen begins the names of its
// constants: post_tag is postTag.
func labelCamel(label string) string {
	first, rest, ok := strings.Cut(label, "_")
	if !ok {
		return label
	}
	return first + plugin.GoName(rest)
}

// colConst is the constant velox-gen declares for the position of e's field
// fd, its column: postColTitle.
func colConst(e *plugin.Entity, fd *plugin.Field) string {
	return labelCamel(e.Label) + "Col" + fd.GoName
}

// edgeConst is the constant velox-gen declares for the position of e's edge
// ed: postEdgeAuthor.
func edgeConst(e *plugin.Entity, ed *plugin.Edge) string {
	return labelCamel(e.Label) + "Edge" + ed.GoName
}

// connParams are the parameters of a connection to t's nodes, in the SDL's
// argument order (sdl's connArgs), in groups for a signature: paging, then
// orderBy and where when t has an order or a where input.
func connParams(f *file, t *sdl.Type) []string {
	vgql := f.veloxgql()
	ps := []string{"after *" + vgql + ".Cursor, first *int, before *" + vgql + ".Cursor, last *int"}
	var rest []string
	if t.Order != nil {
		rest = append(rest, "orderBy "+orderType(t))
	}
	if t.Where != nil {
		rest = append(rest, "where *"+t.Name+"WhereInput")
	}
	if len(rest) > 0 {
		ps = append(ps, strings.Join(rest, ", "))
	}
	return ps
}

// signature is the line that opens the function name, whose parameters are
// first then connParams, with results: one line, or one group a line.
func signature(name, first string, params []string, results string) string {
	return items(name+"(", append([]string{first}, params...), ") "+results+" {")
}

// connArgs are the names of connParams, for a call.
func connArgs(t *sdl.Type) string {
	as := "after, first, before, last"
	if t.Order != nil {
		as += ", orderBy"
	}
	if t.Where != nil {
		as += ", where"
	}
	return as
}

// writeConnection writes XConnection and XEdge; XPaginateOption and its
// constructors; the entity's PaginateQuery, which pages a query of t's nodes
// for velox.Query's Paginate; newXConnection, which pages a query or an edge
// for PaginateQuery and the edge methods; and the variable that describes t
// to gqlgraph.
func (r *renderer) writeConnection(f *file, t *sdl.Type) error {
	e, n := t.Entity, t.Name
	what := "schema " + e.Name
	names := []string{n + "Connection", n + "Edge", optType(t), e.Name + ".PaginateQuery", newConnImpl(t), typeVar(t)}
	if t.Order != nil {
		names = append(names, withOrder(t))
	}
	if t.Where != nil {
		names = append(names, withFilter(t))
	}
	for _, name := range names {
		if err := r.declare(name, what); err != nil {
			return err
		}
	}
	ctx, vgql, impl := f.use("context", "context"), f.veloxgql(), f.gqlgraph()

	resolves := "gqlgen resolves them"
	if r.graphqlGo {
		resolves = "they are resolved"
	}
	f.doc("", fmt.Sprintf("%sConnection is the GraphQL type %[1]sConnection. Its fields run their queries when %s.", n, resolves))
	f.p("type %sConnection struct {", n)
	f.p("\tc *%s.Connection", impl)
	f.p("}")
	f.p("")
	f.doc("", "Edges are the edges of the page, in the connection's order.")
	f.p("func (c *%sConnection) Edges(ctx %s.Context) ([]*%[1]sEdge, error) {", n, ctx)
	f.p("\tp, err := c.c.Page(ctx)")
	f.p("\tif err != nil {")
	f.p("\t\treturn nil, err")
	f.p("\t}")
	f.p("\tslab := make([]%sEdge, len(p.Rows)) // one allocation for all the edges", n)
	f.p("\tedges := make([]*%sEdge, len(p.Rows))", n)
	f.p("\tfor i := range p.Rows {")
	f.p("\t\tslab[i] = %sEdge{Node: p.Node(i).(*%s), Cursor: p.Cursors[i]}", n, e.Name)
	f.p("\t\tedges[i] = &slab[i]")
	f.p("\t}")
	f.p("\treturn edges, nil")
	f.p("}")
	f.p("")
	f.p("// PageInfo is the page info of the page.")
	f.p("func (c *%sConnection) PageInfo(ctx %s.Context) (*%s.PageInfo, error) {", n, ctx, vgql)
	f.p("\tp, err := c.c.Page(ctx)")
	f.p("\tif err != nil {")
	f.p("\t\treturn nil, err")
	f.p("\t}")
	f.p("\treturn &p.Info, nil")
	f.p("}")
	f.p("")
	f.doc("", fmt.Sprintf("TotalCount is the number of %s nodes in the whole connection, not only on this page.", n))
	f.p("func (c *%sConnection) TotalCount(ctx %s.Context) (int, error) { return c.c.TotalCount(ctx) }", n, ctx)
	f.p("")
	f.doc("", fmt.Sprintf("%sEdge is the GraphQL type %[1]sEdge.", n))
	f.p("type %sEdge struct {", n)
	f.p("\tNode   *%s", e.Name)
	f.p("\tCursor %s.Cursor", vgql)
	f.p("}")
	f.p("")

	r.writePaginate(f, t)

	f.doc("", fmt.Sprintf("%s is PaginateQuery of the nodes from leads to: a query's, or an edge's, whose queries batch.", newConnImpl(t)))
	f.p("%s", signature("func "+newConnImpl(t), "ctx "+ctx+".Context, from "+impl+".Source", connParams(f, t), "(*"+n+"Connection, error)"))
	args := "After: after, First: first, Before: before, Last: last"
	if t.Where != nil {
		args += fmt.Sprintf(", Where: %s.WhereOf(where, (*%sWhereInput).%s)", impl, t.Name, predMethod)
	}
	f.p("\targs := %s.Args{%s}", impl, args)
	if t.Order != nil {
		if t.MultiOrder {
			// Paginate refuses more than MaxOrderTerms terms.
			f.p("\tfor _, o := range orderBy {")
			f.p("\t\targs.Order = append(args.Order, %s.Order{Field: string(o.Field), Direction: o.Direction})", impl)
			f.p("\t}")
		} else {
			f.p("\tif o := orderBy; o != nil {")
			f.p("\t\targs.Order = []%s.Order{{Field: string(o.Field), Direction: o.Direction}}", impl)
			f.p("\t}")
		}
	}
	f.p("\tc, err := %s.Paginate(ctx, from, &%s, args)", impl, typeVar(t))
	f.p("\tif err != nil {")
	f.p("\t\treturn nil, err")
	f.p("\t}")
	f.p("\treturn &%sConnection{c: c}, nil", n)
	f.p("}")
	f.p("")

	f.doc("", fmt.Sprintf("%s describes the GraphQL type %s to gqlgraph.", typeVar(t), n))
	f.p("var %s = %s.Type{", typeVar(t), impl)
	f.p("\tName:  %q,", n)
	f.p("\tTable: %s.TableOf[%s],", f.untyped(), e.Name)
	var keys []string
	for _, i := range e.PrimaryKey {
		fd := e.Fields[i]
		// The key's Go type pages.
		if err := r.checkTerm(e, fd); err != nil {
			return err
		}
		keys = append(keys, fmt.Sprintf("{Name: %q, Index: %s}", fd.Name, colConst(e, fd)))
	}
	f.p("\tKey: %s,", items("[]"+impl+".Column{", keys, "}"))
	if t.Order != nil {
		f.p("\tOrder: []%s.Column{", impl)
		for _, v := range t.Order {
			if v.Count {
				// The number of a to-many edge's nodes: no column.
				f.p("\t\t{Name: %q, Index: -1, Edge: %s + 1, Target: %s.TableOf[%s], Count: true},", v.Value, edgeConst(e, e.Edges[v.Edge-1]), f.untyped(), v.Target.Name)
				continue
			}
			fd, _ := orderField(e, v)
			if v.Edge == 0 {
				if err := r.checkTerm(e, fd); err != nil {
					return err
				}
				f.p("\t\t{Name: %q, Index: %s},", v.Value, colConst(e, fd))
				continue
			}
			// The target's field pages as the node's own does: a pointer's
			// nil is NULL, which a cursor holds as null.
			if err := r.checkTerm(v.Target, fd); err != nil {
				return err
			}
			f.p("\t\t{Name: %q, Index: %s, Edge: %s + 1, Target: %s.TableOf[%s]},", v.Value, colConst(v.Target, fd), edgeConst(e, e.Edges[v.Edge-1]), f.untyped(), v.Target.Name)
		}
		f.p("\t},")
	}
	r.writeSelection(f, t)
	if t.Where != nil {
		if err := r.writeWhereFields(f, t); err != nil {
			return err
		}
	}
	writeInputNames(f, t)
	if r.veloxe {
		f.p("\tVeloxe: true,")
	}
	f.p("}")
	return nil
}

// writeSelection writes the fields of t's gqlgraph.Type that give the columns a
// selection reads: AlwaysRead, the key and every foreign key on the row (a
// connection adds its order's columns at run time); Fields, the column of each of t's fields; and
// Edges, t's edge fields by relation, which read no column of their own and
// name an edge's field for gqlgraph's batches.
func (r *renderer) writeSelection(f *file, t *sdl.Type) {
	e := t.Entity
	read := slices.Clone(e.PrimaryKey) // indexes in e.Fields
	for i, fd := range e.Fields {
		if !slices.Contains(read, i) && r.foreignKey(e, fd) {
			read = append(read, i)
		}
	}
	cols := make([]string, len(read))
	for k, i := range read {
		cols[k] = colConst(e, e.Fields[i])
	}
	f.p("\tAlwaysRead: %s,", items("[]int{", cols, "}"))
	var fields []string
	for _, fd := range t.Fields {
		fields = append(fields, fmt.Sprintf("%s: %q", colConst(e, e.Fields[fd.Field]), fd.Name))
	}
	if len(fields) > 0 {
		f.p("\tFields: %s,", items("[]string{", fields, "}"))
	}
	var edges []string
	for _, ed := range t.Edges {
		edges = append(edges, edgeConst(e, e.Edges[ed.Edge])+": "+strconv.Quote(ed.Name))
	}
	if len(edges) > 0 {
		f.p("\tEdges: %s,", items("[]string{", edges, "}"))
	}
}

// writeInputNames writes the fields of t's gqlgraph.Type that name, by column, the
// field of an input that sets the column, which a validator's error names:
// Inputs, when t's inputs name the same columns, else CreateInputs and
// UpdateInputs.
func writeInputNames(f *file, t *sdl.Type) {
	e := t.Entity
	list := func(names []inputName) []string {
		var out []string
		for _, x := range names {
			for _, fd := range e.Fields {
				if fd.GoName == x.goName {
					out = append(out, fmt.Sprintf("%s: %q", colConst(e, fd), x.gql))
				}
			}
		}
		return out
	}
	write := func(field string, names []string) {
		if len(names) > 0 {
			f.p("\t%s: %s,", field, items("[]string{", names, "}"))
		}
	}
	cn, un := list(inputNames(e, t.Create)), list(inputNames(e, t.Update))
	switch {
	case t.Create == nil:
		write("Inputs", un)
	case t.Update == nil || slices.Equal(cn, un):
		write("Inputs", cn)
	default:
		write("CreateInputs", cn)
		write("UpdateInputs", un)
	}
}

// checkTerm checks that a connection can order by e's field fd: keyset paging
// continues after a value of its column's type (termBase), and gqlgraph
// converts a value of its Go type to that type and back.
func (r *renderer) checkTerm(e *plugin.Entity, fd *plugin.Field) error {
	base, err := termBase(newFile(), e, fd)
	if err != nil {
		return err
	}
	if elem := cmp.Or(fd.ElemType, base); elem != base { // a model written by hand gives none: velox's own
		return r.converts(e, fd, base)
	}
	return nil
}

// termBase is the Go type a cursor holds the values of fd as: velox's
// standard Go type of its column, which gqlgraph converts a cursor's values
// to.
func termBase(f *file, e *plugin.Entity, fd *plugin.Field) (string, error) {
	switch fd.Type {
	case "string", "enum":
		return "string", nil
	case "bool", "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "float32", "float64":
		return fd.Type, nil
	case "time":
		return f.use("time", "time") + ".Time", nil
	case "uuid":
		return "[16]byte", nil
	}
	return "", fmt.Errorf("schema %s, field %s: a connection orders by it, and keyset paging cannot continue after %s %s value", e.Name, fd.Name, english.Article(fd.Type), fd.Type)
}

// integerKinds and floatKinds are the reflect kinds of the Go types that
// convert to an integer and to a float.
var (
	integerKinds = map[string]bool{"int": true, "int8": true, "int16": true, "int32": true, "int64": true, "uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true}
	floatKinds   = map[string]bool{"float32": true, "float64": true}
)

// converts checks that the generated code can convert a value of fd's Go
// type to base, the Go type of its scalar or velox's standard Go type for
// its column, and back. Only the Go type's kind is known, so a struct is
// refused for a time, and a kind of another scalar's for the others; a
// field of velox's standard type or of velox-gen's enum type has no kind,
// and converts.
func (r *renderer) converts(e *plugin.Entity, fd *plugin.Field, base string) error {
	if fd.Kind == "" {
		return nil
	}
	var ok bool
	switch base {
	case "string":
		ok = fd.Kind == "string"
	case "bool":
		ok = fd.Kind == "bool"
	case "float32", "float64":
		ok = floatKinds[fd.Kind]
	case "time.Time", "veloxgql.Time":
		// A struct that is not time.Time may wrap one, or be anything.
		ok = fd.ElemType == "time.Time" && fd.PkgPath == "time"
	case "[16]byte", "veloxgql.UUID":
		ok = fd.Kind == "array"
	case "[]byte", "veloxgql.Bytes":
		ok = fd.Kind == "slice"
	default: // the integers, and veloxgql.Uint64
		ok = integerKinds[fd.Kind]
	}
	switch {
	case !ok && (base == "time.Time" || base == "veloxgql.Time"):
		return fmt.Errorf("schema %s, field %s: velox-graphql converts a time field only from time.Time, and its Go type is %s; give the field time.Time", e.Name, fd.Name, fd.ElemType)
	case !ok:
		return fmt.Errorf("schema %s, field %s: velox-graphql converts its Go type %s to %s, and %s %s does not convert; give the field velox's standard Go type, or one whose underlying type is it", e.Name, fd.Name, fd.ElemType, base, english.Article(fd.Kind), fd.Kind)
	}
	return nil
}

// foreignKey reports whether e's field fd is a foreign key on e's rows: a
// field velox added for an edge, or the field an edge of any entity names
// on e.
func (r *renderer) foreignKey(e *plugin.Entity, fd *plugin.Field) bool {
	if fd.Implicit != "" {
		return true
	}
	for _, x := range r.m.Entities {
		for _, ed := range x.Edges {
			if ed.FieldOn == e.Name && ed.Field == fd.Name {
				return true
			}
		}
	}
	return false
}

// writePaginate writes XPaginateOption, its constructors and the entity's
// PaginateQuery, which pages a query of t's nodes for velox.Query's
// Paginate.
func (r *renderer) writePaginate(f *file, t *sdl.Type) {
	e, n := t.Entity, t.Name
	ctx, impl, velox := f.use("context", "context"), f.gqlgraph(), f.velox()
	var how []string
	if t.Where != nil {
		how = append(how, "filtered by where")
	}
	if t.Order != nil {
		how = append(how, "ordered by orderBy and then the key")
	} else {
		how = append(how, "ordered by the key")
	}
	selects := "the key"
	if t.Order != nil {
		selects = "the key and the order columns"
	}
	opt := optType(t)
	switch {
	case t.Order == nil && t.Where == nil:
		f.doc("", fmt.Sprintf("%s is an option of a query's Paginate to %s nodes. It has none: %[2]s has no order or where input.", opt, n))
		f.p("type %s struct{}", opt)
		f.p("")
	default:
		var withs []string
		if t.Order != nil {
			withs = append(withs, withOrder(t))
		}
		if t.Where != nil {
			withs = append(withs, withFilter(t))
		}
		f.doc("", fmt.Sprintf("%s is an option of a query's Paginate to %s nodes: %s. Of two options that set the same thing, the later wins.", opt, n, strings.Join(withs, " or ")))
		f.p("type %s struct {", opt)
		if t.Order != nil {
			f.p("\torderBy %s", orderType(t))
		}
		if t.Where != nil {
			f.p("\twhere   *%sWhereInput", n)
		}
		f.p("}")
		f.p("")
	}
	if t.Order != nil {
		f.doc("", fmt.Sprintf("%s orders the connection by orderBy and then the key.", withOrder(t)))
		f.p("func %s(orderBy %s) %s { return %[3]s{orderBy: orderBy} }", withOrder(t), orderType(t), opt)
		f.p("")
	}
	if t.Where != nil {
		f.doc("", fmt.Sprintf("%s filters the connection by where.", withFilter(t)))
		f.p("func %s(where *%sWhereInput) %s { return %[3]s{where: where} }", withFilter(t), n, opt)
		f.p("")
	}
	f.doc("", fmt.Sprintf("PaginateQuery is the connection to the %s nodes q returns, %s, and paged by the other arguments: velox.Query's Paginate. q must not be ordered, and must select %s.", n, strings.Join(how, ", "), selects))
	f.p("%s", signature("func (*"+e.Name+") PaginateQuery", "ctx "+ctx+".Context, q "+velox+".Query["+e.Name+"]",
		[]string{connParams(f, t)[0], "opts []" + opt}, "(*"+n+"Connection, error)"))
	if t.Order != nil || t.Where != nil {
		f.p("\tvar o %s", opt)
		f.p("\tfor _, x := range opts {")
		if t.Order != nil {
			f.p("\t\tif x.orderBy != nil {")
			f.p("\t\t\to.orderBy = x.orderBy")
			f.p("\t\t}")
		}
		if t.Where != nil {
			f.p("\t\tif x.where != nil {")
			f.p("\t\t\to.where = x.where")
			f.p("\t\t}")
		}
		f.p("\t}")
	}
	f.p("\treturn %s(ctx, %s.Query(%s.FromQuery(q)), %s)", newConnImpl(t), impl, f.untyped(),
		strings.NewReplacer("orderBy", "o.orderBy", "where", "o.where").Replace(connArgs(t)))
	f.p("}")
	f.p("")
}
