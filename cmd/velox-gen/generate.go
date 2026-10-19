package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/syssam/velox2/internal/gen"
)

// defaultSchema is the schema package when no package is named.
const defaultSchema = "./schema"

// schemaPackage decides the mode (Ruling 1). The new mode runs when exactly one
// pattern is named, or none, which means ./schema, and the package declares
// schemas; it returns that package's pattern. A pattern with "...", or that
// does not name exactly one package of a module, is the old mode: ok is false.
// So is a package named by an import path that declares no schemas.
//
// An error is a package that cannot be read, as go list says it, or a
// directory with Go files and no schemas, which is a mistake in the new mode
// unless it holds old-mode entities (a method Schema).
func schemaPackage(dir string, patterns, env []string) (pattern string, ok bool, err error) {
	switch len(patterns) {
	case 0:
		pattern = defaultSchema
	case 1:
		pattern = patterns[0]
	default:
		return "", false, nil
	}
	if strings.Contains(pattern, "...") {
		return "", false, nil // several packages: the old mode reads them all
	}
	importPath, schemas, err := gen.Inspect(dir, pattern, env)
	if errors.Is(err, gen.ErrNotPackage) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if len(schemas) == 0 {
		if d := patternDir(dir, pattern); d != "" && !declaresOldSchema(d) {
			return "", false, fmt.Errorf("no schemas in %s: a schema is a struct that embeds schema.Schema", importPath)
		}
		return "", false, nil
	}
	return pattern, true, nil
}

// patternDir is the directory that pattern names, relative to dir when it is
// not absolute, or "" when pattern is not the path of a directory (an import
// path, say).
func patternDir(dir, pattern string) string {
	if !filepath.IsAbs(pattern) {
		pattern = filepath.Join(dir, pattern)
	}
	if fi, err := os.Stat(pattern); err != nil || !fi.IsDir() {
		return ""
	}
	return pattern
}

// declaresOldSchema reports whether a file of the package in dir declares a
// method Schema: a package of the old mode, which the old scanner reads. It
// does not look at the method's signature; the old scanner reports a wrong one.
func declaresOldSchema(dir string) bool {
	des, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	fset := token.NewFileSet()
	for _, de := range des {
		name := de.Name()
		if de.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv != nil && fd.Name.Name == "Schema" {
				return true
			}
		}
	}
	return false
}

// genFlags are the flags of the new mode.
type genFlags struct {
	out, pkg string
	check    bool
	rerun    []string // the flags the user set, as a command line, for the -check hint
}

// outDir is the absolute path of the output directory. It is absolute so that
// go list reads it as a directory: a relative "gen" would be an import path.
// Symlinks in dir are resolved, as relPath does, so that paths print relative.
func (f genFlags) outDir(dir string) string {
	if filepath.IsAbs(f.out) {
		return filepath.Clean(f.out)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	} else if r, err := filepath.EvalSymlinks(abs); err == nil {
		abs = r
	}
	return filepath.Join(abs, f.out)
}

// packageName is -package, or the base name of -out.
func (f genFlags) packageName(dir string) (string, error) {
	name := f.pkg
	if name == "" {
		name = filepath.Base(f.outDir(dir))
	}
	if !token.IsIdentifier(name) {
		return "", fmt.Errorf("%q is not a package name; name it with -package", name)
	}
	return name, nil
}

// genModel is the generator's view of a schema package: what Lower made of its
// graph, and the names of every generated entity, join entities included.
type genModel struct {
	graph      *gen.Graph
	importPath string
	names      []string
	rels       []string // every relation's handle, e.g. "Users.Posts", join entities included
}

// loadModel loads and builds the schema package at pattern and lowers it to
// the names of its generated entities.
func loadModel(dir, pattern, pkg string, env []string) (*genModel, error) {
	importPath, _, err := gen.Inspect(dir, pattern, env)
	if err != nil {
		return nil, err
	}
	m, err := gen.Load(dir, pattern, env)
	if err != nil {
		return nil, err
	}
	g, err := gen.Build(m)
	if err != nil {
		return nil, err
	}
	o, err := gen.Lower(g, gen.Config{Package: pkg, SchemaPath: importPath})
	if err != nil {
		return nil, err
	}
	gm := &genModel{graph: g, importPath: importPath}
	for _, e := range o.Entities {
		gm.names = append(gm.names, e.Name)
		for _, r := range e.Rels {
			gm.rels = append(gm.rels, e.Handles+"."+r.GoName)
		}
	}
	return gm, nil
}

