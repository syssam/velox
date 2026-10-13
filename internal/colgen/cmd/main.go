// Command cmd writes the column types of package orm to the file it is
// given: see package colgen.
package main

import (
	"log/slog"
	"os"

	"github.com/syssam/velox/internal/colgen"
)

func main() {
	if len(os.Args) != 2 {
		slog.Error("usage: go run ./internal/colgen/cmd <file>")
		os.Exit(2)
	}
	src, err := colgen.Source()
	if err != nil {
		slog.Error("colgen", "error", err)
		os.Exit(1)
	}
	if err := os.WriteFile(os.Args[1], src, 0o644); err != nil { //nolint:gosec // a source file of the repository
		slog.Error("colgen", "error", err)
		os.Exit(1)
	}
}
