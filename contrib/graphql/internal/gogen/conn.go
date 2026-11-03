package gogen

import (
	"cmp"
	"fmt"
	"slices"
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

// The functions of t's connections: the exported one the application composes
// a connection with, and the unexported ones of its key's and its terms.
func connFunc(t *sdl.Type) string { return plugin.GoPlural(t.Name) + "ConnectionOf" }
func keyFunc(t *sdl.Type) string  { return lowerFirst(t.Entity.Name) + "KeyGQL" }
func termFunc(t *sdl.Type) string { return lowerFirst(t.Entity.Name) + "TermGQL" }
func colsFunc(t *sdl.Type) string { return lowerFirst(t.Entity.Name) + "ColumnsGQL" }

// connParams are the parameters of a connection to t's nodes, in the SDL's
// argument order (sdl's connArgs): paging, and orderBy and where when t has
// an order and a where input.
func connParams(f *file, t *sdl.Type) string {
	vgql := f.veloxgql()
	ps := "after *" + vgql + ".Cursor, first *int, before *" + vgql + ".Cursor, last *int"
	switch {
	case t.Order != nil && t.MultiOrder:
		ps += ", orderBy []*" + t.Name + "Order"
	case t.Order != nil:
		ps += ", orderBy *" + t.Name + "Order"
	}
	if t.Where != nil {
		ps += ", where *" + t.Name + "WhereInput"
	}
	return ps
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

// writeConnection writes XConnection and XEdge, the root function of the
// connection to every node, and the unexported functions every connection
// to t's nodes is made with: of the connection, of the key's order and of
// XOrder's terms.
func (r *renderer) writeConnection(f *file, t *sdl.Type) error {
	e, n := t.Entity, t.Name
	what := "schema " + e.Name
	root := plugin.GoPlural(n) + "Connection"
	for _, name := range []string{n + "Connection", n + "Edge", root, connFunc(t), keyFunc(t), colsFunc(t)} {
		if err := r.declare(name, what); err != nil {
			return err
		}
	}
	ctx, vgql, impl, velox := f.use("context", "context"), f.veloxgql(), f.gqlimpl(), f.velox()

	f.doc("", fmt.Sprintf("%sConnection is the GraphQL type %[1]sConnection, a connection to a list of %[1]s. It holds the query of the whole list and the page asked for; gqlgen calls its methods for the fields a query selects, and each runs its query only then: edges and pageInfo share one page query, and totalCount runs a COUNT.", n))
	f.p("type %sConnection struct {", n)
	f.p("\tc *%s.Connection[%s]", impl, e.Name)
	f.p("}")
	f.p("")
	f.doc("", "Edges are the edges of the page, in the connection's order. The first call of Edges or PageInfo runs the page query, and the other reads its page.")
	f.p("func (c *%sConnection) Edges(ctx %s.Context) ([]*%[1]sEdge, error) {", n, ctx)
	f.p("\tp, err := c.c.Page(ctx)")
	f.p("\tif err != nil {")
	f.p("\t\treturn nil, err")
	f.p("\t}")
	f.p("\tslab := make([]%sEdge, len(p.Rows)) // one allocation for all the edges", n)
	f.p("\tedges := make([]*%sEdge, len(p.Rows))", n)
	f.p("\tfor i, x := range p.Rows {")
	f.p("\t\tslab[i] = %sEdge{Node: x, Cursor: p.Cursors[i]}", n)
	f.p("\t\tedges[i] = &slab[i]")
	f.p("\t}")
	f.p("\treturn edges, nil")
	f.p("}")
	f.p("")
	f.p("// PageInfo is the page info of the page; see Edges.")
	f.p("func (c *%sConnection) PageInfo(ctx %s.Context) (*%s.PageInfo, error) {", n, ctx, vgql)
	f.p("\tp, err := c.c.Page(ctx)")
	f.p("\tif err != nil {")
	f.p("\t\treturn nil, err")
	f.p("\t}")
	f.p("\treturn &p.Info, nil")
	f.p("}")
	f.p("")
	f.doc("", fmt.Sprintf("TotalCount is the number of %s nodes in the whole connection, not only on this page. It counts the rows when gqlgen asks for it, which is only when the field totalCount is selected.", n))
	f.p("func (c *%sConnection) TotalCount(ctx %s.Context) (int, error) { return c.c.TotalCount(ctx) }", n, ctx)
	f.p("")
	f.doc("", fmt.Sprintf("%sEdge is the GraphQL type %[1]sEdge, an edge in a connection to a list of %[1]s.", n))
	f.p("type %sEdge struct {", n)
	f.p("\t// Node is the %s at the end of the edge.", n)
	f.p("\tNode *%s", e.Name)
	f.p("\t// Cursor is the cursor of the edge, for paging.")
	f.p("\tCursor %s.Cursor", vgql)
	f.p("}")
	f.p("")

	f.doc("", fmt.Sprintf("%s is the connection to every %s node, through the DB veloxgql.DBFromContext reads from ctx, paged and ordered by the arguments of a connection to %[2]s: a Query field's resolver returns it. It fails before any query runs on arguments gqlimpl.NewConnection refuses.", root, n))
	f.p("func %s(ctx %s.Context, %s) (*%sConnection, error) {", root, ctx, connParams(f, t), n)
	f.p("\tdb, err := %s.DBFromContext(ctx)", vgql)
	f.p("\tif err != nil {")
	f.p("\t\treturn nil, err")
	f.p("\t}")
	f.p("\treturn %s(ctx, db.Query[%s](), %s)", connFunc(t), e.Name, connArgs(t))
	f.p("}")
	f.p("")

	var how []string
	if t.Where != nil {
		how = append(how, "filtered by where")
	}
	if t.Order != nil {
		how = append(how, "ordered by orderBy and then the key")
	} else {
		how = append(how, "ordered by the key")
	}
	f.doc("", fmt.Sprintf("%s is the connection to the %s nodes q returns, %s, and paged by the other arguments. q keeps its filters and With loads, and the scope the application gave it; its Offset is cleared and its Limit replaced, since the page is the connection's. q must have no OrderBy: the connection orders it, and a q with one is refused with an internal error, since its order would come first and break the cursors. q must also select the key and the columns orderBy names, which the cursors hold: a q whose Columns or Omit drops one is refused with an internal error. In a gqlgen field, the page reads only the columns the selection needs (%s), of those q selects; a column the selection does not need is left zero in the nodes, except the key, the foreign keys and the order columns, so an application's resolver must not read one it did not ask for. It runs no query.", connFunc(t), n, strings.Join(how, ", "), colsFunc(t)))
	f.p("func %s(ctx %s.Context, q %s.Query[%s], %s) (*%sConnection, error) {", connFunc(t), ctx, velox, e.Name, connParams(f, t), n)
	r.writeSelects(f, t)
	f.p("\tif fields, ok := %s.Selected(ctx, %q); ok {", impl, n)
	f.p("\t\tq = %s.Narrow(q, %s(fields))", impl, colsFunc(t))
	f.p("\t}")
	if t.Where != nil {
		f.p("\tif where != nil {")
		f.p("\t\tp, err := where.Pred(ctx)")
		f.p("\t\tif err != nil {")
		f.p("\t\t\treturn nil, err")
		f.p("\t\t}")
		f.p("\t\tq = q.Where(p)")
		f.p("\t}")
	}
	order := "nil"
	if t.Order != nil {
		order = "order"
		f.p("\tvar order []%s.OrderTerm[%s]", impl, e.Name)
		if t.MultiOrder {
			// NewConnection refuses more than MaxOrderTerms terms.
			f.p("\tfor _, o := range orderBy {")
		} else {
			f.p("\tif o := orderBy; o != nil {")
		}
		f.p("\t\tterm, err := %s(o)", termFunc(t))
		f.p("\t\tif err != nil {")
		f.p("\t\t\treturn nil, err")
		f.p("\t\t}")
		f.p("\t\torder = append(order, term)")
		f.p("\t}")
	}
	f.p("\tc, err := %s.NewConnection(ctx, q, %q, %s.Args{After: after, First: first, Before: before, Last: last}, %s, %s())", impl, n, impl, order, keyFunc(t))
	f.p("\tif err != nil {")
	f.p("\t\treturn nil, err")
	f.p("\t}")
	f.p("\treturn &%sConnection{c: c}, nil", n)
	f.p("}")
	f.p("")

	f.doc("", fmt.Sprintf("%s is the order of %s's key, which ends every order of a connection to %s.", keyFunc(t), e.Name, n))
	f.p("func %s() []%s.OrderTerm[%s] {", keyFunc(t), impl, e.Name)
	f.p("\treturn []%s.OrderTerm[%s]{", impl, e.Name)
	for _, i := range e.PrimaryKey {
		fd := e.Fields[i]
		term, err := r.term(f, e, fd, fmt.Sprintf("%q", fd.Name), vgql+".OrderDirectionAsc", "\t\t")
		if err != nil {
			return err
		}
		f.p("\t\t%s,", term)
	}
	f.p("\t}")
	f.p("}")
	r.writeColumns(f, t)
	if t.Order == nil {
		return nil
	}
	if err := r.declare(termFunc(t), what); err != nil {
		return err
	}
	f.p("")
	f.doc("", fmt.Sprintf("%s is the order term o, an order of connections to %s.", termFunc(t), n))
	f.p("func %s(o *%sOrder) (%s.OrderTerm[%s], error) {", termFunc(t), n, impl, e.Name)
	f.p("\tswitch o.Field {")
	for _, v := range t.Order {
		fd, via := orderField(e, v)
		var term string
		var err error
		if v.Edge == 0 {
			term, err = r.term(f, e, fd, "string(o.Field)", "o.Direction", "\t\t")
		} else {
			term, err = r.viaTerm(f, e, v, fd, "string(o.Field)", "o.Direction", "\t\t")
		}
		if err != nil {
			return err
		}
		f.p("\tcase %sOrderField%s%s:", n, via, fd.GoName)
		f.p("\t\treturn %s, nil", term)
	}
	f.p("\t}")
	f.p("\treturn %s.OrderTerm[%s]{}, %s.InputErrorf(\"%sOrder: %%q is not a value of %sOrderField\", o.Field)", impl, e.Name, impl, n, n)
	f.p("}")
	return nil
}

// term is the expression of a gqlimpl.Term of the field fd, named field
// and in the direction dir (Go expressions both), at indent.
func (r *renderer) term(f *file, e *plugin.Entity, fd *plugin.Field, field, dir, indent string) (string, error) {
	base, err := termBase(f, e, fd)
	if err != nil {
		return "", err
	}
	elem := cmp.Or(fd.ElemType, base) // a model written by hand gives none: velox's own
	conv := elem != base
	if conv {
		if err := r.converts(e, fd, base); err != nil {
			return "", err
		}
		if elem, err = r.qualify(f, e, fd, elem, fd.PkgPath); err != nil {
			return "", err
		}
	}
	to := func(typ, v string) string {
		if conv {
			return typ + "(" + v + ")"
		}
		return v
	}
	handle := e.Handles + "." + fd.GoName
	if fd.Pointer {
		// A Nillable field: nil is NULL, which the cursor carries as null. A
		// key is never Nillable.
		get := fmt.Sprintf("func(x *%s) (%s, bool) {\n%s\t\tif x.%s == nil {\n%s\t\t\tvar z %s\n%s\t\t\treturn z, false\n%s\t\t}\n%s\t\treturn %s, true\n%s\t}",
			e.Name, base, indent, fd.GoName, indent, base, indent, indent, indent, to(base, "*x."+fd.GoName), indent)
		set := fmt.Sprintf("func(x *%s, v %s, ok bool) {\n%s\t\tx.%s = nil\n%s\t\tif ok {\n%s\t\t\tw := %s\n%s\t\t\tx.%s = &w\n%s\t\t}\n%s\t}",
			e.Name, base, indent, fd.GoName, indent, indent, to(elem, "v"), indent, fd.GoName, indent, indent)
		return fmt.Sprintf("%s.NullTerm(%s, %s, %s.Asc(), %s.Desc(),\n%s\t%s,\n%s\t%s)", f.gqlimpl(), field, dir, handle, handle, indent, get, indent, set), nil
	}
	get := fmt.Sprintf("func(x *%s) %s { return %s }", e.Name, base, to(base, "x."+fd.GoName))
	set := fmt.Sprintf("func(x *%s, v %s) { x.%s = %s }", e.Name, base, fd.GoName, to(elem, "v"))
	return fmt.Sprintf("%s.Term(%s, %s, %s.Asc(), %s.Desc(),\n%s\t%s,\n%s\t%s)", f.gqlimpl(), field, dir, handle, handle, indent, get, indent, set), nil
}

// termBase is the Go type gqlimpl.Term takes the values of fd as: velox's
// standard Go type of its column, which the connection converts a cursor's
// values to.
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

// writeSelects writes the refusal, in XConnectionOf, of a q that does not
// select the key or a column of an orderBy term: the cursors hold them.
func (r *renderer) writeSelects(f *file, t *sdl.Type) {
	e, n := t.Entity, t.Name
	errorf := f.use("fmt", "fmt") + ".Errorf"
	var keys []string
	for _, i := range e.PrimaryKey {
		keys = append(keys, e.Handles+"."+e.Fields[i].GoName)
	}
	f.p("\tif !q.Selects(%s) {", strings.Join(keys, ", "))
	f.p("\t\treturn nil, %s(\"veloxgql: %s: the base query does not select the key, which the cursors hold\")", errorf, connFunc(t))
	f.p("\t}")
	if t.Order == nil {
		return
	}
	if t.MultiOrder {
		f.p("\tfor _, o := range orderBy {")
	} else {
		f.p("\tif o := orderBy; o != nil {")
	}
	var cases []string
	for _, v := range t.Order {
		if v.Edge != 0 {
			continue // the join reads the target's columns, and no column of the entity's
		}
		fd := e.Fields[v.Field]
		cases = append(cases, fmt.Sprintf("o.Field == %sOrderField%s && !q.Selects(%s.%s)", n, fd.GoName, e.Handles, fd.GoName))
	}
	if len(cases) == 0 {
		f.p("\t\t_ = o")
		f.p("\t}")
		return
	}
	f.p("\t\tswitch {")
	f.p("\t\tcase %s:", strings.Join(cases, ",\n\t\t\t"))
	f.p("\t\t\treturn nil, %s(\"veloxgql: %s: the base query does not select the column of %%s, which orderBy names and the cursors hold\", o.Field)", errorf, connFunc(t))
	f.p("\t\t}")
	f.p("\t}")

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

// writeColumns writes <entity>ColumnsGQL, the columns a selection of t's
// fields reads: the key, every foreign key on the
// row (an edge, the application's With loads, and mutations' checks read
// them) and every order field's column (a cursor holds them), always; and
// each selected field's column. A field it does not know, an application's
// own, may read any column: then all of them.
func (r *renderer) writeColumns(f *file, t *sdl.Type) {
	e, velox := t.Entity, f.velox()
	base := slices.Clone(e.PrimaryKey) // the indexes in e.Fields of the columns always read
	in := func(i int) bool { return slices.Contains(base, i) }
	for i, fd := range e.Fields {
		if !in(i) && r.foreignKey(e, fd) {
			base = append(base, i)
		}
	}
	for _, v := range t.Order {
		if v.Edge == 0 && !in(v.Field) {
			base = append(base, v.Field)
		}
	}
	cols := make([]string, len(base))
	for k, i := range base {
		cols[k] = e.Handles + "." + e.Fields[i].GoName
	}
	var none []string // the fields whose columns are read always, and the edges
	for _, fd := range t.Fields {
		if in(fd.Field) {
			none = append(none, fd.Name)
		}
	}
	for _, ed := range t.Edges {
		none = append(none, ed.Name)
	}
	f.p("")
	f.doc("", fmt.Sprintf("%s is the columns a selection of the %s fields fields reads: the key, every foreign key on the row, every order field's column, and the column of each field. It is nil, every column, when a field is not one velox-graphql wrote, such as an application's own.", colsFunc(t), t.Name))
	f.p("func %s(fields []string) []%s.Column[%s] {", colsFunc(t), velox, e.Name)
	f.p("\tcols := []%s.Column[%s]{%s}", velox, e.Name, strings.Join(cols, ", "))
	f.p("\tfor _, f := range fields {")
	f.p("\t\tswitch f {")
	if len(none) > 0 {
		f.p("\t\tcase %s:", quoteList(none))
	}
	for _, fd := range t.Fields {
		if !in(fd.Field) {
			f.p("\t\tcase %q:", fd.Name)
			f.p("\t\t\tcols = append(cols, %s.%s)", e.Handles, e.Fields[fd.Field].GoName)
		}
	}
	f.p("\t\tdefault:")
	f.p("\t\t\treturn nil")
	f.p("\t\t}")
	f.p("\t}")
	f.p("\treturn cols")
	f.p("}")
}

// writeEdges writes the methods gqlgen binds t's edge fields to.
func (r *renderer) writeEdges(f *file, t *sdl.Type) error {
	for _, ed := range t.Edges {
		if err := r.writeEdge(f, t, ed); err != nil {
			return err
		}
	}
	return nil
}

// nilKey is, for the edge ed of e to tgt when e's row holds its key in a
// pointer, the condition under which the edge has no node, a Go expression
// of x, the owner; "" otherwise. It fails when the key the relation
// compares, the target's or the owner's, is not one column.
func (r *renderer) nilKey(e *plugin.Entity, ed *plugin.Edge, tgt *plugin.Entity) (string, error) {
	where := "schema " + e.Name + ", edge " + ed.Name
	oneKey := func(of *plugin.Entity) error {
		if len(of.PrimaryKey) != 1 {
			return fmt.Errorf("%s: velox-graphql finds its nodes by the key of %s, which is %d columns; skip the edge with graphql.Skip()", where, of.Name, len(of.PrimaryKey))
		}
		return nil
	}
	switch {
	case ed.Field != "" && ed.OwnsKey:
		if err := oneKey(tgt); err != nil {
			return "", err
		}
		for _, fd := range e.Fields {
			if fd.Name == ed.Field {
				if fd.Pointer {
					return "x." + fd.GoName + " == nil", nil
				}
				return "", nil
			}
		}
		return "", fmt.Errorf("%s: its foreign key %s is no field of %s", where, ed.Field, e.Name)
	case ed.Field != "":
		return "", oneKey(e)
	}
	if err := oneKey(e); err != nil {
		return "", err
	}
	return "", oneKey(tgt)
}

// writeEdge writes the method of the edge field ed of t: the edge's node or
// nodes, read through gqlimpl's batches (EdgeConnection, EdgeOne, EdgeList)
// on the DB veloxgql.DBFromContext reads from ctx. Each gets the edge's GraphQL
// field ("Post.likes"), which alone batches it, and a connection its
// arguments, which its batch keeps. It never reads the relation's field: an
// application's With may have loaded it filtered or limited.
func (r *renderer) writeEdge(f *file, t *sdl.Type, ed sdl.Edge) error {
	e, pe := t.Entity, t.Entity.Edges[ed.Edge]
	tt, tgt := ed.Target, ed.Target.Entity
	if err := r.declare(e.Name+"."+ed.Method, "schema "+e.Name+", edge "+pe.Name); err != nil {
		return err
	}
	nilKey, err := r.nilKey(e, pe, tgt)
	if err != nil {
		return err
	}
	if nilKey != "" && ed.Kind != sdl.EdgeUnique {
		return fmt.Errorf("schema %s, edge %s: a to-many edge whose key is on the owner's row, which velox-gen does not write", e.Name, pe.Name)
	}
	ctx, vgql, impl := f.use("context", "context"), f.veloxgql(), f.gqlimpl()
	rel := e.Handles + "." + pe.GoName
	field := fmt.Sprintf("%q", t.Name+"."+ed.Name)
	dbFrom := func() {
		f.p("\tdb, err := %s.DBFromContext(ctx)", vgql)
		f.p("\tif err != nil {")
		f.p("\t\treturn nil, err")
		f.p("\t}")
	}
	// query writes q, the target's query, reading only the columns the
	// field's selection needs.
	query := func() {
		dbFrom()
		f.p("\tq := db.Query[%s]()", tgt.Name)
		f.p("\tif fields, ok := %s.Selected(ctx, %q); ok {", impl, tt.Name)
		f.p("\t\tq = %s.Narrow(q, %s(fields))", impl, colsFunc(tt))
		f.p("\t}")
	}
	switch ed.Kind {
	case sdl.EdgeUnique:
		f.doc("", fmt.Sprintf("%s is the edge %s: the %s it leads to, or nil when there is none. It is read with the edge of every row read with x, in one query.", ed.Method, pe.Name, tt.Name))
		f.p("func (x *%s) %s(ctx %s.Context) (*%s, error) {", e.Name, ed.Method, ctx, tgt.Name)
		if nilKey != "" {
			f.p("\tif %s {", nilKey)
			f.p("\t\treturn nil, nil")
			f.p("\t}")
		}
		query()
		f.p("\treturn %s.EdgeOne(ctx, q, x, %s, %s)", impl, rel, field)
		f.p("}")
	case sdl.EdgeList:
		most := "the MaxList of veloxgql.LimitsFromContext(ctx)"
		if ed.MaxList != 0 {
			most = fmt.Sprintf("%d, or the MaxList of veloxgql.LimitsFromContext(ctx) when that is lower,", ed.MaxList)
		}
		f.doc("", fmt.Sprintf("%s is the edge %s: the %s nodes it leads to, in key order. It is read with the edge of every row read with x, in one query, which reads at most %s rows for each, whatever the DB's WithMaxRows: more fail x's with velox.ErrTooManyRows, in an error that names the edge.", ed.Method, pe.Name, tt.Name, most))
		f.p("func (x *%s) %s(ctx %s.Context) ([]*%s, error) {", e.Name, ed.Method, ctx, tgt.Name)
		query()
		f.p("\treturn %s.EdgeList(ctx, q, x, %s, %s, %d)", impl, rel, field, ed.MaxList)
		f.p("}")
	case sdl.EdgeConnection:
		f.doc("", fmt.Sprintf("%s is the edge %s: a connection to the %s nodes it leads to, paged, ordered and filtered by the arguments. Its queries run when gqlgen asks for the connection's fields, each with those of every row read with x, in one query.", ed.Method, pe.Name, tt.Name))
		f.p("func (x *%s) %s(ctx %s.Context, %s) (*%sConnection, error) {", e.Name, ed.Method, ctx, connParams(f, tt), tt.Name)
		dbFrom()
		f.p("\tc, err := %s(ctx, db.Query[%s](), %s)", connFunc(tt), tgt.Name, connArgs(tt))
		f.p("\tif err != nil {")
		f.p("\t\treturn nil, err")
		f.p("\t}")
		f.p("\treturn &%sConnection{c: %s.EdgeConnection(ctx, c.c, x, %s, %s, []any{%s})}, nil", tt.Name, impl, rel, field, connArgs(tt))
		f.p("}")
	}
	f.p("")
	return nil
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

// viaTerm is the expression of a gqlimpl.JoinedTerm of the field fd of the
// target of e's edge v.Edge, named field and in the direction dir, at indent.
func (r *renderer) viaTerm(f *file, e *plugin.Entity, v sdl.OrderValue, fd *plugin.Field, field, dir, indent string) (string, error) {
	ed := e.Edges[v.Edge-1]
	t := v.Target
	base, err := termBase(f, t, fd)
	if err != nil {
		return "", err
	}
	elem := cmp.Or(fd.ElemType, base)
	conv := elem != base
	if conv {
		if err := r.converts(t, fd, base); err != nil {
			return "", err
		}
		if elem, err = r.qualify(f, t, fd, elem, fd.PkgPath); err != nil {
			return "", err
		}
	}
	to := func(typ, x string) string {
		if conv {
			return typ + "(" + x + ")"
		}
		return x
	}
	if fd.Pointer {
		return "", fmt.Errorf("schema %s, edge %s: a connection orders by %s.%s, and its Go field is a pointer, which only a Nillable field is", e.Name, ed.Name, t.Name, fd.Name)
	}
	rel := e.Handles + "." + ed.GoName
	handle := t.Handles + "." + fd.GoName
	get := fmt.Sprintf("func(x *%s) %s { return %s }", t.Name, base, to(base, "x."+fd.GoName))
	set := fmt.Sprintf("func(x *%s, v %s) { x.%s = %s }", t.Name, base, fd.GoName, to(elem, "v"))
	return fmt.Sprintf("%s.JoinedTerm(%s, %s, %s, %s.Asc(), %s.Desc(),\n%s\t%s,\n%s\t%s)", f.gqlimpl(), field, dir, rel, handle, handle, indent, get, indent, set), nil
}
