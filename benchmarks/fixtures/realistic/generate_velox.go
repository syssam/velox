//go:build ignore

// generate_velox generates velox's code for the fixture into ./velox, with the
// features and the GraphQL extension of the application the fixture models.
// Set REALISTIC_GRAPHQL=0 for the core generator alone.
package main

import (
	"log"
	"os"

	"github.com/syssam/velox/compiler"
	"github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/contrib/graphql/graphqlgen"
)

func main() {
	cfg, err := gen.NewConfig(
		gen.WithTarget("./velox"),
		gen.WithPackage("example.com/realistic/velox"),
		gen.WithFeatures(gen.FeaturePrivacy, gen.FeatureIntercept, gen.FeatureBidiEdgeRefs,
			gen.FeatureModifier, gen.FeatureLock, gen.FeatureAutoDefault),
	)
	if err != nil {
		log.Fatal(err)
	}
	var opts []compiler.Option
	if os.Getenv("REALISTIC_GRAPHQL") != "0" {
		ex, err := graphqlgen.NewExtension(
			graphqlgen.WithoutGQLGen(),
			graphqlgen.WithSchemaGenerator(),
			graphqlgen.WithSchemaPath("gqlschema/velox.graphql"),
			graphqlgen.WithSchemaSplitMode(graphqlgen.SchemaSplitPerEntity),
			graphqlgen.WithWhereInputs(true),
		)
		if err != nil {
			log.Fatal(err)
		}
		opts = append(opts, compiler.Extensions(ex))
	}
	if err := compiler.Generate("./schema", cfg, opts...); err != nil {
		log.Fatal(err)
	}
}
