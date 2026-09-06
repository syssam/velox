package main

import (
	"flag"
	"fmt"

	"github.com/syssam/velox/compiler"
	"github.com/syssam/velox/compiler/gen"
)

func validateCmd(args []string) error {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	verbose := setupVerbose(fs)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), `Validate schema definitions without generating code.

Usage:
  velox validate <schema-path>

Loads and validates all schema files in the given directory, reporting any
errors. No code is generated. This is useful for CI checks or quick feedback
while editing schemas.

Examples:
  velox validate ./schema
`)
	}

	if err := fs.Parse(args); err != nil {
		return err
	}
	applyVerbose(*verbose)

	if fs.NArg() < 1 {
		return fmt.Errorf("missing schema path\nusage: velox validate <schema-path>")
	}

	schemaPath := fs.Arg(0)

	cfg, err := gen.NewConfig()
	if err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	graph, err := compiler.LoadGraph(schemaPath, cfg)
	if err != nil {
		return fmt.Errorf("schema validation failed: %w", err)
	}

	fmt.Printf("velox: schema valid — %d entities found\n", len(graph.Nodes))
	return nil
}
