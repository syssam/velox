package generate_test

import (
	"context"
	"log"

	"github.com/syssam/velox/generate"
)

// A generate.go in the module's root replaces ent's and veloxe's
// compiler.Generate("./schema", cfg). Its build constraint keeps it out of
// the package, and a go:generate line in another file of the package runs
// it, as ent's entc.go is run:
//
//	// generate.go
//	//go:build ignore
//
//	package main
//
//	// doc.go
//	//go:generate go run generate.go
//
// The program below is generate.go's main. It generates ./velox from
// ./veloxschema, with velox-graphql as a tool of the module.
// graphqlgen.Plugin(opts), from
// github.com/syssam/velox/contrib/graphql/graphqlgen, would run
// velox-graphql in the process, with no tool to build. CI runs the program
// with Check set, as velox-gen -check does not know a plugin's Run.
func ExampleRun() {
	cfg := generate.Config{
		Schema:  "./veloxschema",
		Out:     "./velox",
		Plugins: []generate.Plugin{{Tool: "velox-graphql", Options: map[string]string{"compat": "veloxe"}}},
	}
	if _, err := generate.Run(context.Background(), "", cfg); err != nil {
		log.Fatal(err)
	}
}
