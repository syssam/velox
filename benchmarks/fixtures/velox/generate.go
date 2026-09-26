//go:build ignore

// The velox side of the Velox-vs-Ent benchmark: the same 50 entities as
// ../ent/ent/schema, converted import for import, generated with the
// equivalent options.
package main

import (
	"log"

	"github.com/syssam/velox/compiler"
	"github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/contrib/graphql/graphqlgen"
)

func main() {
	ex, err := graphqlgen.NewExtension(
		graphqlgen.WithSchemaGenerator(),
		graphqlgen.WithSchemaPath("velox.graphql"),
		graphqlgen.WithWhereInputs(true),
	)
	if err != nil {
		log.Fatalf("creating graphql extension: %v", err)
	}
	cfg, err := gen.NewConfig(gen.WithTarget("./velox"))
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if err := compiler.Generate("./schema", cfg, compiler.Extensions(ex)); err != nil {
		log.Fatalf("running velox codegen: %v", err)
	}
}
