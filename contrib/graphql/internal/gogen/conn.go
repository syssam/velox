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
// a connection with, and the unexported table its connections are made of and
// the columns its selections read.
func connFunc(t *sdl.Type) string { return plugin.GoPlural(t.Name) + "ConnectionOf" }
func connVar(t *sdl.Type) string  { return lowerFirst(t.Entity.Name) + "ConnGQL" }
func colsFunc(t *sdl.Type) string { return lowerFirst(t.Entity.Name) + "ColumnsGQL" }

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
	for _, name := range []string{n + "Connection", n + "Edge", root, connFunc(t), connVar(t), colsFunc(t)} {
		if err := r.declare(name, what); err != nil {
			return err
		}
	}
	ctx, vgql, impl, velox := f.use("context", "context"), f.veloxgql(), f.gqlimpl(), f.velox()

	f.doc("", fmt.Sprintf("%sConnection is the GraphQL type %[1]sConnection, a connection to a list of %[1]s. Its fields run their queries when gqlgen asks; see gqlimpl.Connection.", n))
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
	f.doc("", fmt.Sprintf("%sEdge is the GraphQL type %[1]sEdge, an edge in a connection to a list of %[1]s.", n))
	f.p("type %sEdge struct {", n)
	f.p("\t// Node is the %s at the end of the edge.", n)
	f.p("\tNode *%s", e.Name)
	f.p("\t// Cursor is the cursor of the edge, for paging.")
	f.p("\tCursor %s.Cursor", vgql)
	f.p("}")
	f.p("")

	f.doc("", fmt.Sprintf("%s is the connection to every %s node, on the DB in ctx, for a Query field's resolver. It is %s of the whole table.", root, n, connFunc(t)))
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
	f.doc("", fmt.Sprintf("%s is the connection to the %s nodes q returns, %s, and paged by the other arguments. q must not be ordered and must select the key and the order columns; see gqlimpl.ConnectionBase and gqlimpl.NewConnection.", connFunc(t), n, strings.Join(how, ", ")))
	f.p("func %s(ctx %s.Context, q %s.Query[%s], %s) (*%sConnection, error) {", connFunc(t), ctx, velox, e.Name, connParams(f, t), n)
	order := "nil"
	if t.Order != nil {
		order = "order"
		if t.MultiOrder {
			// NewConnection refuses more than MaxOrderTerms terms.
			f.p("\torder := make([]%s.Order, len(orderBy))", impl)
			f.p("\tfor i, o := range orderBy {")
			f.p("\t\torder[i] = %s.Order{Field: string(o.Field), Direction: o.Direction}", impl)
			f.p("\t}")
		} else {
			f.p("\tvar order []%s.Order", impl)
			f.p("\tif o := orderBy; o != nil {")
			f.p("\t\torder = []%s.Order{{Field: string(o.Field), Direction: o.Direction}}", impl)
			f.p("\t}")
		}
	}
	f.p("\tb, err := %s.ConnectionBase(ctx, %s.Untyped(q), &%s, %s)", impl, velox, connVar(t), order)
	f.p("\tif err != nil {")
	f.p("\t\treturn nil, err")
	f.p("\t}")
	if t.Where != nil {
		f.p("\tif where != nil {")
		f.p("\t\tp, err := where.PredicateGQL(ctx)")
		f.p("\t\tif err != nil {")
		f.p("\t\t\treturn nil, err")
		f.p("\t\t}")
		f.p("\t\tb = b.Where(p)")
		f.p("\t}")
	}
	f.p("\tc, err := %s.NewConnection(ctx, b, &%s, %s.Args{After: after, First: first, Before: before, Last: last}, %s)", impl, connVar(t), impl, order)
	f.p("\tif err != nil {")
	f.p("\t\treturn nil, err")
	f.p("\t}")
	f.p("\treturn &%sConnection{c: c}, nil", n)
	f.p("}")
	f.p("")

	f.doc("", fmt.Sprintf("%s is what the runtime knows of the GraphQL type %s: its table, the key and the order columns of its connections, the columns its selections read and the fields of its inputs.", connVar(t), n))
	f.p("var %s = %s.Conn{", connVar(t), impl)
	f.p("\tType:  %q,", n)
	f.p("\tTable: %s.TableOf[%s],", velox, e.Name)
	f.p("\tKey: []%s.Col{", impl)
	for _, i := range e.PrimaryKey {
		fd := e.Fields[i]
		// The key's Go type pages.
		if err := r.checkTerm(e, fd); err != nil {
			return err
		}
		f.p("\t\t{Field: %q, Col: %s},", fd.Name, colConst(e, fd))
	}
	f.p("\t},")
	if t.Order != nil {
		f.p("\tOrder: []%s.Col{", impl)
		for _, v := range t.Order {
			fd, _ := orderField(e, v)
			if v.Edge == 0 {
				if err := r.checkTerm(e, fd); err != nil {
					return err
				}
				f.p("\t\t{Field: %q, Col: %s},", v.Value, colConst(e, fd))
				continue
			}
			// The target's field pages as the node's own does: a pointer's
			// nil is NULL, which a cursor holds as null.
			if err := r.checkTerm(v.Target, fd); err != nil {
				return err
			}
			f.p("\t\t{Field: %q, Col: %s, Edge: %s + 1, Target: %s.TableOf[%s]},", v.Value, colConst(v.Target, fd), edgeConst(e, e.Edges[v.Edge-1]), velox, v.Target.Name)
		}
		f.p("\t},")
	}
	f.p("\tColumns: %s,", colsFunc(t))
	create, update := inputFuncs(t)
	if create != "" {
		f.p("\tCreateField: %s,", create)
	}
	if update != "" {
		f.p("\tUpdateField: %s,", update)
	}
	f.p("}")
	r.writeColumns(f, t)
	return nil
}

