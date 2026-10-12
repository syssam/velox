package ormgen

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	vgen "github.com/syssam/velox/compiler/gen"
)

// names of the per-entity types in package entity.
type names struct {
	Meta, Edges                               string
	Query, Create, CreateBulk, Update, Delete string
	NewQuery, NewCreate, NewCreateBulk        string
	NewUpdate, NewDelete, ptr, edge, link     string
}

func (g *generator) names(e *ent) names { return namesOf(e.Name) }

// reservedNames are the identifiers of package entity an entity named n
// always gets.
func reservedNames(n string) []string {
	x := namesOf(n)
	return []string{x.Meta, x.Edges, n + "Edge", n + "Connection", x.Query, x.Create, x.CreateBulk, x.Update, x.Delete,
		x.NewQuery, x.NewCreate, x.NewCreateBulk, x.NewUpdate, x.NewDelete}
}

func namesOf(n string) names {
	return names{
		Meta: n + "Meta", Edges: n + "Edges",
		Query: n + "Query", Create: n + "Create", CreateBulk: n + "CreateBulk", Update: n + "Update", Delete: n + "Delete",
		NewQuery: "New" + n + "Query", NewCreate: "New" + n + "Create", NewCreateBulk: "New" + n + "CreateBulk",
		NewUpdate: "New" + n + "Update", NewDelete: "New" + n + "Delete",
		ptr: "ptr" + n, edge: "edge" + n, link: "link" + n,
	}
}

func relConst(r vgen.Rel) string {
	switch r {
	case vgen.O2O:
		return "orm.O2O"
	case vgen.O2M:
		return "orm.O2M"
	case vgen.M2O:
		return "orm.M2O"
	default:
		return "orm.M2M"
	}
}

