//go:build ignore

package main

import (
	"log"

	"github.com/syssam/velox/compiler"
	"github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/contrib/graphql"
)

func main() {
	// Create GraphQL extension with gqlgen integration
	ex, err := graphql.NewExtension(
		graphql.WithSchemaGenerator(),
		graphql.WithConfigPath("./gqlgen.yml"),
		graphql.WithSchemaPath("./graph/ent.graphql"),
		graphql.WithWhereInputs(true),
	)
	if err != nil {
		log.Fatalf("creating graphql extension: %v", err)
	}

	// Configure code generation with advanced features
	cfg, err := gen.NewConfig(
		gen.WithTarget("./velox"),
		gen.WithPackage("example.com/shop/velox"),
		gen.WithFeatures(
			gen.FeaturePrivacy,      // Multi-tenant, permissions
			gen.FeatureIntercept,    // Soft delete, workspace filtering
			gen.FeatureBidiEdgeRefs, // Bidirectional edges
			gen.FeatureModifier,     // Query.Modify()
			gen.FeatureLock,         // Row-level locking (FOR UPDATE/SHARE)
			gen.FeatureUpsert,       // ON CONFLICT support
			gen.FeatureExecQuery,    // Raw SQL execution
		),
	)
	if err != nil {
		log.Fatalf("creating config: %v", err)
	}

	// Run code generation
	if err := compiler.Generate("./schema", cfg,
		compiler.Extensions(ex),
	); err != nil {
		log.Fatalf("running velox codegen: %v", err)
	}

	log.Println("Code generation completed successfully!")
}
