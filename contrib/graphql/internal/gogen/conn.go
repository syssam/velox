package gogen

import (
	"cmp"
	"fmt"
	"strings"

	"github.com/syssam/velox2/contrib/graphql/internal/sdl"
	"github.com/syssam/velox2/plugin"
)

// lowerFirst is name with its first letter in lower case, as velox-gen names
// an entity's unexported functions (userDesc).
func lowerFirst(name string) string {
	return strings.ToLower(name[:1]) + name[1:]
}

// The unexported functions of t's connections.
func connFunc(t *sdl.Type) string { return lowerFirst(t.Entity.Name) + "ConnectionGQL" }
func keyFunc(t *sdl.Type) string  { return lowerFirst(t.Entity.Name) + "KeyGQL" }
func termFunc(t *sdl.Type) string { return lowerFirst(t.Entity.Name) + "TermGQL" }

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
	root := plugin.Plural(n) + "Connection"
	for _, name := range []string{n + "Connection", n + "Edge", root, connFunc(t), keyFunc(t)} {
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
	f.p("\tedges := make([]*%sEdge, len(p.Rows))", n)
	f.p("\tfor i, x := range p.Rows {")
	f.p("\t\tedges[i] = &%sEdge{Node: x, Cursor: p.Cursors[i]}", n)
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

	f.doc("", fmt.Sprintf("%s is the connection to every %s node, through the DB veloxgql.DBFrom reads from ctx, paged and ordered by the arguments of a connection to %[2]s: a Query field's resolver returns it. It fails before any query runs on arguments gqlimpl.NewConnection refuses.", root, n))
	f.p("func %s(ctx %s.Context, %s) (*%sConnection, error) {", root, ctx, connParams(f, t), n)
	f.p("\tdb, err := %s.DBFrom(ctx)", vgql)
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
	f.doc("", fmt.Sprintf("%s is the connection to the %s nodes q returns, %s, and paged by the other arguments. It runs no query.", connFunc(t), n, strings.Join(how, ", ")))
	f.p("func %s(ctx %s.Context, q %s.Query[%s], %s) (*%sConnection, error) {", connFunc(t), ctx, velox, e.Name, connParams(f, t), n)
	if t.Where != nil {
		f.p("\tif where != nil {")
		f.p("\t\tp, err := where.Pred(ctx, 0)")
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
			f.p("\tif most := %s.LimitsFrom(ctx).MaxOrderTerms; len(orderBy) > most {", vgql)
			f.p("\t\treturn nil, %s.Errorf(\"veloxgql: %s connection: %%w: orderBy has %%d terms, and the most is %%d\", %s.ErrBadPagination, len(orderBy), most)", f.use("fmt", "fmt"), n, vgql)
			f.p("\t}")
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
		fd := e.Fields[v.Field]
		term, err := r.term(f, e, fd, "string(o.Field)", "o.Direction", "\t\t")
		if err != nil {
			return err
		}
		f.p("\tcase %sOrderField%s:", n, fd.GoName)
		f.p("\t\treturn %s, nil", term)
	}
	f.p("\t}")
	f.p("\treturn %s.OrderTerm[%s]{}, %s.Errorf(\"veloxgql: %sOrder: %%q is not a value of %sOrderField\", o.Field)", impl, e.Name, f.use("fmt", "fmt"), n, n)
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
		// Only a Nillable field is a pointer, and its column is nullable, on
		// which sdl refuses OrderField; a key is never Nillable. A model that
		// says otherwise is not velox-gen's.
		return "", fmt.Errorf("schema %s, field %s: a connection orders by it, and its Go field is a pointer, which only a Nillable field is", e.Name, fd.Name)
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
	return "", fmt.Errorf("schema %s, field %s: a connection orders by it, and keyset paging cannot continue after a %s value", e.Name, fd.Name, fd.Type)
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
		return fmt.Errorf("schema %s, field %s: velox-graphql converts its Go type %s to %s, and a %s does not convert; give the field velox's standard Go type, or one whose underlying type is it", e.Name, fd.Name, fd.ElemType, base, fd.Kind)
	}
	return nil
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

// edgeFilter is how the edge's nodes are found: cond, a condition on the
// target's query, Go code of x, the owner; and nilKey, when the owner holds
// the key in a pointer, the condition under which the edge has no node.
type edgeFilter struct {
	cond, nilKey string
}

