// Command compare is the harness that compares velox's build and runtime cost
// with veloxe and ent. Each subcommand is one stage of bench/compare/run.sh.
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
  step         run one command, timed and logged, behind an optional load gate
  build        measure one ORM's build cost at one size, as JSON
  runtime-app  write one ORM's runtime app: the n=10 schema, its code, the benchmarks
  summarize    turn a results directory into summary.json
  report       turn summary.json into Markdown tables, on stdout
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
	case "runtime-app":
		os.Exit(runtimeAppMain(os.Args[2:]))
	case "summarize":
		os.Exit(summarizeMain(os.Args[2:]))
	case "report":
		os.Exit(reportMain(os.Args[2:]))
	default:
		fmt.Fprintf(os.Stderr, "compare: unknown subcommand %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
}
