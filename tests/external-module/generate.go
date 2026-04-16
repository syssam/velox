//go:build ignore

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
		gen.WithPackage("example.com/integration-test/velox"),
	)
	if err != nil {
		slog.Error("creating config", "error", err)
		os.Exit(1)
	}

	if err := compiler.Generate("./schema", cfg); err != nil {
		slog.Error("running velox codegen", "error", err)
		os.Exit(1)
	}

	slog.Info("Code generation completed successfully!")
}