// filter is the edgeFilter of ed, an edge of e to the entity tgt:
//   - A foreign key on the owner's row (OwnsKey): the target's key is it.
//   - A foreign key on the target's rows: it is the owner's key.
//   - Many-to-many: the target's inverse edge (the edge Ref names) has the
//     owner, through Rel.HasWhere.
//
// It fails when the key it compares, the target's, the owner's or both, is
// not one column.
func (r *renderer) filter(e *plugin.Entity, ed *plugin.Edge, tgt *plugin.Entity) (edgeFilter, error) {
	where := "schema " + e.Name + ", edge " + ed.Name
	// key is the one column of of's key, which the filter compares.
	key := func(of *plugin.Entity) (*plugin.Field, error) {
		if len(of.PrimaryKey) != 1 {
			return nil, fmt.Errorf("%s: velox-graphql finds its nodes by the key of %s, which is %d columns; skip the edge with graphql.Skip()", where, of.Name, len(of.PrimaryKey))
		}
		return of.Fields[of.PrimaryKey[0]], nil
	}
	field := func(of *plugin.Entity, name string) (*plugin.Field, error) {
		for _, fd := range of.Fields {
			if fd.Name == name {
				return fd, nil
			}
		}
		return nil, fmt.Errorf("%s: its foreign key %s is no field of %s", where, name, of.Name)
	}
	switch {
	case ed.Field != "" && ed.OwnsKey:
		fk, err := field(e, ed.Field)
		if err != nil {
			return edgeFilter{}, err
		}
		tk, err := key(tgt)
		if err != nil {
			return edgeFilter{}, err
		}
		if fk.Pointer {
			return edgeFilter{cond: fmt.Sprintf("%s.%s.EQ(*x.%s)", tgt.Handles, tk.GoName, fk.GoName), nilKey: "x." + fk.GoName + " == nil"}, nil
		}
		return edgeFilter{cond: fmt.Sprintf("%s.%s.EQ(x.%s)", tgt.Handles, tk.GoName, fk.GoName)}, nil
	case ed.Field != "":
		fk, err := field(tgt, ed.Field)
		if err != nil {
			return edgeFilter{}, err
		}
		ok, err := key(e)
		if err != nil {
			return edgeFilter{}, err
		}
		return edgeFilter{cond: fmt.Sprintf("%s.%s.EQ(x.%s)", tgt.Handles, fk.GoName, ok.GoName)}, nil
	}
	for _, inv := range tgt.Edges {
		if ed.Ref != "" && inv.Name == ed.Ref && inv.Target == e.Name {
			ok, err := key(e)
			if err != nil {
				return edgeFilter{}, err
			}
			if _, err := key(tgt); err != nil {
				return edgeFilter{}, err
			}
			return edgeFilter{cond: fmt.Sprintf("%s.%s.HasWhere(%s.%s.EQ(x.%s))", tgt.Handles, inv.GoName, e.Handles, ok.GoName, ok.GoName)}, nil
		}
	}
	return edgeFilter{}, fmt.Errorf("%s: velox-graphql finds the nodes of a many-to-many edge through its inverse edge, and %s has none; declare one with Ref(%q), or skip the edge with graphql.Skip()", where, tgt.Name, ed.Name)
}

