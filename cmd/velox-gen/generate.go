package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/syssam/velox/generate"
	"github.com/syssam/velox/internal/genrun"
)

// veloxPath is the import path of the velox library.
const veloxPath = "github.com/syssam/velox"

// genFlags are the flags of generate and verify.
type genFlags struct {
	out, pkg string
	check    bool
	plugins  []pluginSpec // the -plugin flags, in order
	rerun    []string     // the flags the user set, as a command line, for the -check hint
}

// outDir is the absolute path of the output directory (genrun.OutDir).
func (f genFlags) outDir(dir string) string { return genrun.OutDir(dir, f.out) }

// genPlugins are the -plugin flags as the plugins genrun runs.
func (f genFlags) genPlugins() []genrun.Plugin {
	ps := make([]genrun.Plugin, len(f.plugins))
	for i, s := range f.plugins {
		ps[i] = genrun.Plugin{Name: s.name, Options: s.options}
	}
	return ps
}

// runGenerate generates the gen package of the schema package at pattern,
// with generate.Run, and returns the exit code: 3 for a stale -check, 130
// for an interrupted write.
func runGenerate(f genFlags, pattern, dir string, env []string, stdout, stderr io.Writer) int {
	cfg := generate.Config{Schema: pattern, Out: f.out, Package: f.pkg, Check: f.check, Env: env, Stdout: stdout, Stderr: stderr}
	for _, s := range f.plugins {
		cfg.Plugins = append(cfg.Plugins, generate.Plugin{Tool: s.name, Options: s.options})
	}
	_, err := generate.Run(context.Background(), dir, cfg)
	switch {
	case err == nil:
		return 0
	case errors.Is(err, generate.ErrStale):
		_, _ = fmt.Fprintf(stderr, "velox-gen: run %s\n", rerunCommand(f.rerun))
		return 3
	case errors.Is(err, generate.ErrInterrupted):
		if j, ok := err.(interface{ Unwrap() []error }); ok {
			for _, e := range j.Unwrap() {
				if !errors.Is(e, generate.ErrInterrupted) {
					_, _ = fmt.Fprintf(stderr, "velox-gen: %v\n", e)
				}
			}
		}
		_, _ = fmt.Fprintln(stderr, "velox-gen: interrupted")
		return interruptedCode
	}
	_, _ = fmt.Fprintf(stderr, "velox-gen: %v\n", err)
	return 1
}

// rerunCommand is the command that regenerates with the flags args, each
// quoted for a POSIX shell where it needs to be.
func rerunCommand(args []string) string {
	words := []string{"go", "tool", "velox-gen"}
	for _, a := range args {
		words = append(words, shellQuote(a))
	}
	return strings.Join(words, " ")
}

// shellQuote is s as a POSIX shell word: itself when the shell neither splits
// nor expands it, else in single quotes, with each single quote in it
// closed, escaped and reopened.
func shellQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_@%+=:,./-") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// runGenerateVerify is verify for a generated package: it builds a program in
// the module of the output package that calls velox.CheckEntity for every
// entity and join entity of the schema package at pattern, and
// velox.CheckRelation for every relation, which only the handle of a relation
// can resolve: the generated metadata names no relation's target.
func runGenerateVerify(f genFlags, pattern, dir string, env []string, stdout, stderr io.Writer) int {
	fail := func(err error) int {
		_, _ = fmt.Fprintf(stderr, "velox-gen verify: %v\n", err)
		return 1
	}
	out := f.outDir(dir)
	pkg, err := genrun.PackageName(out, f.pkg)
	if err != nil {
		return fail(err)
	}
	gm, err := genrun.LoadModel(context.Background(), dir, pattern, out, pkg, env)
	if err != nil {
		return fail(err)
	}
	if fi, err := os.Stat(out); err != nil || !fi.IsDir() {
		return fail(fmt.Errorf("%s is not a directory; run velox-gen to generate it first", genrun.RelPath(dir, out)))
	}
	mod, err := genrun.ModuleDir(context.Background(), dir, out, env)
	if err != nil {
		return fail(err)
	}
	// The plugins' Go files are compiled with the package, as they are on
	// disk; they must be the files the plugins write now. A plugin path that
	// collides with a file of velox-gen's fails as it does in generate.
	o, err := genrun.BuildOutput(context.Background(), f.genPlugins(), gm, pattern, dir, out, pkg, env, &genrun.Log{Stderr: stderr})
	if err != nil {
		return fail(err)
	}
	outPath := o.GenPath
	for _, p := range slices.Sorted(maps.Keys(o.Plugin)) {
		if filepath.Ext(p) != ".go" {
			continue
		}
		if got, err := os.ReadFile(p); err != nil || !bytes.Equal(got, o.Want[p]) {
			_, _ = fmt.Fprintf(stderr, "velox-gen verify: %s is out of date; run velox-gen\n", genrun.RelPath(dir, p))
			return 3
		}
	}
	types := make([]string, len(gm.Names))
	for i, n := range gm.Names {
		types[i] = "g." + n
	}
	rels := make([]string, len(gm.Rels))
	for i, r := range gm.Rels {
		rels[i] = "g." + r
	}
	// An interrupt stops the check program, and its directory is removed.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	imports := [][2]string{{"g", outPath}}
	return runProgram(ctx, mod, checkProgram(imports, types, rels), env, stdout, stderr)
}
