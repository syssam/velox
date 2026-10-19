// Command velox-gen generates the Go code of velox entities. It has two modes,
// chosen by its argument.
//
// The new mode reads a schema package, a package whose structs embed
// schema.Schema, and writes the gen package: one file, <entity>_velox.go,
// for every entity and for every join entity of a many-to-many relation, and
// velox.go, which holds the enums and the hash of the model.
//
// The old mode scans the structs that declare func (x *T) Schema(s
// *velox.Schema) and writes the typed column handles beside each one. It runs
// when the argument has "..." or names no package or several, or names a
// package by its import path that declares no schemas, and it is removed in
// schema plan 4. A directory that holds Go files and declares no schemas is an
// error in the new mode, not an old-mode package, and so is any other failure
// of go list for the argument, such as a go.mod that needs go mod tidy.
//
// Install it as a tool of your module and run it from the module root:
//
//	go get -tool github.com/syssam/velox2/cmd/velox-gen
//	go tool velox-gen ./schema
//
// Usage:
//
//	velox-gen [-out dir] [-package name] [-check] [package]
//	velox-gen verify -out dir [package]
//	velox-gen describe [-json] [package]
//
//	velox-gen [-check] [-ordered=path.Type,...] [-number=path.Type,...] [patterns]
//	velox-gen verify [patterns]
//
// The first three lines are the new mode. The package defaults to ./schema,
// -out to ./gen and -package to the base name of -out. velox-gen writes
// nothing when any entity has a problem. -check writes nothing and exits 1
// when a generated file is out of date, missing or left over; CI runs it, and
// it is the check that the generated package matches the schema. A run
// rewrites the files that changed, deletes the generated files that no entity
// produces any more, and prints each path it wrote or deleted. It writes,
// rewrites and deletes only files that start with its header "Code generated
// by velox-gen. DO NOT EDIT."; a file of yours at a path it would write is an
// error. The files you add to the output directory stay: a method Policy or
// Hooks on a generated entity E, with receiver *E, in a file of yours there,
// is bound into E's metadata. A value receiver is an error, because velox
// calls the method on a nil *E. Your own file must not declare a type named
// like a generated one.
//
// verify -out builds a program in a temporary .velox-verify-* directory of
// your module, imports the generated package and reports, for every entity,
// the mistakes that velox.CheckEntity finds and, in the new mode,
// velox.CheckRelation for every relation, whose target the generated metadata
// does not name. It removes the directory, but a killed process leaves it
// behind; ignore .velox-verify-*/ in git.
//
// describe prints the entities, columns and relations that the schema package
// (default ./schema) declares.
//
// The old mode (the last two lines) writes nothing when any entity has a
// problem; -check, verify and the flags -ordered and -number are as before.
// -out and -package are errors in it.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

func main() {
	os.Exit(run(os.Args[1:], ".", nil, os.Stdout, os.Stderr))
}

// run is the command, with its directory, environment and output passed in
// so that tests can run it.
func run(args []string, dir string, env []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "describe" {
		return runDescribe(args[1:], dir, env, stdout, stderr)
	}
	verify := len(args) > 0 && args[0] == "verify"
	if verify {
		args = args[1:]
	}
	fs := flag.NewFlagSet("velox-gen", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = usage(fs)
	checkOnly := fs.Bool("check", false, "write nothing; exit 1 when a generated file is out of date")
	ordered := fs.String("ordered", "", "comma-separated types, as import/path.Name, whose handles are ordered")
	number := fs.String("number", "", "comma-separated types, as import/path.Name, whose handles are numbers")
	outDir := fs.String("out", "./gen", "the directory of the generated package, for a schema package")
	pkgName := fs.String("package", "", "the generated package's name, for a schema package (default: the base name of -out)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	patterns := fs.Args()
	var set, rerun []string
	fs.Visit(func(f *flag.Flag) {
		set = append(set, f.Name)
		if f.Name == "out" || f.Name == "package" {
			rerun = append(rerun, "-"+f.Name, f.Value.String())
		}
	})
	pattern, isSchema, err := schemaPackage(dir, patterns, env)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "velox-gen: %v\n", err)
		return 1
	}
	if isSchema {
		for _, name := range []string{"ordered", "number"} {
			if slices.Contains(set, name) {
				_, _ = fmt.Fprintln(stderr, "velox-gen: -ordered and -number apply to the old mode only")
				return 1
			}
		}
		f := genFlags{out: *outDir, pkg: *pkgName, check: *checkOnly, rerun: append(rerun, patterns...)}
		if verify {
			return runGenerateVerify(f, pattern, dir, env, stdout, stderr)
		}
		return runGenerate(f, pattern, dir, env, stdout, stderr)
	}
	for _, name := range []string{"out", "package"} {
		if slices.Contains(set, name) {
			_, _ = fmt.Fprintf(stderr, "velox-gen: -%s needs a schema package\n", name)
			return 1
		}
	}
	if len(patterns) == 0 {
		patterns = []string{"."}
	}
	cfg := config{dir: dir, env: env, ordered: typeSet(*ordered), number: typeSet(*number)}
	ps, err := load(cfg, patterns)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "velox-gen: %v\n", err)
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
			_, _ = fmt.Fprintf(stderr, "velox-gen: %s is out of date\n", relPath(dir, f))
		}
		if len(files) > 0 {
			_, _ = fmt.Fprintf(stderr, "velox-gen: run go tool velox-gen %s\n", strings.Join(patterns, " "))
			return 1
		}
		return 0
	}
	changed, err := apply(res)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "velox-gen: %v\n", err)
		return 1
	}
	for _, f := range changed {
		_, _ = fmt.Fprintln(stdout, relPath(dir, f))
	}
	return 0
}

// report prints problems and says whether there were any.
func report(w io.Writer, probs []problem) bool {
	for _, p := range probs {
		_, _ = fmt.Fprintf(w, "velox-gen: %s\n", p)
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
