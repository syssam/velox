//go:build ignore

// Generate regenerates the tests/integration test fixture.
// Run from the project root: go run tests/integration/generate.go
package main

import (
	"log/slog"
	"os"

	"github.com/syssam/velox/compiler"
	"github.com/syssam/velox/compiler/gen"
)

func main() {
	cfg, err := gen.NewConfig(
		gen.WithTarget("./tests/integration"),
		gen.WithPackage("github.com/syssam/velox/tests/integration"),
		gen.WithFeatures(
			gen.FeaturePrivacy,
			gen.FeatureIntercept,
			gen.FeatureEntQL,
			gen.FeatureNamedEdges,
			gen.FeatureBidiEdgeRefs,
			gen.FeatureSnapshot,
			gen.FeatureSchemaConfig,
			gen.FeatureLock,
			gen.FeatureExecQuery,
			gen.FeatureUpsert,
			gen.FeatureVersionedMigration,
			gen.FeatureGlobalID,
			gen.FeatureValidator,
			gen.FeatureAutoDefault,
		),
	)
	if err != nil {
		slog.Error("creating config", "error", err)
		os.Exit(1)
	}

	if err := compiler.Generate("./testschema", cfg); err != nil {
		slog.Error("running velox codegen", "error", err)
		os.Exit(1)
	}

	slog.Info("prototype regenerated successfully")
}
