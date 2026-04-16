//go:build ignore

package main

import (
	"log/slog"
	"os"

	"github.com/syssam/velox/compiler"
	"github.com/syssam/velox/compiler/gen"
)

func main() {
	cfg, err := gen.NewConfig(
		gen.WithTarget("./velox"),
		// FeatureGlobalID assigns each entity type a distinct "type prefix"
		// in its IDs, so a single integer ID uniquely identifies both the
		// entity type and the row. Useful for GraphQL Relay `Node` interfaces
		// where every object has a globally unique `id`.
		gen.WithFeatures(gen.FeatureGlobalID),
	)
	if err != nil {
		slog.Error("creating config", "error", err)
		os.Exit(1)
	}

	if err := compiler.Generate("./schema", cfg); err != nil {
		slog.Error("running velox codegen", "error", err)
		os.Exit(1)
	}

	slog.Info("code generation completed successfully")
}
