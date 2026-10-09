// Command vxgen generates vx code from a velox schema package.
//
//	vxgen -schema ./schema -target ./db -package example.com/app/db
package main

import (
	"flag"
	"log/slog"
	"os"

	"github.com/syssam/velox/compiler"
	vgen "github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/vx/gen"
)

func main() {
	schema := flag.String("schema", "./schema", "path of the schema package")
	target := flag.String("target", "./db", "output directory")
	pkg := flag.String("package", "", "import path of the output directory")
	flag.Parse()
	if *pkg == "" {
		slog.Error("vxgen: -package is required")
		os.Exit(2)
	}
	cfg, err := vgen.NewConfig(vgen.WithTarget(*target), vgen.WithPackage(*pkg))
	if err != nil {
		slog.Error("vxgen: config", "error", err)
		os.Exit(1)
	}
	graph, err := compiler.LoadGraph(*schema, cfg)
	if err != nil {
		slog.Error("vxgen: loading the schema", "error", err)
		os.Exit(1)
	}
	if err := gen.Generate(graph, gen.Config{Target: *target, Package: *pkg}); err != nil {
		slog.Error("vxgen: generating", "error", err)
		os.Exit(1)
	}
	slog.Info("vxgen: done", "entities", len(graph.Nodes), "target", *target)
}
