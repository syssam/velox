//go:build ignore

// The velox side of the Velox-vs-Ent benchmark: the same 50 entities as
// ../ent/ent/schema, converted import for import, generated with the
// equivalent options.
package main

import (
	"log"

	"github.com/syssam/velox/compiler"
	"github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/contrib/graphql"
)

func main() {
	ex, err := graphql.NewExtension(
		graphql.WithSchemaGenerator(),
		graphql.WithSchemaPath("velox.graphql"),
		graphql.WithWhereInputs(true),
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
