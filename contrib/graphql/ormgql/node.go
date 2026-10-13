package ormgql

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/syssam/velox/internal/ormgen"
)

// orderFile is the file of the layer in the package of e.
func orderFile(e *ormgen.Entity) string {
	if ormgen.FileName(e) == "gql" {
		return "gql_gen"
	}
	return "gql"
}

// orderFields generates what the package of e holds of the layer: the type of
// the fields a list of e can be ordered by, and its values. It returns no
// source for an entity without them.
func (ext *extension) orderFields(e *ormgen.Entity) (name string, src []byte, err error) {
	q := ext.ents[e]
	if q == nil || !q.HasOrder {
		return "", nil, nil
	}
	var b bytes.Buffer
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	b.WriteString(ormgen.Header)
	w("package %s\n\n", e.Pkg)
	w("// %[1]s is a field a list of %[2]s can be ordered by, as the GraphQL schema\n// names it: the Field of an entity.%[2]sOrder.\ntype %[1]s string\n\n// The fields a list of %[2]s can be ordered by.\nconst (\n", q.OrderType, e.Name)
	for _, o := range q.Order {
		w("\t%s %s = %q\n", o.Const, q.OrderType, o.Value)
	}
	w(")\n")
	name = orderFile(e) + ".go"
	src, err = ormgen.Formatted(b.Bytes(), e.Pkg+"/"+name)
	return name, src, err
}

// nodes generates what the root package holds of the layer: the types of
// node of the schema, and how a node of each is read by an id of any type.
func (ext *extension) nodes() (map[string][]byte, error) {
	g := ext.g
	var nodes []*ormgen.Entity
	for _, e := range g.Entities() {
		if q := ext.ents[e]; q != nil && q.Node {
			nodes = append(nodes, e)
		}
	}
	if len(nodes) == 0 {
		return nil, nil
	}
	im := g.Imports()
	im.Use(ormgen.VeloxPath, "velox")
	im.Use(gqlinputPath, "gqlinput")
	ctxPkg := im.Use("context", "context")
	var out bytes.Buffer
	out.WriteString(ormgen.Header)
	fmt.Fprintf(&out, "package %s\n\n", ormgen.RootPackage(g.Package()))
	out.WriteString(im.Block())
	out.WriteString("\n")
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
		fmt.Fprintf(&out, "\t\t{Name: %q, Table: %q, Get: gqlinput.NodeGet[Noder](&%s)},\n", e.Name, e.T.Table(), e.Name)
	}
	out.WriteString("\t}\n}\n")
	return ormgen.Split(out.Bytes(), "node_gql")
}
