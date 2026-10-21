package main

import (
	"fmt"
	"io"

	"github.com/syssam/velox2/internal/gen"
)

// runDescribe is velox-gen describe: it loads the schema package, builds its
// graph and prints it.
func runDescribe(args []string, dir string, env []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("describe", stderr)
	asJSON := fs.Bool("json", false, "print the model that a plugin receives, as JSON")
	if code, done := parseFlags(fs, args); done {
		return code
	}
	pattern := "./schema"
	switch fs.NArg() {
	case 0:
	case 1:
		pattern = fs.Arg(0)
	default:
		_, _ = fmt.Fprintln(stderr, "velox-gen: describe takes one package")
		return 2
	}
	m, err := gen.Load(dir, pattern, env)
	if err == nil {
		var g *gen.Graph
		if g, err = gen.Build(m); err == nil {
			err = gen.Describe(stdout, g, *asJSON)
		}
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "velox-gen: %v\n", relErrors(dir, err))
		return 1
	}
	return 0
}
