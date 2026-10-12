package ormgen

import (
	"bytes"
	"fmt"
	"sort"
)

// client generates the package at the root of the generated tree, the one
// application code starts every query and mutation from. With the code
// generated into a directory called db:
//
//	db.User.Query(conn).Where(user.Age.GT(18)).All(ctx)
//	db.User.Create(tx).Set(user.Name.Set("ann")).Save(ctx)
//	db.User.UpdateByID(tx, id).Set(user.Age.Add(1)).SaveOne(ctx)
//
// db.User is a package-level variable of a small struct type, not a field of
// a root struct. The difference is what a package that uses it pays: a root
// struct with a field per entity makes the compiler read every entity and
// builder when the type is named (0.76 CPU-s and 186 MB for a six-line
// package on a 454-entity schema), while a package-level variable is read
// only where it is used (0.40 s and 106 MB, the cost of naming one builder).
// The variable holds no state: what a query or mutation runs on, a database
// handle or a transaction, is given where it starts and travels with the
// builder, so a builder can be finished and run by another function.
func (g *generator) client() (map[string][]byte, error) {
	im := newImports()
	im.use(ormPath, "orm")
	im.use(veloxPath, "velox")
	modelPkg := im.use(g.cfg.Package+"/entity", "entity")
	im.use(g.cfg.Package+"/predicate", "predicate")
	ctxPkg := im.use("context", "context")
	// The schema's defaults, validators, hooks and policies are linked into
	// every program that uses this package, so that no program can forget
	// them. It costs the build cache nothing: an edit to the schema rebuilds
	// schemainit and this package, whose compiled form does not change, so
	// nothing that imports it is rebuilt.
	im.byPath[g.cfg.Package+"/schemainit"] = "_"
	var b bytes.Buffer
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }

	// A type per entity, named after it: UserClient for User. A schema may
	// have an entity of that very name, and then the type gives way.
	taken := map[string]bool{}
	for _, e := range g.ents {
		taken[e.Name] = true
	}
	typeOf := map[*ent]string{}
	for _, e := range g.ents {
		name := e.Name + "Client"
		for taken[name] {
			name += "Type"
		}
		taken[name] = true
		typeOf[e] = name
	}

	var nodes []*ent
	for _, e := range g.ents {
		n, t := g.names(e), typeOf[e]
		ref, err := goTypeOf(e.ID.F)
		if err != nil {
			return nil, fmt.Errorf("velox: %s: %w", e.Name, err)
		}
		if ref.PkgPath != "" {
			im.use(ref.PkgPath, ref.PkgName)
		}
		id := ref.Expr
		w("\n%s%s\n\n", fileMarker, fileName(e))
		w(`// %[2]s starts the queries and mutations of %[2]s.
var %[2]s %[1]s

// %[1]s is the type of %[2]s.
type %[1]s struct{}

// Query returns a query of every %[2]s, to run on ex: a database handle or a
// transaction.
func (*%[1]s) Query(ex velox.Executor) *%[3]s.%[4]s { return %[3]s.%[5]s(ex) }

// Create returns a builder that creates a %[2]s.
func (*%[1]s) Create(ex velox.Executor) *%[3]s.%[6]s { return %[3]s.%[7]s(ex) }

// CreateBulk returns a builder that creates one row per given builder with one statement.
func (*%[1]s) CreateBulk(ex velox.Executor, bs ...*%[3]s.%[6]s) *%[3]s.%[8]s { return %[3]s.%[9]s(ex, bs...) }

// Update returns a builder that updates the %[2]s rows its conditions match.
func (*%[1]s) Update(ex velox.Executor) *%[3]s.%[10]s { return %[3]s.%[11]s(ex) }

// Delete returns a builder that deletes the %[2]s rows its conditions match.
func (*%[1]s) Delete(ex velox.Executor) *%[3]s.%[12]s { return %[3]s.%[13]s(ex) }

// Get returns the %[2]s with the given id, or velox.ErrNotFound.
func (c *%[1]s) Get(ctx %[14]s.Context, ex velox.Executor, id %[15]s) (*%[3]s.%[2]s, error) {
	return c.Query(ex).Where(%[16]s(orm.P{Op: orm.OpEQ, Col: %[17]q, Val: id})).Only(ctx)
}

// UpdateByID returns a builder that updates the %[2]s with the given id; end it
// with SaveOne, which reports a missing row.
func (c *%[1]s) UpdateByID(ex velox.Executor, id %[15]s) *%[3]s.%[10]s {
	return c.Update(ex).Where(%[16]s(orm.P{Op: orm.OpEQ, Col: %[17]q, Val: id}))
}

// DeleteByID deletes the %[2]s with the given id, or returns velox.ErrNotFound.
func (c *%[1]s) DeleteByID(ctx %[14]s.Context, ex velox.Executor, id %[15]s) error {
	n, err := c.Delete(ex).Where(%[16]s(orm.P{Op: orm.OpEQ, Col: %[17]q, Val: id})).Exec(ctx)
	if err == nil && n == 0 {
		err = %[3]s.%[18]s.NotFound()
	}
	return err
}

`, t, e.Name, modelPkg, n.Query, n.NewQuery, n.Create, n.NewCreate, n.CreateBulk, n.NewCreateBulk, n.Update, n.NewUpdate, n.Delete, n.NewDelete, ctxPkg, id, e.pred(), e.ID.Col, n.Meta)
		if q := g.gql[e]; q != nil && q.Node {
			nodes = append(nodes, e)
			im.use(gqlinputPath, "gqlinput")
			w(`// node is Get for an id of any type: see NodeTypes.
func (c *%[1]s) node(ctx %[2]s.Context, ex velox.Executor, key any) (Noder, error) {
	v, ok := gqlinput.IDAs[%[3]s](key)
	if !ok {
		return nil, %[5]s.%[4]s.NotFound()
	}
	n, err := c.Get(ctx, ex, v)
	if err != nil {
		return nil, err
	}
	return n, nil
}

`, t, ctxPkg, id, n.Meta, modelPkg)
		}
	}
	var out bytes.Buffer
	out.WriteString(header)
	root := rootPackage(g.cfg.Package)
	fmt.Fprintf(&out, "// Package %[1]s is where every query and mutation starts: %[1]s.<Entity>.Query(conn),\n// %[1]s.<Entity>.Create(conn), %[1]s.<Entity>.UpdateByID(conn, id). The entities are in the\n// entity package, their columns and edges in a package each.\npackage %[1]s\n\n", root)
	out.WriteString(im.block())
	out.WriteString("\n")
	if len(nodes) > 0 {
		sort.Slice(nodes, func(i, j int) bool { return nodes[i].Name < nodes[j].Name })
		fmt.Fprintf(&out, `// Noder is an entity that is a node of the GraphQL schema: what the schema's
// Node interface is bound to.
type Noder interface{ IsNode() }

// NodeType is one type of node of the GraphQL schema.
type NodeType struct {
	// Name is the entity's, and the GraphQL type's.
	Name  string
	Table string
	// Get returns the node of this type with the id, read like the Get of
	// the entity (its policy and interceptors apply), or velox.ErrNotFound. An
	// id that is not of the type's id type is the id of no node of it; an
	// integer of another integer type is the same id.
	Get func(ctx %[1]s.Context, ex velox.Executor, id any) (Noder, error)
}

// NodeTypes returns the types of node, ordered by name: what a node(id:)
// field has to go by when an id does not say its type. To find a node, ask
// each type in turn and take the first answer that is not velox.ErrNotFound;
// that is a query per type, so an application with many types keeps what it
// learns (a prefix in the id, or which type answered for a range of ids).
func NodeTypes() []NodeType {
	return []NodeType{
`, ctxPkg)
		for _, e := range nodes {
			fmt.Fprintf(&out, "\t\t{Name: %q, Table: %q, Get: %s.node},\n", e.Name, e.T.Table(), e.Name)
		}
		out.WriteString("\t}\n}\n\n")
	}
	out.Write(b.Bytes())
	return split(out.Bytes(), root)
}
