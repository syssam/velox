//go:build ignore

// Dual generator for the parity harness: generates the velox client into
// ./velox from ./schema, and the Ent client into ./ent from ./entschema.
// Run from this directory: go run generate.go
package main

import (
	"log"

	entgen "entgo.io/ent/entc/gen"

	"entgo.io/contrib/entgql"
	"entgo.io/ent/entc"

	"github.com/syssam/velox/compiler"
	"github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/contrib/graphql"
)

func main() {
	generateVelox()
	generateEnt()
}

// generateVelox runs velox codegen over ./schema into ./velox.
func generateVelox() {
	cfg, err := gen.NewConfig(
		gen.WithTarget("./velox"),
		gen.WithPackage("velox.test/parity/velox"),
		gen.WithFeatures(gen.FeatureUpsert),
	)
	if err != nil {
		log.Fatalf("creating velox config: %v", err)
	}

	vex, err := graphql.NewExtension(
		graphql.WithSchemaGenerator(),
		graphql.WithSchemaPath("./velox/schema.graphql"),
	)
	if err != nil {
		log.Fatalf("creating velox graphql extension: %v", err)
	}

	if err := compiler.Generate("./schema", cfg, compiler.Extensions(vex)); err != nil {
		log.Fatalf("running velox codegen: %v", err)
	}
}

// generateEnt runs Ent codegen over ./entschema into ./ent.
func generateEnt() {
	eex, err := entgql.NewExtension(
		entgql.WithSchemaGenerator(),
		entgql.WithSchemaPath("./ent/ent.graphql"),
		entgql.WithWhereInputs(true),
	)
	if err != nil {
		log.Fatalf("creating ent graphql extension: %v", err)
	}

	if err := entc.Generate("./entschema", &entgen.Config{
		Target:  "./ent",
		Package: "velox.test/parity/ent",
		Features: []entgen.Feature{
			entgen.FeatureNamedEdges,
			entgen.FeatureUpsert,
		},
	}, entc.Extensions(eex)); err != nil {
		log.Fatalf("running ent codegen: %v", err)
	}
}