// runGenerate is the new mode: it generates the gen package of the schema
// package at pattern.
func runGenerate(f genFlags, pattern, dir string, env []string, stdout, stderr io.Writer) int {
	fail := func(err error) int {
		_, _ = fmt.Fprintf(stderr, "velox-gen: %v\n", err)
		return 1
	}
	pkg, err := f.packageName(dir)
	if err != nil {
		return fail(err)
	}
	gm, err := loadModel(dir, pattern, pkg, env)
	if err != nil {
		return fail(err)
	}
	out := f.outDir(dir)
	bound, err := gen.Methods(out, gm.names)
	if fe := (*gen.FileError)(nil); errors.As(err, &fe) {
		return fail(fmt.Errorf("%s: %s", relPath(dir, fe.Path), fe.Reason))
	}
	if err != nil {
		return fail(err)
	}
	files, err := gen.Generate(gm.graph, gen.Config{Package: pkg, SchemaPath: gm.importPath, Bound: bound})
	if err != nil {
		return fail(err)
	}
	want := make(map[string][]byte, len(files))
	for _, file := range files {
		want[filepath.Join(out, file.Name)] = file.Data
	}
	// Nothing is written before every path is known to be velox-gen's.
	for _, p := range slices.Sorted(maps.Keys(want)) {
		if _, err := os.Stat(p); err == nil && !isGenerated(p) {
			return fail(fmt.Errorf("%s was not written by velox-gen; move it", relPath(dir, p)))
		}
	}
	stale, remove, err := staleFiles(out, want)
	if err != nil {
		return fail(err)
	}
	changed := append(slices.Clone(stale), remove...)
	slices.Sort(changed)
	if f.check {
		for _, p := range changed {
			_, _ = fmt.Fprintf(stderr, "velox-gen: %s is out of date\n", relPath(dir, p))
		}
		if len(changed) > 0 {
			_, _ = fmt.Fprintf(stderr, "velox-gen: run %s\n", strings.TrimSpace("go tool velox-gen "+strings.Join(f.rerun, " ")))
			return 1
		}
		return 0
	}
	if len(stale) > 0 {
		if err := os.MkdirAll(out, 0o755); err != nil {
			return fail(err)
		}
	}
	for _, p := range stale {
		if err := os.WriteFile(p, want[p], 0o644); err != nil {
			return fail(err)
		}
	}
	for _, p := range remove {
		if err := os.Remove(p); err != nil {
			return fail(err)
		}
	}
	for _, p := range changed {
		_, _ = fmt.Fprintln(stdout, relPath(dir, p))
	}
	return 0
}

// staleFiles compares want, the files that must be in out, with the disk. It
// returns the paths to write (missing, or with other content) and the
// generated files in out that no entity produces now, to remove. Only a file
// that starts with velox-gen's header is ever on the second list.
func staleFiles(out string, want map[string][]byte) (write, remove []string, err error) {
	for p, data := range want {
		if got, err := os.ReadFile(p); err != nil || !bytes.Equal(got, data) {
			write = append(write, p)
		}
	}
	des, err := os.ReadDir(out)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, nil, err
	}
	for _, de := range des {
		p := filepath.Join(out, de.Name())
		if _, ok := want[p]; !ok && !de.IsDir() && strings.HasSuffix(p, ".go") && isGenerated(p) {
			remove = append(remove, p)
		}
	}
	slices.Sort(write)
	slices.Sort(remove)
	return write, remove, nil
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
	pkg, err := f.packageName(dir)
	if err != nil {
		return fail(err)
	}
	gm, err := loadModel(dir, pattern, pkg, env)
	if err != nil {
		return fail(err)
	}
	out := f.outDir(dir)
	if fi, err := os.Stat(out); err != nil || !fi.IsDir() {
		return fail(fmt.Errorf("%s is not a directory; run velox-gen to generate it first", relPath(dir, out)))
	}
	outPath, _, err := gen.Inspect(dir, out, env)
	if err != nil {
		return fail(err)
	}
	mod, err := moduleDir(dir, out, env)
	if err != nil {
		return fail(err)
	}
	types := make([]string, len(gm.names))
	for i, n := range gm.names {
		types[i] = "g." + n
	}
	rels := make([]string, len(gm.rels))
	for i, r := range gm.rels {
		rels[i] = "g." + r
	}
	return runProgram(mod, checkProgram([][2]string{{"g", outPath}}, types, rels), env, stdout, stderr)
}

// moduleDir is the directory of the module that holds the package at pattern.
func moduleDir(dir, pattern string, env []string) (string, error) {
	cmd := exec.Command("go", "list", "-f", "{{.Module.Dir}}", pattern)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = env
	}
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("go list %s: %w\n%s", pattern, err, strings.TrimRight(se.String(), "\n"))
	}
	mod := strings.TrimSpace(so.String())
	if mod == "" {
		return "", fmt.Errorf("%s is not in a module", pattern)
	}
	return mod, nil
}

// usage prints the synopsis and the flags of velox-gen.
func usage(fs *flag.FlagSet) func() {
	return func() {
		w := fs.Output()
		_, _ = fmt.Fprint(w, `Usage:
  velox-gen [-out dir] [-package name] [-check] [package]
        generate the gen package of a schema package (default ./schema)
  velox-gen verify -out dir [package]
        check every generated entity's metadata
  velox-gen describe [-json] [package]
        print the entities, columns and relations of a schema package
  velox-gen [-check] [-ordered=path.Type,...] [-number=path.Type,...] [patterns]
        the old mode, for packages with no schemas; removed in schema plan 4

Flags:
`)
		fs.PrintDefaults()
	}
}
