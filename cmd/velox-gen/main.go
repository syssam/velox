// Command velox-gen writes the typed column handles and the scan plan of every
// velox entity: one file, <entity>_velox.go, beside each struct that declares
// func (x *T) Schema(s *velox.Schema).
//
// Install it as a tool of your module and run it from the module root:
//
//	go get -tool github.com/syssam/velox2/cmd/velox-gen
//	go tool velox-gen ./...
//
// Usage:
//
//	velox-gen [-check] [-ordered=path.Type,...] [-number=path.Type,...] [packages]
//	velox-gen verify [packages]
//
// velox-gen writes nothing when any entity has a problem. -check writes
// nothing and exits 1 when a generated file is out of date; CI runs it.
// verify builds every entity's metadata and reports mistakes in Schema and
// Mixin methods, which velox-gen itself cannot see because it never runs your
// code.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	os.Exit(run(os.Args[1:], ".", nil, os.Stdout, os.Stderr))
}

// run is the command, with its directory, environment and output passed in
// so that tests can run it.
func run(args []string, dir string, env []string, stdout, stderr io.Writer) int {
	verify := len(args) > 0 && args[0] == "verify"
	if verify {
		args = args[1:]
	}
	fs := flag.NewFlagSet("velox-gen", flag.ContinueOnError)
	fs.SetOutput(stderr)
	checkOnly := fs.Bool("check", false, "write nothing; exit 1 when a generated file is out of date")
	ordered := fs.String("ordered", "", "comma-separated types, as import/path.Name, whose handles are ordered")
	number := fs.String("number", "", "comma-separated types, as import/path.Name, whose handles are numbers")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	patterns := fs.Args()
	if len(patterns) == 0 {
		patterns = []string{"."}
	}
	cfg := config{dir: dir, env: env, ordered: typeSet(*ordered), number: typeSet(*number)}
	ps, err := load(cfg, patterns)
	if err != nil {
		fmt.Fprintf(stderr, "velox-gen: %v\n", err)
		return 1
	}
	if verify {
		return runVerify(cfg, ps, stdout, stderr)
	}
	res, probs := plan(ps, cfg)
	if report(stderr, probs) {
		return 1
	}
	if *checkOnly {
		files := stale(res)
		for _, f := range files {
			fmt.Fprintf(stderr, "velox-gen: %s is out of date\n", relPath(dir, f))
		}
		if len(files) > 0 {
			fmt.Fprintf(stderr, "velox-gen: run go tool velox-gen %s\n", strings.Join(patterns, " "))
			return 1
		}
		return 0
	}
	changed, err := apply(res)
	if err != nil {
		fmt.Fprintf(stderr, "velox-gen: %v\n", err)
		return 1
	}
	for _, f := range changed {
		fmt.Fprintln(stdout, relPath(dir, f))
	}
	return 0
}

// report prints problems and says whether there were any.
func report(w io.Writer, probs []problem) bool {
	for _, p := range probs {
		fmt.Fprintf(w, "velox-gen: %s\n", p)
	}
	return len(probs) > 0
}

func typeSet(s string) map[string]bool {
	m := map[string]bool{}
	for _, t := range strings.Split(s, ",") {
		if t = strings.TrimSpace(t); t != "" {
			m[t] = true
		}
	}
	return m
}

// relPath shows a path relative to dir when it is inside dir.
func relPath(dir, path string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return path
	}
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		abs = r
	}
	if r, err := filepath.Rel(abs, path); err == nil && !strings.HasPrefix(r, "..") {
		return r
	}
	return path
}