func (g *generator) model() (map[string][]byte, error) {
	im := newImports()
	im.use(ormPath, "orm")
	// The errors an edge accessor returns are package velox's.
	veloxPkg := im.use("github.com/syssam/velox", "velox")
	im.use("context", "context")
	im.use("slices", "slices")
	im.use("iter", "iter")
	im.use("github.com/syssam/velox/schema/field", "schemafield")
	im.use(g.cfg.Package+"/predicate", "predicate")
	slotsPkg := im.use(g.cfg.Package+"/internal/slots", "slots")
	var b bytes.Buffer
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }

	// Field types from other packages first, then the per-entity packages (the
	// enum types of the fields live there), under names that no variable of the
	// generated functions can shadow.
	locals := map[string]bool{}
	for _, s := range strings.Fields("e q b v i d s c r l p o ps os ls ss bs vs es id ids ctx ex err out rows subs dst conn where after first before last orderBy context schemafield orm slices predicate entity iter slots filter") {
		locals[s] = true
	}
	for _, e := range g.ents {
		for _, f := range e.Fields {
			ref, err := goTypeOf(f.F)
			if err != nil {
				return nil, err
			}
			if ref.PkgPath != "" && f.Enum == "" {
				if got := im.use(ref.PkgPath, ref.PkgName); got != ref.PkgName && ref.PkgName != "" {
					return nil, fmt.Errorf("velox: %s.%s: two packages named %q are used as field types", e.Name, f.F.Name, ref.PkgName)
				}
			}
		}
	}
	for _, e := range g.ents {
		name := e.Pkg
		if locals[name] {
			name += "pkg"
		}
		_, types, _, _ := g.enumNames(e)
		if q := g.gql[e]; len(types) > 0 || len(e.Edges) > 0 || (q != nil && q.HasOrder) {
			// Only the entities whose enum types, edge descriptions or
			// order fields the package entity uses are imported.
			e.alias = im.use(g.cfg.Package+"/"+e.Pkg, name)
		}
		for f, t := range types {
			f.Enum = t
			f.Base = e.alias + "." + t
		}
		for _, x := range e.Edges {
			x.Target.linked = true
		}
	}

	for _, e := range g.ents {
		n := g.names(e)
		id := e.ID
		w("\n%s%s\n\n", fileMarker, fileName(e))

		// Entity struct.
		w("// %s is the %s entity.\ntype %s struct {\n", e.Name, e.Name, e.Name)
		for _, f := range e.Fields {
			if f.Hidden {
				w("\t%s %s\n", f.GoName, f.goType())
				continue
			}
			// velox's tags: a sensitive field never reaches JSON, a tag
			// declared in the schema is used as it is, and otherwise the
			// key is the field's name.
			tag := fmt.Sprintf(`json:"%s,omitempty"`, f.JSON)
			switch {
			case f.F.Sensitive():
				tag = `json:"-"`
			case f.F.StructTag != "" && f.JSON == f.F.Name:
				tag = f.F.StructTag
			}
			w("\t%s %s `%s`\n", f.GoName, f.goType(), tag)
		}
		w("\t// Edges holds the edges loaded with the query; see the OrErr methods.\n\tEdges %s `json:\"edges\"`\n}\n\n", n.Edges)

		// Edges struct.
		w("// %s holds the loaded edges of a %s.\ntype %s struct {\n", n.Edges, e.Name, n.Edges)
		for _, x := range e.Edges {
			tag := fmt.Sprintf(`json:"%s,omitempty"`, x.E.Name)
			if x.E.StructTag != "" {
				tag = x.E.StructTag
			}
			if x.E.Unique {
				w("\t%s *%s `%s`\n", x.GoName, x.Target.Name, tag)
			} else {
				w("\t%s []*%s `%s`\n", x.GoName, x.Target.Name, tag)
			}
		}
		w("\tloaded [%d]bool\n}\n\n", max(len(e.Edges), 1))
		for _, x := range e.Edges {
			if x.E.Unique {
				w("// %[1]sOrErr returns the %[2]s edge: an error when it was not loaded (velox.IsNotLoaded), velox.ErrNotFound when it was loaded and is empty.\nfunc (e *%[3]s) %[1]sOrErr() (*%[4]s, error) {\n\tif !e.loaded[%[5]d] {\n\t\treturn nil, %[6]s.NewNotLoadedError(%[2]q)\n\t}\n\tif e.%[1]s == nil {\n\t\treturn nil, %[7]s.NotFound()\n\t}\n\treturn e.%[1]s, nil\n}\n\n",
					x.GoName, x.E.Name, n.Edges, x.Target.Name, x.Index, veloxPkg, g.names(x.Target).Meta)
			} else {
				w("// %[1]sOrErr returns the %[2]s edge, or an error when it was not loaded (velox.IsNotLoaded).\nfunc (e *%[3]s) %[1]sOrErr() ([]*%[4]s, error) {\n\tif !e.loaded[%[5]d] {\n\t\treturn nil, %[6]s.NewNotLoadedError(%[2]q)\n\t}\n\treturn e.%[1]s, nil\n}\n\n",
					x.GoName, x.E.Name, n.Edges, x.Target.Name, x.Index, veloxPkg)
			}
		}

		// The address of each column's field: the only per-field code.
		w("func %s(v any, i int) any {\n\te := v.(*%s)\n\tswitch i {\n", n.ptr, e.Name)
		for i, f := range e.Fields {
			w("\tcase %d:\n\t\treturn &e.%s\n", i, f.GoName)
		}
		w("\t}\n\treturn nil\n}\n\n")
		if len(e.Edges) > 0 {
			w("func %s(v any, i int) any {\n\te := v.(*%s)\n\te.Edges.loaded[i] = true\n\tswitch i {\n", n.edge, e.Name)
			for _, x := range e.Edges {
				w("\tcase %d:\n\t\treturn &e.Edges.%s\n", x.Index, x.GoName)
			}
			w("\t}\n\treturn nil\n}\n\n")
		}
		if e.linked {
			w("func %[1]s(dst any, es []any) {\n\tswitch d := dst.(type) {\n\tcase *[]*%[2]s:\n\t\ts := make([]*%[2]s, len(es))\n\t\tfor i, e := range es {\n\t\t\ts[i] = e.(*%[2]s)\n\t\t}\n\t\t*d = s\n\tcase **%[2]s:\n\t\t*d = nil\n\t\tif len(es) > 0 {\n\t\t\t*d = es[0].(*%[2]s)\n\t\t}\n\t}\n}\n\n", n.link, e.Name)
		}

		// One page of the entity: what Paginate returns. The shape and the
		// field names are Relay's, which is also what a GraphQL engine binds
		// the entity's connection type to.
		w(`// %[1]sEdge is a row of a page of %[1]s, with its cursor.
type %[1]sEdge struct {
	Node   *%[1]s     `+"`json:\"node\"`"+`
	Cursor orm.Cursor `+"`json:\"cursor\"`"+`
}

// %[1]sConnection is one page of %[1]s.
type %[1]sConnection struct {
	Edges    []*%[1]sEdge `+"`json:\"edges\"`"+`
	PageInfo orm.PageInfo `+"`json:\"pageInfo\"`"+`
	// TotalCount is the number of rows the query matches, or -1 when it
	// was not counted (orm.WithoutTotal).
	TotalCount int `+"`json:\"totalCount\"`"+`
}

`, e.Name)

		// Meta.
		var cols []string
		for _, f := range e.Fields {
			cols = append(cols, strconv.Quote(f.Col))
		}
		w("// %s is the runtime description of %s. Use it through its address.\nvar %s = orm.Meta{\n\tName: %q,\n\tTable: %q,\n\tColumns: []string{%s},\n\tNew: func() any { return new(%s) },\n\tPtr: %s,\n",
			n.Meta, e.Name, n.Meta, e.Name, e.T.Table(), strings.Join(cols, ", "), e.Name, n.ptr)
		if hasRuntime(e) {
			w("\tRuntime: true,\n\tPackage: %q,\n", g.cfg.Package)
		}
		if len(e.Edges) > 0 {
			w("\tEdge: %s,\n", n.edge)
		}
		if e.linked {
			w("\tLink: %s,\n", n.link)
		}
		sensitive := false
		w("\tFields: []*orm.FieldMeta{\n")
		for _, f := range e.Fields {
			if f.Hidden {
				continue
			}
			w("\t\t{Name: %q, Column: %q, Type: schemafield.%s", f.F.Name, f.Col, f.F.Type.Type.ConstName())
			if f.Ptr {
				w(", Nillable: true")
			}
			// The id of a new row is the database's to assign when it is a
			// number; any other id is the caller's or a default's.
			if f.F.Optional || (f.IsID && f.F.Type.Numeric()) {
				w(", Optional: true")
			}
			if !f.Null {
				w(", NotNull: true")
			}
			if f.F.Immutable && !f.IsID {
				w(", Immutable: true")
			}
			if f.F.Sensitive() {
				w(", Sensitive: true")
				sensitive = true
			}
			if f.F.IsEnum() {
				var vs []string
				for _, en := range f.F.Enums {
					vs = append(vs, strconv.Quote(en.Value))
				}
				w(", Enums: []string{%s}", strings.Join(vs, ", "))
			}
			w("},\n")
		}
		w("\t},\n}\n\n")
		if sensitive {
			for _, f := range e.Fields {
				if !f.Hidden && f.GoName == "String" {
					return nil, fmt.Errorf("velox: %s has a field %q and, with a sensitive field, a method String that keeps the secret out of what is printed: rename the field", e.Name, f.F.Name)
				}
			}
			w("// String renders the %[1]s without its sensitive fields, so that printing\n// one does not put a secret in a log.\nfunc (e *%[1]s) String() string { return orm.Describe(&%[2]s, e) }\n\n", e.Name, n.Meta)
		}

		// Query.
		w(`// %[1]s is the query of %[2]s.
type %[1]s struct{ c orm.QueryCore }

// %[3]s returns a query of every %[2]s.
func %[3]s(ex orm.Executor) *%[1]s { return &%[1]s{c: orm.QueryCore{M: &%[4]s, Ex: ex}} }

// Where adds predicates; all must hold.
func (q *%[1]s) Where(ps ...%[5]s) *%[1]s {
	q.c.Preds = slices.Grow(q.c.Preds, len(ps))
	for _, p := range ps {
		q.c.Preds = append(q.c.Preds, orm.P(p))
	}
	return q
}

// OrderBy adds order terms.
func (q *%[1]s) OrderBy(os ...%[6]s) *%[1]s {
	for _, o := range os {
		q.c.Orders = append(q.c.Orders, orm.O(o))
	}
	return q
}

// Limit caps the number of rows.
func (q *%[1]s) Limit(n int) *%[1]s { q.c.Limit = n; return q }

// Offset skips the first n rows.
func (q *%[1]s) Offset(n int) *%[1]s { q.c.Offset = n; return q }

// Distinct removes duplicate rows.
func (q *%[1]s) Distinct() *%[1]s { q.c.Distinct = true; return q }

// Columns reads only the named columns into the entities, for a list that
// shows a few columns of a wide table; every other field of an entity is
// left at its zero value, so such an entity is for reading, not for saving
// back. The id is always read, and so is what the query needs itself: the
// columns it is ordered by and the keys of the edges it loads.
func (q *%[1]s) Columns(columns ...string) *%[1]s { q.c.Columns(columns...); return q }

// ForUpdate locks the rows read against concurrent changes until the
// transaction ends. It needs a transaction to mean anything.
func (q *%[1]s) ForUpdate(opts ...orm.LockOption) *%[1]s { q.c.SetLock(orm.LockUpdate, opts); return q }

// ForShare locks the rows read against concurrent changes, and lets others
// read them, until the transaction ends.
func (q *%[1]s) ForShare(opts ...orm.LockOption) *%[1]s { q.c.SetLock(orm.LockShare, opts); return q }

// Select reads the given columns of the matching rows, and aggregates over
// them, instead of entities: Select().GroupBy("status").Aggregate(orm.Count()).Scan(ctx, &rows).
func (q *%[1]s) Select(columns ...string) *orm.Selection { return q.c.Select(columns...) }

// Clone returns an independent copy of the query.
func (q *%[1]s) Clone() *%[1]s {
	c := *q
	c.c.Preds = append([]orm.P(nil), q.c.Preds...)
	c.c.Orders = append([]orm.O(nil), q.c.Orders...)
	c.c.With = append([]*orm.EdgeLoad(nil), q.c.With...)
	return &c
}

// All returns every matching %[2]s.
func (q *%[1]s) All(ctx context.Context) ([]*%[2]s, error) {
	rows, err := q.c.All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*%[2]s, len(rows))
	for i, r := range rows {
		out[i] = r.(*%[2]s)
	}
	return out, nil
}

// First returns the first matching %[2]s, or velox.ErrNotFound.
func (q *%[1]s) First(ctx context.Context) (*%[2]s, error) {
	r, err := q.c.First(ctx)
	if err != nil {
		return nil, err
	}
	return r.(*%[2]s), nil
}

// Only returns the single matching %[2]s: velox.ErrNotFound for none, velox.ErrNotSingular for more.
func (q *%[1]s) Only(ctx context.Context) (*%[2]s, error) {
	r, err := q.c.Only(ctx)
	if err != nil {
		return nil, err
	}
	return r.(*%[2]s), nil
}

// Iter returns the matching rows one at a time, without loading them all:
//
//	for u, err := range q.Iter(ctx) { ... }
//
// Every loop over the result runs the query again. A failure is delivered as
// the last pair. The rows stay open during the loop, so on a transaction
// nothing else can run until it ends; eager loads are refused.
func (q *%[1]s) Iter(ctx context.Context) iter.Seq2[*%[2]s, error] {
	return func(yield func(*%[2]s, error) bool) {
		err := q.c.Each(ctx, func(e any) bool { return yield(e.(*%[2]s), nil) })
		if err != nil {
			yield(nil, err)
		}
	}
}

// Paginate returns one page of the matching rows, in the query's order, with
// a cursor per row: the first rows after the cursor after, or the last rows
// before the cursor before. It is keyset pagination in the shape of a Relay
// connection, for any kind of list endpoint.
func (q *%[1]s) Paginate(ctx context.Context, after *orm.Cursor, first *int, before *orm.Cursor, last *int) (*%[2]sConnection, error) {
	p, err := q.c.Paginate(ctx, after, first, before, last)
	if err != nil {
		return nil, err
	}
	conn := &%[2]sConnection{Edges: make([]*%[2]sEdge, len(p.Rows)), PageInfo: p.Info, TotalCount: p.Total}
	for i, r := range p.Rows {
		conn.Edges[i] = &%[2]sEdge{Node: r.(*%[2]s), Cursor: p.Cursors[i]}
	}
	return conn, nil
}

// Count returns the number of matching rows.
func (q *%[1]s) Count(ctx context.Context) (int, error) { return q.c.Count(ctx) }

// Exists reports whether any row matches.
func (q *%[1]s) Exists(ctx context.Context) (bool, error) { return q.c.Exists(ctx) }

// IDs returns the ids of the matching rows.
func (q *%[1]s) IDs(ctx context.Context) ([]%[8]s, error) {
	vs, err := q.c.IDs(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]%[8]s, len(vs))
	for i, v := range vs {
		ids[i] = v.(%[8]s)
	}
	return ids, nil
}

`, n.Query, e.Name, n.NewQuery, n.Meta, e.pred(), e.order(), "", id.Base)

		// Mutations.
		w(`// %[1]s creates one %[2]s.
type %[1]s struct{ c orm.MutCore }

// %[3]s returns a builder that creates a %[2]s.
func %[3]s(ex orm.Executor) *%[1]s { return &%[1]s{c: orm.MutCore{M: &%[4]s, Ex: ex}} }

// Set adds field values and edges of the new row.
func (b *%[1]s) Set(ss ...%[5]s) *%[1]s {
	b.c.Sets = slices.Grow(b.c.Sets, len(ss))
	for _, s := range ss {
		b.c.Sets = append(b.c.Sets, orm.S(s))
	}
	return b
}

// Fill takes what in gives: a struct whose fields are named like those of
// %[2]s, such as the input of an endpoint. A nil pointer gives nothing, an
// optional (a type with ValueOK) gives nothing when absent, anything else is
// written. A field that names nothing of %[2]s is an error unless listed in
// skip. A later Set wins.
func (b *%[1]s) Fill(in any, skip ...string) *%[1]s {
	b.c.Fill(in, false, skip)
	return b
}

// Save inserts the row and returns it.
func (b *%[1]s) Save(ctx context.Context) (*%[2]s, error) {
	e, err := b.c.Insert(ctx)
	if err != nil {
		return nil, err
	}
	return e.(*%[2]s), nil
}

// Exec inserts the row.
func (b *%[1]s) Exec(ctx context.Context) error {
	_, err := b.c.Insert(ctx)
	return err
}

// %[6]s creates several %[2]s rows with one statement.
type %[6]s struct {
	ex orm.Executor
	bs []*%[1]s
	n  int
}

// %[7]s returns a builder that creates one row per given builder, as each
// is when the rows are inserted: what is set on one after this call is part
// of its row.
func %[7]s(ex orm.Executor, bs ...*%[1]s) *%[6]s {
	return &%[6]s{ex: ex, bs: bs}
}

func (b *%[6]s) sets() [][]orm.S {
	rows := make([][]orm.S, len(b.bs))
	for i, c := range b.bs {
		rows[i] = c.c.Sets
	}
	return rows
}

// BatchSize makes Save and Exec insert the rows n at a time, with a statement
// for every n of them where they would use one for all: a statement has a
// limit on its parameters, and a row takes one for each of its columns. The
// statements are one transaction only when the handle is one. On a database
// handle, one that fails leaves the rows of those before it in the table, and
// Save returns them with the error.
func (b *%[6]s) BatchSize(n int) *%[6]s {
	b.n = n
	return b
}

// Save inserts the rows and returns them in order.
func (b *%[6]s) Save(ctx context.Context) ([]*%[2]s, error) {
	rows, err := orm.InsertBulk(ctx, b.ex, &%[4]s, b.sets(), b.n)
	if err != nil && len(rows) == 0 {
		return nil, err
	}
	out := make([]*%[2]s, len(rows))
	for i, r := range rows {
		out[i] = r.(*%[2]s)
	}
	return out, err
}

// Exec inserts the rows.
func (b *%[6]s) Exec(ctx context.Context) error {
	_, err := orm.InsertBulk(ctx, b.ex, &%[4]s, b.sets(), b.n)
	return err
}

// %[8]s updates %[2]s rows.
type %[8]s struct{ c orm.MutCore }

// %[9]s returns a builder that updates the rows its predicates match.
func %[9]s(ex orm.Executor) *%[8]s { return &%[8]s{c: orm.MutCore{M: &%[4]s, Ex: ex}} }

// Set adds assignments and edge changes.
func (b *%[8]s) Set(ss ...%[5]s) *%[8]s {
	b.c.Sets = slices.Grow(b.c.Sets, len(ss))
	for _, s := range ss {
		b.c.Sets = append(b.c.Sets, orm.S(s))
	}
	return b
}

// Where restricts the rows updated; all predicates must hold.
func (b *%[8]s) Where(ps ...%[10]s) *%[8]s {
	b.c.Preds = slices.Grow(b.c.Preds, len(ps))
	for _, p := range ps {
		b.c.Preds = append(b.c.Preds, orm.P(p))
	}
	return b
}

// Fill takes what in gives, as on a create; here an optional that is present
// and null, and a true Clear<Name> field, set a column that can be NULL to
// NULL. What in does not give is left alone.
func (b *%[8]s) Fill(in any, skip ...string) *%[8]s {
	b.c.Fill(in, true, skip)
	return b
}

// Exec updates the matching rows and returns how many: with nothing to set,
// how many match.
func (b *%[8]s) Exec(ctx context.Context) (int, error) { return b.c.Update(ctx) }

// ExecOne updates the single matching row: velox.ErrNotFound when no row
// matches, velox.ErrNotSingular (and nothing written) when several do. It is
// SaveOne for a caller that has no use for the row.
func (b *%[8]s) ExecOne(ctx context.Context) error { return b.c.ExecOne(ctx) }

// SaveOne updates the single matching row and returns it: velox.ErrNotFound
// when no row matches, velox.ErrNotSingular (and nothing written) when several
// do. Updating any number of rows is Exec.
func (b *%[8]s) SaveOne(ctx context.Context) (*%[2]s, error) {
	e, err := b.c.UpdateOne(ctx)
	if err != nil {
		return nil, err
	}
	return e.(*%[2]s), nil
}

// %[11]s deletes %[2]s rows.
type %[11]s struct{ c orm.MutCore }

// %[12]s returns a builder that deletes the rows its predicates match.
func %[12]s(ex orm.Executor) *%[11]s { return &%[11]s{c: orm.MutCore{M: &%[4]s, Ex: ex}} }

// Where restricts the rows deleted; all predicates must hold.
func (b *%[11]s) Where(ps ...%[10]s) *%[11]s {
	b.c.Preds = slices.Grow(b.c.Preds, len(ps))
	for _, p := range ps {
		b.c.Preds = append(b.c.Preds, orm.P(p))
	}
	return b
}

// Exec deletes every matching row and returns how many.
func (b *%[11]s) Exec(ctx context.Context) (int, error) { return b.c.Delete(ctx) }

`, n.Create, e.Name, n.NewCreate, n.Meta, e.set(), n.CreateBulk, n.NewCreateBulk, n.Update, n.NewUpdate, e.pred(), n.Delete, n.NewDelete)
		// The edges of the entity: u.QueryPosts(ex) on the entity, WithPosts
		// on its query.
		//
		// Both name the query builder of another entity, and a Go package
		// compiles against everything the types it names expose, so a
		// package that names one entity reads the query builder of every
		// entity. Keeping the builders out of reach (the neighbors queried
		// from the root package, db.User.QueryPosts(ex, u), and WithPosts
		// taking an interface) made the 312 services of the benchmark compile
		// a quarter faster, 110-114 CPU-s against 140-154. It was given up
		// for the spelling: one way to query an edge, the one that reads
		// best. What it costs is in proportion to the methods of a query
		// type, so a method added there is paid for by every package.
		for _, x := range e.Edges {
			t := g.names(x.Target)
			w(`// Query%[1]s queries the %[2]q neighbors of e, on ex.
func (e *%[3]s) Query%[1]s(ex orm.Executor) *%[4]s {
	q := %[5]s(ex)
	q.c.Preds = append(q.c.Preds, orm.NeighborOf(%[6]s.Edges[%[7]d], e))
	return q
}

// With%[1]s eager-loads the %[2]q edge: one more query, whatever the number
// of rows. A query of %[8]s given to it describes the load: its conditions,
// order, nested loads and its limit, which applies per %[3]s.
func (q *%[9]s) With%[1]s(opts ...*%[4]s) *%[9]s {
	var o *orm.QueryCore
	if len(opts) == 1 && opts[0] != nil {
		o = &opts[0].c
	}
	q.c.Load(%[6]s.Edges[%[7]d], o, len(opts))
	return q
}

`, x.GoName, x.E.Name, e.Name, t.Query, t.NewQuery, n.Meta, x.Index, x.Target.Name, n.Query)
		}

		if q := g.gql[e]; q != nil {
			g.gqlModel(w, e, q, veloxPkg)
		}

		// An edge is described by data in its owner's package, which cannot
		// name package entity; package entity hands it the Metas it connects.
		if hasRuntime(e) {
			w("func init() { %s.%s.Bind(&%s) }\n\n", slotsPkg, e.Name, n.Meta)
		}
		if len(e.Edges) > 0 {
			w("func init() {\n\t%s.Edges = %s.EdgeMetas()\n", n.Meta, e.alias)
			for _, x := range e.Edges {
				w("\t%[1]s.Edges[%[2]d].Owner, %[1]s.Edges[%[2]d].Target = &%[1]s, &%[3]s\n", n.Meta, x.Index, g.names(x.Target).Meta)
				if th := x.E.Through; th != nil && g.by[th.Name] != nil {
					w("\t%s.Edges[%d].Through = &%s\n", n.Meta, x.Index, g.names(g.by[th.Name]).Meta)
				}
			}
			w("}\n\n")
		}
	}

	var out bytes.Buffer
	out.WriteString(header)
	out.WriteString("// Package entity holds every entity, its typed builders and its runtime description:\n// a file per entity.\npackage entity\n\n")
	out.WriteString(im.block())
	out.WriteString("\n")
	out.Write(noInline(b.Bytes()))
	return split(out.Bytes(), "entity")
}