// writeEdge writes the method of the edge field ed of t: the edge's node or
// nodes, from the relation's field when a With loaded it, else with one
// query on the target, filtered by the relation, through the DB
// veloxgql.DBFrom reads from ctx.
func (r *renderer) writeEdge(f *file, t *sdl.Type, ed sdl.Edge) error {
	e, pe := t.Entity, t.Entity.Edges[ed.Edge]
	tt, tgt := ed.Target, ed.Target.Entity
	if err := r.declare(e.Name+"."+ed.Method, "schema "+e.Name+", edge "+pe.Name); err != nil {
		return err
	}
	flt, err := r.filter(e, pe, tgt)
	if err != nil {
		return err
	}
	if flt.nilKey != "" && ed.Kind != sdl.EdgeUnique {
		return fmt.Errorf("schema %s, edge %s: a to-many edge whose key is on the owner's row, which velox-gen does not write", e.Name, pe.Name)
	}
	ctx, vgql := f.use("context", "context"), f.veloxgql()
	dbFrom := func(fail string) {
		f.p("\tdb, err := %s.DBFrom(ctx)", vgql)
		f.p("\tif err != nil {")
		f.p("\t\treturn %s, err", fail)
		f.p("\t}")
	}
	switch ed.Kind {
	case sdl.EdgeUnique:
		f.doc("", fmt.Sprintf("%s is the edge %s: the %s it leads to, or nil when there is none. It is x.%s when a With loaded it, and else one query.", ed.Method, pe.Name, tt.Name, pe.GoName))
		f.p("func (x *%s) %s(ctx %s.Context) (*%s, error) {", e.Name, ed.Method, ctx, tgt.Name)
		f.p("\tif v, ok := x.%s.Get(); ok {", pe.GoName)
		f.p("\t\treturn v, nil")
		f.p("\t}")
		if flt.nilKey != "" {
			f.p("\tif %s {", flt.nilKey)
			f.p("\t\treturn nil, nil")
			f.p("\t}")
		}
		dbFrom("nil")
		f.p("\tv, err := db.Query[%s]().Where(%s).One(ctx)", tgt.Name, flt.cond)
		f.p("\tif %s.Is(err, %s.ErrNotFound) {", f.use("errors", "errors"), f.velox())
		f.p("\t\treturn nil, nil")
		f.p("\t}")
		f.p("\treturn v, err")
		f.p("}")
	case sdl.EdgeList:
		f.doc("", fmt.Sprintf("%s is the edge %s: the %s nodes it leads to, in key order. It is x.%s when a With loaded it, and else one query, which reads at most the MaxList of veloxgql.LimitsFrom(ctx) rows, whatever the DB's WithMaxRows: more fail it with velox.ErrTooManyRows, in an error that names the edge.", ed.Method, pe.Name, tt.Name, pe.GoName))
		f.p("func (x *%s) %s(ctx %s.Context) ([]*%s, error) {", e.Name, ed.Method, ctx, tgt.Name)
		f.p("\trows, ok := x.%s.Get()", pe.GoName)
		f.p("\tif !ok {")
		f.p("\t\tdb, err := %s.DBFrom(ctx)", vgql)
		f.p("\t\tif err != nil {")
		f.p("\t\t\treturn nil, err")
		f.p("\t\t}")
		var order []string
		for _, i := range tgt.PrimaryKey {
			order = append(order, tgt.Handles+"."+tgt.Fields[i].GoName+".Asc()")
		}
		f.p("\t\tif rows, err = db.Query[%s]().Where(%s).OrderBy(%s).Unbounded().MaxRows(%s.LimitsFrom(ctx).MaxList).All(ctx); err != nil {", tgt.Name, flt.cond, strings.Join(order, ", "), vgql)
		f.p("\t\t\treturn nil, %s.Errorf(\"veloxgql: %s.%s: %%w\", err)", f.use("fmt", "fmt"), t.Name, pe.Name)
		f.p("\t\t}")
		f.p("\t}")
		f.p("\tout := make([]*%s, len(rows))", tgt.Name)
		f.p("\tfor i := range rows {")
		f.p("\t\tout[i] = &rows[i]")
		f.p("\t}")
		f.p("\treturn out, nil")
		f.p("}")
	case sdl.EdgeConnection:
		f.doc("", fmt.Sprintf("%s is the edge %s: a connection to the %s nodes it leads to, paged, ordered and filtered by the arguments. With no argument it is over x.%s when a With loaded it, in the order loaded; else its queries are on %s, filtered by the relation, and run when gqlgen asks for the connection's fields.", ed.Method, pe.Name, tt.Name, pe.GoName, tgt.Name))
		f.p("func (x *%s) %s(ctx %s.Context, %s) (*%sConnection, error) {", e.Name, ed.Method, ctx, connParams(f, tt), tt.Name)
		none := strings.ReplaceAll(connArgs(tt), ", ", " == nil && ") + " == nil"
		f.p("\tif %s {", none)
		f.p("\t\tif rows, ok := x.%s.Get(); ok {", pe.GoName)
		f.p("\t\t\treturn &%sConnection{c: %s.LoadedConnection(ctx, rows, %q, %s())}, nil", tt.Name, f.gqlimpl(), tt.Name, keyFunc(tt))
		f.p("\t\t}")
		f.p("\t}")
		dbFrom("nil")
		f.p("\treturn %s(ctx, db.Query[%s]().Where(%s), %s)", connFunc(tt), tgt.Name, flt.cond, connArgs(tt))
		f.p("}")
	}
	f.p("")
	return nil
}
