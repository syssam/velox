//go:build ignore

package main

import (
	"log/slog"
	"os"

	"github.com/syssam/velox/compiler"
	"github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/contrib/graphql"
)

func main() {
	ex, err := graphql.NewExtension(
		graphql.WithSchemaGenerator(),
		graphql.WithSchemaPath("./velox/schema.graphql"),
		graphql.WithConfigPath("./gqlgen.yml"),
	)
	if err != nil {
		slog.Error("creating graphql extension", "error", err)
		os.Exit(1)
	}

	cfg, err := gen.NewConfig(
		gen.WithTarget("./velox"),
		gen.WithFeatures(gen.FeaturePrivacy),
	)
	if err != nil {
		slog.Error("creating config", "error", err)
		os.Exit(1)
	}
	if err := compiler.Generate("./schema", cfg,
		compiler.Extensions(ex),
	); err != nil {
		slog.Error("running velox codegen", "error", err)
		os.Exit(1)
	}
}
