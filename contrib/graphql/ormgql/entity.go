package ormgql

import (
	"bytes"
	"fmt"

	"github.com/syssam/velox/internal/ormgen"
)

// gqlFile is the file of e's GraphQL layer in package entity. The name ends
// in _gql whatever the entity is called: gql_test.go and gql_linux.go would
// be left out of the build by their names.
func gqlFile(e *ormgen.Entity) string { return ormgen.FileName(e) + "_gql" }

// entities generates the files of the layer in package entity: one per
// entity that has something of it.
func (ext *extension) entities() (map[string][]byte, error) {
	g := ext.g
	im := g.Imports()
	im.Use(ormgen.ORMPath, "orm")
	veloxPkg := im.Use(ormgen.VeloxPath, "velox")
	im.Use(gqlinputPath, "gqlinput")
	im.Use("context", "context")
	im.Use(g.Package()+"/predicate", "predicate")
	var b bytes.Buffer
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	for _, e := range g.Entities() {
		q := ext.ents[e]
		if q == nil {
			continue
		}
		start := b.Len()
		w("\n%s%s\n\n", ormgen.FileMarker, gqlFile(e))
		marked := b.Len()
		ext.entity(w, e, q, im, veloxPkg)
		if b.Len() == marked {
			b.Truncate(start) // in the schema, and nothing of it is here
		}
	}
	if b.Len() == 0 {
		return nil, nil
	}
	var out bytes.Buffer
	out.WriteString(ormgen.Header)
	out.WriteString("package entity\n\n")
	out.WriteString(im.Block())
	out.WriteString("\n")
	// As the rest of the package: see ormgen.NoInline.
	out.Write(ormgen.NoInline(b.Bytes()))
	// Every declaration is in the file of its entity; the common file of
	// the split is empty.
	const common = "gql"
	files, err := ormgen.Split(out.Bytes(), common)
	delete(files, common+".go")
	return files, err
}

// entity writes what package entity holds of an entity's GraphQL layer:
// its order input, and a method per edge the schema exposes, by which a
// GraphQL engine resolves the edge (it binds a field to the method of the
// same name). The methods take what they run on from the context
// (velox.WithExecutor): an entity holds no connection, and an engine passes
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
func (ext *extension) entity(w func(string, ...any), e *ormgen.Entity, q *gqlEnt, im *ormgen.Imports, veloxPkg string) {
	n := ext.g.Names(e)
	if q.Node {
		w("// IsNode marks %[1]s as a node of the GraphQL schema.\nfunc (*%[1]s) IsNode() {}\n\n", e.Name)
	}
	if q.HasOrder {
		// The fields the list can be ordered by are in the entity's own
		// package: see gqlOrder.
		leaf := im.Use(ext.g.Package()+"/"+e.Pkg, ormgen.LocalName(e.Pkg))
		w("// %[2]sOrderColumns maps each field a list of %[1]s can be ordered by, as the\n// schema names it (the constants of %[3]s.%[4]s), to its column.\nvar %[2]sOrderColumns = map[string]string{\n", e.Name, lowerFirst(e.Name), leaf, q.OrderType)
		for _, o := range q.Order {
			w("\t%q: %q,\n", o.Value, o.Column)
		}
		w(`}

// %[1]sOrder is the ordering a request asks for: the orderBy argument of a
// GraphQL connection. Without a Field it orders by the id.
type %[1]sOrder struct {
	Direction gqlinput.OrderDirection `+"`json:\"direction\"`"+`
	Field     %[5]s.%[6]s    `+"`json:\"field\"`"+`
}

// Term returns the order term of the input, for OrderBy; the term of a nil
// input orders by nothing.
func (o *%[1]sOrder) Term() %[2]s {
	if o == nil {
		return %[2]s{}
	}
	return %[2]s(orm.OrderTerm(&%[3]s, %[4]sOrderColumns, string(o.Field), o.Direction == gqlinput.OrderDirectionDesc))
}

`, e.Name, e.OrderTerm(), n.Meta, lowerFirst(e.Name), leaf, q.OrderType)
	}
	for _, x := range q.Edges {
		t := x.X.Target
		switch x.Kind {
		case gqlOne:
			w(`// %[1]s resolves the %[2]q edge for a GraphQL engine: the neighbor the query
// loaded, or else a query on the handle of the context (velox.WithExecutor).
// An edge without a neighbor is nil, not an error.
func (e *%[3]s) %[1]s(ctx context.Context) (*%[4]s, error) {
	v, err := e.Edges.%[5]sOrErr()
	if %[6]s.IsNotLoaded(err) {
		v, err = e.Query%[5]s(velox.ExecutorFrom(ctx)).Only(ctx)
	}
	return v, %[6]s.MaskNotFound(err)
}

`, x.Method, x.X.E.Name, e.Name, t.Name, x.X.GoName, veloxPkg)
		case gqlList:
			w(`// %[1]s resolves the %[2]q edge for a GraphQL engine: the neighbors the query
// loaded, or else a query on the handle of the context (velox.WithExecutor).
func (e *%[3]s) %[1]s(ctx context.Context) ([]*%[4]s, error) {
	if v, err := e.Edges.%[5]sOrErr(); !%[6]s.IsNotLoaded(err) {
		return v, err
	}
	return e.Query%[5]s(velox.ExecutorFrom(ctx)).All(ctx)
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
				// type: see the note on entity.
				params += ", where interface{ P() " + t.Predicate() + " }"
				doc = fmt.Sprintf("\n// where is a *filter.%sWhereInput, or nil.", t.Name)
			}
			w(`// %[1]s resolves the %[2]q edge for a GraphQL engine: one page of the
// neighbors, queried on the handle of the context (velox.WithExecutor).%[8]s
func (e *%[3]s) %[1]s(ctx context.Context, after *velox.Cursor, first *int, before *velox.Cursor, last *int%[6]s) (*%[4]sConnection, error) {
	q := e.Query%[5]s(velox.ExecutorFrom(ctx))%[7]s
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
