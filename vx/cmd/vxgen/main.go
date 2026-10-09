// Command vxgen generates vx code from a velox schema package.
//
//	vxgen -schema ./schema -target ./db -package example.com/app/db
package main

import (
	"flag"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/syssam/velox/compiler"
	vgen "github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/contrib/graphql/graphqlgen"
	"github.com/syssam/velox/vx/gen"
)

func main() {
	schema := flag.String("schema", "./schema", "path of the schema package")
	target := flag.String("target", "./db", "output directory")
	pkg := flag.String("package", "", "import path of the output directory")
	graphqlSchema := flag.String("graphql", "", "also generate the GraphQL layer; the path of the root schema file to write, e.g. ./schema/velox.graphql")
	flag.Parse()
	if *pkg == "" {
		slog.Error("vxgen: -package is required")
		os.Exit(2)
	}
	cfg, err := vgen.NewConfig(vgen.WithTarget(*target), vgen.WithPackage(*pkg))
	if err != nil {
		slog.Error("vxgen: config", "error", err)
		os.Exit(1)
	}
	graph, err := compiler.LoadGraph(*schema, cfg)
	if err != nil {
		slog.Error("vxgen: loading the schema", "error", err)
		os.Exit(1)
	}
	gcfg := gen.Config{Target: *target, Package: *pkg}
	if *graphqlSchema != "" {
		gcfg.GraphQL = &gen.GraphQL{Dir: filepath.Dir(*graphqlSchema), Schema: graphqlgen.Config{
			SchemaFilename: filepath.Base(*graphqlSchema), SchemaSplitMode: graphqlgen.SchemaSplitPerEntity, WhereInputs: true,
		}}
	}
	if err := gen.Generate(graph, gcfg); err != nil {
		slog.Error("vxgen: generating", "error", err)
		os.Exit(1)
	}
	slog.Info("vxgen: done", "entities", len(graph.Nodes), "target", *target)
}
