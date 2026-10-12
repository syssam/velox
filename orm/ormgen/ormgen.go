// Package ormgen generates the code of an application from a velox schema
// graph: package entity holding every entity, a small package per entity
// holding its columns and edges, and a root package where every query
// starts. cmd/velox-gen is this package as a command.
//
// It generates what package orm runs and nothing else. A layer on top of
// the tree is an Extension; the GraphQL layer is one, in
// contrib/graphql/ormgql.
package ormgen

import (
	"github.com/syssam/velox/compiler/gen"
	core "github.com/syssam/velox/internal/ormgen"
)

// Config says where the generated packages go, and which layers are
// generated on top of them.
type Config = core.Config

// Extension is a layer generated on top of the tree: declarations of its
// own, in files of its own, in the packages the generator writes. It is
// implemented in this module (contrib/graphql/ormgql.New returns one); what
// it is written with is not a public interface.
type Extension = core.Extension

// Generate writes the generated tree of graph into cfg.Target.
func Generate(graph *gen.Graph, cfg Config) error { return core.Generate(graph, cfg) }

// RootNames returns the names of the library's root package that the root
// package of a generated tree gives under the same name, so that a file
// needs one import; an entity called like one of them keeps the name.
func RootNames() []string { return core.RootNames() }
