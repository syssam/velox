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
	rest, code, done := parseFlags(fs, args)
	if done {
		return code
	}
	if len(rest) > 1 {
		return tooMany("describe", fs, stderr)
	}
	err := func() error {
		pattern, err := schemaPackage(dir, rest, env)
		if err != nil {
			return err
		}
		m, err := gen.Load(dir, pattern, env)
		if err != nil {
			return err
		}
		g, err := gen.Build(m)
		if err != nil {
			return err
		}
		return gen.Describe(stdout, g, *asJSON)
	}()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "velox-gen: %v\n", relErrors(dir, err))
		return 1
	}
	return 0
}
