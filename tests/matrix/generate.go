//go:build ignore

// Generate regenerates the schema-shape matrix. Run from this directory:
//
//	go run generate.go
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
		gen.WithPackage("example.com/matrix/velox"),
		// A broad feature set on purpose: a shape that compiles only while a
		// feature is off is not covered. Every feature asked for composes;
		// none had to be dropped. Left out deliberately: sql/autodefault,
		// because it would hide the "Optional() adds no DB DEFAULT" behaviour
		// TestBlob pins. sql/globalid logs "mixed ID types across entities,
		// falling back to int" here (UUIDKey and StringKey sit next to int
		// ids); that is a warning, not a failure.
		gen.WithFeatures(
			gen.FeaturePrivacy,
			gen.FeatureIntercept,
			gen.FeatureUpsert,
			gen.FeatureSchemaConfig,
			gen.FeatureNamedEdges,
			gen.FeatureBidiEdgeRefs,
			gen.FeatureModifier,
			gen.FeatureExecQuery,
			gen.FeatureGlobalID,
		),
	)
	if err != nil {
		slog.Error("creating config", "error", err)
		os.Exit(1)
	}

	if err := compiler.Generate("./schema", cfg); err != nil {
		slog.Error("running velox codegen", "error", err)
		os.Exit(1)
	}

	slog.Info("matrix regenerated")
}
