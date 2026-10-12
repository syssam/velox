//go:build ignore

// Command generate writes ./velox and ./graphql from ./schema. It is what
//
//	go run github.com/syssam/velox/cmd/velox-gen -schema ./schema -target ./velox \
//	    -package example.com/orm-graphql/velox -graphql ./graphql/velox.graphql
//
// does, kept as a file so that `go run generate.go` works here as it does in
// the other examples.
package main

import (
	"log/slog"
	"os"

	"github.com/syssam/velox/compiler"
	"github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/contrib/graphql/graphqlgen"
	"github.com/syssam/velox/orm/ormgen"
)

func main() {
	const target, pkg = "./velox", "example.com/orm-graphql/velox"
	cfg, err := gen.NewConfig(gen.WithTarget(target), gen.WithPackage(pkg))
	if err != nil {
		slog.Error("creating config", "error", err)
		os.Exit(1)
	}
	graph, err := compiler.LoadGraph("./schema", cfg)
	if err != nil {
		slog.Error("loading the schema", "error", err)
		os.Exit(1)
	}
	err = ormgen.Generate(graph, ormgen.Config{
		Target:  target,
		Package: pkg,
		GraphQL: &ormgen.GraphQL{Dir: "./graphql", Schema: graphqlgen.Config{
			SchemaFilename:  "velox.graphql",
			SchemaSplitMode: graphqlgen.SchemaSplitPerEntity,
			WhereInputs:     true,
		}},
	})
	if err != nil {
		slog.Error("generating", "error", err)
		os.Exit(1)
	}
	slog.Info("code generation completed successfully", "entities", len(graph.Nodes))
}
