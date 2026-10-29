// Command compare is the harness that compares velox2's build and runtime cost
// with old velox and ent. Each subcommand is one stage of bench/compare/run.sh.
package main

import (
	"fmt"
	"os"

	// the apps' drivers: required here, so that every app resolves the same version from this go.sum
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

const usage = `usage: compare <subcommand> [flags]

subcommands:
  step   run one command, timed and logged, behind an optional load gate
  build  measure one ORM's build cost at one size, as JSON
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "step":
		os.Exit(stepMain(os.Args[2:]))
	case "build":
		os.Exit(buildMain(os.Args[2:]))
	default:
		fmt.Fprintf(os.Stderr, "compare: unknown subcommand %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
}