// noInline marks every function of the generated entity package
// //go:noinline.
//
// The compiler writes the body of each function it could inline into the
// package's export data, and every package that calls one reads it back and
// compiles it again at the call site. Package entity is thousands of
// three-line wrappers around the runtime, called from every package of the
// application: withholding their bodies took 17% off the compile time of 312
// services on a 454-entity schema (100.6 to 83.5 CPU-s) and 14% off this
// package itself, for one more call per builder method at run time.
func noInline(src []byte) []byte {
	var out bytes.Buffer
	for _, line := range bytes.SplitAfter(src, []byte("\n")) {
		// Constructors stay inlinable: inlined, the builder they return
		// need not be allocated on the heap.
		if bytes.HasPrefix(line, []byte("func ")) && !bytes.HasPrefix(line, []byte("func init(")) && !bytes.HasPrefix(line, []byte("func New")) {
			out.WriteString("//go:noinline\n")
		}
		out.Write(line)
	}
	return out.Bytes()
}

// gqlModel writes what package entity holds of an entity's GraphQL layer:
// its order input, and a method per edge the schema exposes, by which a
// GraphQL engine resolves the edge (it binds a field to the method of the
// same name). The methods take what they run on from the context
// (orm.WithExecutor): an entity holds no connection, and an engine passes
// only the arguments of the field.
//
// An edge that takes a filter declares it as what it needs of one,
// where interface{ P() predicate.Post }, and not as *filter.PostWhereInput,
// which is what an engine passes. Package entity must not import package
// filter: a package compiles against every type in the signatures of the
// methods of every type it can reach, the entities reach each other, and so
// with the where input in a signature every package that returns an entity
// reads every where input in full. On a 454-entity schema (66,495 fields of
// where inputs) that was 0.07 of the 0.31 CPU-seconds such a package took.
// The predicate type in the interface keeps the check: a filter of another
// entity does not compile.
func (g *generator) gqlModel(w func(string, ...any), e *ent, q *gqlEnt, veloxPkg string) {
	n := g.names(e)
	if q.Node {
		w("// IsNode marks %[1]s as a node of the GraphQL schema.\nfunc (*%[1]s) IsNode() {}\n\n", e.Name)
	}
	if q.HasOrder {
		w("// %[2]sOrderColumns maps each field a list of %[1]s can be ordered by, as the\n// schema names it (the constants of %[3]s.%[4]s), to its column.\nvar %[2]sOrderColumns = map[string]string{\n", e.Name, lowerFirst(e.Name), e.alias, q.OrderType)
		for _, o := range q.Order {
			w("\t%q: %q,\n", o.Value, o.Column)
		}
		w(`}

// %[1]sOrder is the ordering a request asks for: the orderBy argument of a
// GraphQL connection. Without a Field it orders by the id.
type %[1]sOrder struct {
	Direction orm.OrderDirection `+"`json:\"direction\"`"+`
	Field     %[5]s.%[6]s    `+"`json:\"field\"`"+`
}

// Term returns the order term of the input, for OrderBy; the term of a nil
// input orders by nothing.
func (o *%[1]sOrder) Term() %[2]s {
	if o == nil {
		return %[2]s{}
	}
	return %[2]s(orm.OrderTerm(&%[3]s, %[4]sOrderColumns, string(o.Field), o.Direction))
}

`, e.Name, e.order(), n.Meta, lowerFirst(e.Name), e.alias, q.OrderType)
	}
	for _, x := range q.Edges {
		t := x.X.Target
		switch x.Kind {
		case gqlOne:
			w(`// %[1]s resolves the %[2]q edge for a GraphQL engine: the neighbor the query
// loaded, or else a query on the handle of the context (orm.WithExecutor).
// An edge without a neighbor is nil, not an error.
func (e *%[3]s) %[1]s(ctx context.Context) (*%[4]s, error) {
	v, err := e.Edges.%[5]sOrErr()
	if %[6]s.IsNotLoaded(err) {
		v, err = e.Query%[5]s(orm.ExecutorFrom(ctx)).Only(ctx)
	}
	return v, %[6]s.MaskNotFound(err)
}

`, x.Method, x.X.E.Name, e.Name, t.Name, x.X.GoName, veloxPkg)
		case gqlList:
			w(`// %[1]s resolves the %[2]q edge for a GraphQL engine: the neighbors the query
// loaded, or else a query on the handle of the context (orm.WithExecutor).
func (e *%[3]s) %[1]s(ctx context.Context) ([]*%[4]s, error) {
	if v, err := e.Edges.%[5]sOrErr(); !%[6]s.IsNotLoaded(err) {
		return v, err
	}
	return e.Query%[5]s(orm.ExecutorFrom(ctx)).All(ctx)
}

`, x.Method, x.X.E.Name, e.Name, t.Name, x.X.GoName, veloxPkg)
		case gqlConnection:
			params, chain := "", ""
			switch x.Order {
			case 1:
				params += ", orderBy *" + t.Name + "Order"
				chain += ".OrderBy(orderBy.Term())"
			case 2:
				params += ", orderBy []*" + t.Name + "Order"
			}
			doc := ""
			if x.Where {
				// What the method needs of the where input, and not its
				// type: see the note on gqlModel.
				params += ", where interface{ P() " + t.pred() + " }"
				doc = fmt.Sprintf("\n// where is a *filter.%sWhereInput, or nil.", t.Name)
			}
			w(`// %[1]s resolves the %[2]q edge for a GraphQL engine: one page of the
// neighbors, queried on the handle of the context (orm.WithExecutor).%[8]s
func (e *%[3]s) %[1]s(ctx context.Context, after *orm.Cursor, first *int, before *orm.Cursor, last *int%[6]s) (*%[4]sConnection, error) {
	q := e.Query%[5]s(orm.ExecutorFrom(ctx))%[7]s
`, x.Method, x.X.E.Name, e.Name, t.Name, x.X.GoName, params, chain, doc)
			if x.Where {
				w("\tif where != nil {\n\t\tq.Where(where.P())\n\t}\n")
			}
			if x.Order == 2 {
				w("\tfor _, o := range orderBy {\n\t\tq.OrderBy(o.Term())\n\t}\n")
			}
			w("\treturn q.Paginate(ctx, after, first, before, last)\n}\n\n")
		}
	}
}
