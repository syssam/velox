//go:build ignore

// Generate regenerates the tests/integration test fixture.
// Run from the project root: go run tests/integration/generate.go
//
// Includes the contrib/graphql extension so this fixture exercises the
// GraphQL pipeline (entity/gql_edge_*.go, entity/gql_pagination.go,
// filter/, etc.) as part of velox's own integration tests — not only
// via the external examples/fullgql matrix job. Schema under ./testschema
// has RelayConnection + WhereInput annotations on User and Post for
// e2e_graphql_edge_test.go to drive end-to-end.
package main

import (
	"log/slog"
	"os"

	"github.com/syssam/velox/compiler"
	"github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/contrib/graphql"
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

	// contrib/graphql extension — generates filter/, entity/gql_*,
	// query/gql_pagination_*, etc. WithSchemaGenerator emits schema.graphql
	// alongside the Go output so a downstream gqlgen run would see a
	// complete schema, though tests/integration doesn't actually call
	// gqlgen — the tests drive the Go API directly.
	ex, err := graphql.NewExtension(
		graphql.WithSchemaGenerator(),
		graphql.WithSchemaPath("./tests/integration/schema.graphql"),
	)
	if err != nil {
		slog.Error("creating graphql extension", "error", err)
		os.Exit(1)
	}

	if err := compiler.Generate("./testschema", cfg, compiler.Extensions(ex)); err != nil {
		slog.Error("running velox codegen", "error", err)
		os.Exit(1)
	}

	slog.Info("prototype regenerated successfully")
}
