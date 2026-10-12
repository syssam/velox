// Package ormgql generates the GraphQL layer of a tree velox-gen writes: the
// schema files, and the Go a GraphQL engine binds them to.
//
// It is an extension of the generator (orm/ormgen), which knows no GraphQL:
//
//	ormgen.Generate(graph, ormgen.Config{
//		Target:  "./velox",
//		Package: "example.com/app/velox",
//		Extensions: []ormgen.Extension{ormgql.New(ormgql.Config{
//			Dir:    "./graphql",
//			Schema: graphqlgen.Config{SchemaFilename: "velox.graphql", WhereInputs: true},
//		})},
//	})
//
// The schema text is velox's, unchanged: what a client of an application
// sees does not depend on which generator wrote the Go. The Go is derived
// from that text, type by type and field by field, so the two cannot
// disagree.
//
// Everything it writes is in files of its own, so that the tree without
// them is the tree of an application that has no GraphQL:
//
//	<target>/filter/            a where input per entity
//	<target>/entity/<e>_gql.go  the order input, and a method per edge an engine resolves it by
//	<target>/<e>/gql.go         the fields a list of the entity can be ordered by
//	<target>/node_gql.go        the types of node, for a node(id:) field
//
// What the generated code calls at run time is package gqlinput.
package ormgql

import (
	"path/filepath"

	vgen "github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/contrib/graphql/graphqlgen"
	"github.com/syssam/velox/internal/ormgen"
)

// Config says where the schema goes and how it is written.
type Config struct {
	// Dir is the directory the .graphql files are written to.
	Dir string
	// Schema holds velox's schema options: the name of the root file
	// (SchemaFilename), how the schema is split over files, where inputs.
	// The package paths and what the generator itself decides are filled in.
	Schema graphqlgen.Config
}

// New returns the extension that generates the GraphQL layer.
func New(cfg Config) ormgen.Extension { return &extension{cfg: cfg} }

const (
	// gqlinputPath is the runtime of the GraphQL layer: what the generated
	// where and order inputs, and the node lookup, call.
	gqlinputPath = "github.com/syssam/velox/contrib/graphql/gqlinput"
	// orderTypeKey is where LeafNames notes the name of the type of the
	// fields a list can be ordered by.
	orderTypeKey = "gql.OrderField"
)

type extension struct {
	cfg Config
	g   *ormgen.Generator
	// ents is what the schema says of each entity that is in it.
	ents      map[*ormgen.Entity]*gqlEnt
	enumOwner map[*ormgen.Field]*ormgen.Entity
	enumType  map[*ormgen.Field]string
	filterIm  *ormgen.Imports
	// schema are the schema files by name.
	schema map[string]string
}

// Begin generates the schema text and reads from it what Go each entity
// needs.
func (ext *extension) Begin(g *ormgen.Generator, graph *vgen.Graph) error {
	// A second generation with the same extension starts from nothing.
	ext.g, ext.ents = g, nil
	// The enum types are named, and every field knows its own, before the
	// schema is read: a where input names them.
	ext.enumOwner, ext.enumType = map[*ormgen.Field]*ormgen.Entity{}, map[*ormgen.Field]string{}
	for _, e := range g.Entities() {
		_, types := g.LeafNames(e)
		for f, t := range types {
			ext.enumOwner[f], ext.enumType[f] = e, t
		}
	}
	ext.filterIm = g.Imports()
	schema, err := ext.read(graph)
	if err != nil {
		return err
	}
	ext.schema = schema
	for _, q := range ext.ents {
		if q.Node {
			g.ReserveRoot("Noder", "NodeType", "NodeTypes")
			break
		}
	}
	return nil
}

// LeafNames takes, in the package of every entity, the name of the type of
// the fields a list can be ordered by, and the names its values were given
// when the schema was read (see gqlOrder for why they are not in package
// entity).
func (ext *extension) LeafNames(e *ormgen.Entity, n *ormgen.Namer) {
	n.Ext[orderTypeKey] = n.Unique("OrderField", "Type")
	if q := ext.ents[e]; q != nil {
		for _, o := range q.Order {
			n.Take(o.Const)
		}
	}
}

// Files returns the Go of the layer.
func (ext *extension) Files() (map[string][]byte, error) {
	files := map[string][]byte{}
	for dir, gen := range map[string]func() (map[string][]byte, error){
		"filter": ext.filters,
		"entity": ext.entities,
		"":       ext.nodes,
	} {
		part, err := gen()
		if err != nil {
			return nil, err
		}
		for name, src := range part {
			files[filepath.Join(dir, name)] = src
		}
	}
	for _, e := range ext.g.Entities() {
		name, src, err := ext.orderFields(e)
		if err != nil {
			return nil, err
		}
		if src != nil {
			files[filepath.Join(e.Pkg, name)] = src
		}
	}
	return files, nil
}

// End writes the schema files.
func (ext *extension) End() error { return ext.writeSchema(ext.schema) }
