// Command cmd writes the column types of package orm, and the calls that
// test them, to the two files it is given: see package colgen.
package main

import (
	"log/slog"
	"os"

	"github.com/syssam/velox/internal/colgen"
)

func main() {
	if len(os.Args) != 3 {
		slog.Error("usage: go run ./internal/colgen/cmd <columns.go> <columns_calls_test.go>")
		os.Exit(2)
	}
	for i, source := range []func() ([]byte, error){colgen.Source, colgen.Calls} {
		src, err := source()
		if err != nil {
			slog.Error("colgen", "error", err)
			os.Exit(1)
		}
		if err := os.WriteFile(os.Args[i+1], src, 0o644); err != nil { //nolint:gosec // a source file of the repository
			slog.Error("colgen", "error", err)
			os.Exit(1)
		}
	}
}