// checkTerm checks that a connection can order by e's field fd: keyset paging
// continues after a value of its column's type (termBase), and gqlimpl
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
// standard Go type of its column, which gqlimpl converts a cursor's values
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

// writeColumns writes <entity>ColumnsGQL, the columns a selection of t's
// fields reads: the key, every foreign key on the
// row (an edge, the application's With loads, and mutations' checks read
// them) and every order field's column (a cursor holds them), always; and
// each selected field's column. A field it does not know, an application's
// own, may read any column: then all of them.
func (r *renderer) writeColumns(f *file, t *sdl.Type) {
	e := t.Entity
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
		cols[k] = colConst(e, e.Fields[i])
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
	f.doc("", fmt.Sprintf("%s returns the columns a selection of the given %s fields reads. It returns nil, every column, for a field that is not velox-graphql's; see gqlimpl.Conn.", colsFunc(t), t.Name))
	f.p("func %s(fields []string) []int {", colsFunc(t))
	f.p("\tcols := []int{%s}", strings.Join(cols, ", "))
	f.p("\tfor _, f := range fields {")
	f.p("\t\tswitch f {")
	if len(none) > 0 {
		f.p("\t\tcase %s:", quoteList(none))
	}
	for _, fd := range t.Fields {
		if !in(fd.Field) {
			f.p("\t\tcase %q:", fd.Name)
			f.p("\t\t\tcols = append(cols, %s)", colConst(e, e.Fields[fd.Field]))
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
	ctx, impl := f.use("context", "context"), f.gqlimpl()
	// The owner's table, x and the relation's index: the batches are
	// compiled once, on velox.Table, and gqlimpl takes and gives the rows
	// as *E in an any.
	rel := fmt.Sprintf("&%s, x, %s, %q", connVar(t), edgeConst(e, pe), t.Name+"."+ed.Name)
	switch ed.Kind {
	case sdl.EdgeUnique:
		f.doc("", fmt.Sprintf("%s is the edge %s: the %s it leads to, or nil. It is read in one query with the edge of every row read with x.", ed.Method, pe.Name, tt.Name))
		f.p("func (x *%s) %s(ctx %s.Context) (*%s, error) {", e.Name, ed.Method, ctx, tgt.Name)
		if nilKey != "" {
			f.p("\tif %s {", nilKey)
			f.p("\t\treturn nil, nil")
			f.p("\t}")
		}
		f.p("\tv, err := %s.EdgeOne(ctx, &%s, %s)", impl, connVar(tt), rel)
		f.p("\tn, _ := v.(*%s)", tgt.Name)
		f.p("\treturn n, err")
		f.p("}")
	case sdl.EdgeList:
		f.doc("", fmt.Sprintf("%s is the edge %s: the %s nodes it leads to, in key order. It is read in one query with the edge of every row read with x, up to MaxList; see gqlimpl.EdgeList.", ed.Method, pe.Name, tt.Name))
		f.p("func (x *%s) %s(ctx %s.Context) ([]*%s, error) {", e.Name, ed.Method, ctx, tgt.Name)
		f.p("\tv, err := %s.EdgeList(ctx, &%s, %s, %d)", impl, connVar(tt), rel, ed.MaxList)
		f.p("\tn, _ := v.([]*%s)", tgt.Name)
		f.p("\treturn n, err")
		f.p("}")
	case sdl.EdgeConnection:
		f.doc("", fmt.Sprintf("%s is the edge %s: a connection to the %s nodes it leads to. Its queries batch with the edge of every row read with x; see gqlimpl.EdgeConnection.", ed.Method, pe.Name, tt.Name))
		f.p("func (x *%s) %s(ctx %s.Context, %s) (*%sConnection, error) {", e.Name, ed.Method, ctx, connParams(f, tt), tt.Name)
		f.p("\tc, err := %sConnection(ctx, %s)", plugin.GoPlural(tt.Name), connArgs(tt))
		f.p("\tif err != nil {")
		f.p("\t\treturn nil, err")
		f.p("\t}")
		f.p("\treturn &%sConnection{c: %s.EdgeConnection(ctx, c.c, %s, []any{%s})}, nil", tt.Name, impl, rel, connArgs(tt))
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
