// Command velox-gen generates the code of an application from a velox schema
// package: the entities, their columns and edges, and the clients that
// package orm runs.
//
//	velox-gen -schema ./schema -target ./velox -package example.com/app/velox
package main

import (
	"flag"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/syssam/velox/compiler"
	vgen "github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/contrib/graphql/graphqlgen"
	"github.com/syssam/velox/orm/ormgen"
)

func main() {
	schema := flag.String("schema", "./schema", "path of the schema package")
	target := flag.String("target", "./velox", "output directory")
	pkg := flag.String("package", "", "import path of the output directory")
	graphqlSchema := flag.String("graphql", "", "also generate the GraphQL layer; the path of the root schema file to write, e.g. ./schema/velox.graphql")
	flag.Parse()
	if *pkg == "" {
		slog.Error("velox-gen: -package is required")
		os.Exit(2)
	}
	cfg, err := vgen.NewConfig(vgen.WithTarget(*target), vgen.WithPackage(*pkg))
	if err != nil {
		slog.Error("velox-gen: config", "error", err)
		os.Exit(1)
	}
	graph, err := compiler.LoadGraph(*schema, cfg)
	if err != nil {
		slog.Error("velox-gen: loading the schema", "error", err)
		os.Exit(1)
	}
	gcfg := ormgen.Config{Target: *target, Package: *pkg}
	if *graphqlSchema != "" {
		gcfg.GraphQL = &ormgen.GraphQL{Dir: filepath.Dir(*graphqlSchema), Schema: graphqlgen.Config{
			SchemaFilename: filepath.Base(*graphqlSchema), SchemaSplitMode: graphqlgen.SchemaSplitPerEntity, WhereInputs: true,
		}}
	}
	if err := ormgen.Generate(graph, gcfg); err != nil {
		slog.Error("velox-gen: generating", "error", err)
		os.Exit(1)
	}
	slog.Info("velox-gen: done", "entities", len(graph.Nodes), "target", *target)
}
