package main

import (
	"bufio"
	"flag"
	"fmt"
	"go/ast"
	"go/types"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

func main() {
	if err := run(os.Args[1:], os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "vxmigrate:", err)
		os.Exit(1)
	}
}

// run is the command: its arguments, and where it reports to.
func run(args []string, stderr io.Writer) error {
	flags := flag.NewFlagSet("vxmigrate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dir := flags.String("dir", ".", "the application's module directory")
	root := flags.String("generated", "", "import path of velox's generated root package, e.g. example.com/app/velox")
	to := flags.String("to", "", "import path of the root package vx generates, when it is not the same")
	lib := flags.String("lib", "github.com/syssam/velox", "import path of the velox module")
	out := flags.String("inventory", "", "write what the application uses of the old API to this file, tab-separated")
	showErrors := flags.Bool("errors", false, "print the type errors found in the application's own packages")
	write := flags.Bool("write", false, "rewrite the application's files for vx's generated code, in place")
	report := flags.String("report", "", "write what has to be rewritten by hand to this file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *root == "" {
		return fmt.Errorf("-generated is required")
	}
	if *to == "" {
		*to = *root
	}
	patterns := flags.Args()
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}
	start := time.Now()
	p, err := load(*dir, patterns, func(path string) bool { return path == *root || strings.HasPrefix(path, *root+"/") })
	if err != nil {
		return err
	}
	files, errs := 0, 0
	for _, k := range p.own {
		files += len(k.files)
		errs += k.errs
		for _, t := range k.tests {
			files += len(t.files)
			errs += len(t.errs)
		}
	}
	if *showErrors {
		for _, k := range p.own {
			for _, e := range k.all {
				fmt.Fprintln(stderr, "  ", e)
			}
			for _, t := range k.tests {
				for _, e := range t.errs {
					fmt.Fprintln(stderr, "   test:", e)
				}
			}
		}
	}
	fmt.Fprintf(stderr, "vxmigrate: read %d packages, %d of the application's (%d files, %d type errors) in %s\n",
		len(p.pkgs), len(p.own), files, errs, time.Since(start).Round(time.Second))
	s, err := readSchema(p, *root)
	if err != nil {
		return err
	}
	s.to = *to
	fmt.Fprintf(stderr, "vxmigrate: %d entities\n", len(s.entities))
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			return err
		}
		w := bufio.NewWriter(f)
		inventory(p, s, *lib, w)
		if err := w.Flush(); err != nil {
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
	}
	if !*write && *report == "" {
		return nil
	}
	defaultName := func(path string) string {
		if k := p.pkgs[path]; k != nil && k.types != nil {
			return k.types.Name()
		}
		return path[strings.LastIndex(path, "/")+1:]
	}
	done := map[string]int{}
	var notes []string
	changed, failed := 0, 0
	rewrite := func(files []*ast.File, info *types.Info, pkg *types.Package) error {
		for _, file := range files {
			if ast.IsGenerated(file) {
				continue
			}
			fe, err := newFileEdits(p, file, info, pkg, s)
			if err != nil {
				return err
			}
			r := &rewriter{fileEdits: fe, s: s, lib: *lib, done: done, sets: map[*ast.CallExpr]bool{}}
			if k := p.pkgs[*lib]; k != nil && k.types != nil {
				r.libScope = k.types.Scope()
			}
			r.run()
			notes = append(notes, fe.notes...)
			if len(fe.edits) == 0 && len(fe.imports) == 0 && !fe.moved {
				continue
			}
			out, err := fe.finish(defaultName)
			if err != nil {
				failed++
				notes = append(notes, fmt.Sprintf("%s: %v", fe.path, err))
				continue
			}
			changed++
			if *write {
				if err := os.WriteFile(fe.path, out, 0o644); err != nil { //nolint:gosec // a source file of the application
					return err
				}
			}
		}
		return nil
	}
	for _, k := range p.own {
		if err := rewrite(k.files, k.info, k.types); err != nil {
			return err
		}
		for _, t := range k.tests {
			if err := rewrite(t.files, t.info, t.types); err != nil {
				return err
			}
		}
	}
	total := 0
	rules := make([]string, 0, len(done))
	for rule, n := range done {
		rules = append(rules, rule)
		total += n
	}
	sort.Slice(rules, func(i, j int) bool { return done[rules[i]] > done[rules[j]] })
	fmt.Fprintf(stderr, "vxmigrate: %d references rewritten in %d files (%d files did not parse afterwards), %d left for a person\n", total, changed, failed, len(notes))
	for _, rule := range rules {
		fmt.Fprintf(stderr, "  %6d  %s\n", done[rule], rule)
	}
	if *report != "" {
		sort.Strings(notes)
		if err := os.WriteFile(*report, []byte(strings.Join(notes, "\n")+"\n"), 0o644); err != nil { //nolint:gosec // a report for the person migrating
			return err
		}
	}
	return nil
}
