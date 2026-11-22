package codegen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"

	"strconv"
	"strings"

	"github.com/syssam/velox/internal/codegen/loadjson"
)

// listed is the part of a go list package that Load reads.
type listed struct {
	ImportPath     string
	Name           string
	Dir            string
	GoFiles        []string
	IgnoredGoFiles []string
	Module         *struct{ Dir string }
	Error          *struct{ Err string }
}

// listFields are the fields of listed, for go list.
const listFields = "-json=ImportPath,Name,Dir,GoFiles,IgnoredGoFiles,Module,Error"

// Inspect runs go list for pattern, in the module that holds dir, and reports
// the import path of the one package it names and the types of the package
// that may be schemas, sorted (see candidates). Load decides which are. It
// does not compile or run any of the package's code. env is the environment
// of the go command; nil means os.Environ().
//
// An error says that pattern names no package, more than one, or is not in a
// module, or is one go list reports as it wrote it, such as a go.mod that needs
// go mod tidy, or a directory that holds two packages. It is also a file of
// the package that only some builds include, or an unexported struct that
// embeds schema.Schema and that no type embeds. A package that has no
// candidates is not an error: schemas is empty. name is the package's name.
func Inspect(dir, pattern string, env []string) (importPath, name string, schemas []string, err error) {
	return InspectContext(context.Background(), dir, pattern, env)
}

// InspectContext is Inspect, whose go commands ctx stops.
func InspectContext(ctx context.Context, dir, pattern string, env []string) (importPath, name string, schemas []string, err error) {
	pkg, names, err := inspectContext(ctx, dir, pattern, env)
	if err != nil {
		return "", "", nil, err
	}
	return pkg.ImportPath, pkg.Name, names, nil
}

// inspect is Inspect's work, which returns the package go list reports. A
// file that only some builds include is refused: velox-gen generates one
// model for every build. A file no build includes, such as one behind
// //go:build ignore, and a _test.go file are not read.
func inspect(dir, pattern string, env []string) (*listed, []string, error) {
	return inspectContext(context.Background(), dir, pattern, env)
}

// inspectContext is inspect, whose go commands ctx stops.
func inspectContext(ctx context.Context, dir, pattern string, env []string) (*listed, []string, error) {
	pkg, err := listContext(ctx, dir, pattern, env)
	if err != nil {
		return nil, nil, err
	}
	goVersion := ModuleGoVersion(pkg.Dir)
	files := make(map[string][]byte, len(pkg.GoFiles))
	for _, name := range slices.Concat(pkg.GoFiles, pkg.IgnoredGoFiles) {
		if filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(pkg.Dir, name)
		src, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, err
		}
		if sys, line := GoFileSystems(name, src, goVersion); sys == SomeSystems {
			return nil, nil, &Error{Pos: posString(token.Position{Filename: path, Line: max(line, 1)}),
				Reason: "go builds this file of the schema package only on some systems or with some tags; velox-gen reads the schemas from files that every build includes"}
		}
		if slices.Contains(pkg.GoFiles, name) {
			files[name] = src
		}
	}
	names, err := candidates(files)
	if err != nil {
		return nil, nil, prefixFiles(err, pkg.Dir)
	}
	return pkg, names, nil
}

// prefixFiles puts dir before the file name of each *Error's position in err.
func prefixFiles(err error, dir string) error {
	errs := []error{err}
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		errs = j.Unwrap()
	}
	for _, e := range errs {
		if ge, ok := e.(*Error); ok && ge.Pos != "" && !filepath.IsAbs(ge.Pos) {
			ge.Pos = filepath.Join(dir, ge.Pos)
		}
	}
	return err
}

// Load loads the schema package that pattern names, from the module that
// holds dir, by compiling and running a program that marshals its schemas.
// env is the environment of the go commands it runs; nil means os.Environ().
//
// It finds the schemas by the discovery rule (see classify) in the package's
// export data, and refuses what looks like a schema and is not one. Those
// refusals, and the misuses of the DSL the model reports, such as a
// builder's, are returned as *Error values joined with errors.Join, each with
// the position of the type or element when it has one.
func Load(dir, pattern string, env []string) (*loadjson.Model, error) {
	pkg, cands, err := inspect(dir, pattern, env)
	if err != nil {
		return nil, err
	}
	if len(cands) == 0 {
		return nil, NoSchemas(pkg.ImportPath)
	}
	tmp, err := os.MkdirTemp(pkg.Module.Dir, ".velox-gen-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	// A deferred call does not run when a signal ends the process, as
	// ci-local's fail-fast does: the handler removes tmp then.
	defer removeOnSignal(tmp)()
	exp, err := exports(pkg.Module.Dir, pkg.ImportPath, env)
	if err != nil {
		return nil, fmt.Errorf("loading %s:\n%s", pkg.ImportPath, err)
	}
	names, err := classify(pkg.ImportPath, exp)
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, NoSchemas(pkg.ImportPath)
	}
	if err := os.WriteFile(filepath.Join(tmp, "main.go"), program(pkg.ImportPath, names), 0o644); err != nil {
		return nil, err
	}
	out, stderr, err := run(pkg.Module.Dir, env, "run", "./"+filepath.Base(tmp))
	if err != nil {
		msg := strings.ReplaceAll(stderr, tmp, "velox-gen")
		msg = strings.ReplaceAll(msg, filepath.Base(tmp), "velox-gen")
		// go run's own last line says how the program exited, which the
		// lines before it already explain.
		msg = exitStatus.ReplaceAllString(strings.TrimRight(msg, "\n"), "")
		if strings.TrimSpace(msg) == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("loading %s:\n%s", pkg.ImportPath, strings.TrimRight(msg, "\n"))
	}
	m, err := decodeModel([]byte(out))
	if err != nil {
		return nil, fmt.Errorf("loading %s: %w", pkg.ImportPath, err)
	}
	typeLines(pkg, m)
	if len(m.Errors) > 0 {
		errs := make([]error, len(m.Errors))
		for i, e := range m.Errors {
			errs[i] = &Error{Pos: e.Pos, Schema: e.Schema, Where: e.Where, Reason: e.Reason}
		}
		return nil, errors.Join(errs...)
	}
	return m, nil
}

// typeLines sets each schema's Pos to the line that declares its type in
// the package's files, which stands for the schema better than the line of
// one of its methods, and which a schema that declares no method has too.
func typeLines(pkg *listed, m *loadjson.Model) {
	fset := token.NewFileSet()
	at := map[string]string{}
	for _, name := range pkg.GoFiles {
		f, err := parser.ParseFile(fset, filepath.Join(pkg.Dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			continue // the program that wrote m compiled it
		}
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, sp := range gd.Specs {
				if ts, ok := sp.(*ast.TypeSpec); ok {
					at[ts.Name.Name] = posString(fset.Position(ts.Name.Pos()))
				}
			}
		}
	}
	for _, s := range m.Schemas {
		if p, ok := at[s.Name]; ok {
			s.Pos = p
		}
	}
}

// exitStatus matches the line go run writes last when the program fails.
var exitStatus = regexp.MustCompile(`(^|\n)exit status \d+$`)

// decodeModel decodes the loader's output. It checks the version before it
// decodes the rest, so a model whose shape changed with its version reports
// the version.
func decodeModel(b []byte) (*loadjson.Model, error) {
	var v struct{ Version int }
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, fmt.Errorf("decoding the model: %w", err)
	}
	if v.Version != loadjson.Version {
		return nil, fmt.Errorf("the loader wrote model version %d; velox-gen reads version %d: update velox-gen and the velox module together", v.Version, loadjson.Version)
	}
	var m loadjson.Model
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("decoding the model: %w", err)
	}
	return &m, nil
}

// list runs go list for pattern and returns the one package it names.
func list(dir, pattern string, env []string) (*listed, error) {
	return listContext(context.Background(), dir, pattern, env)
}

// listContext is list, whose go commands ctx stops.
func listContext(ctx context.Context, dir, pattern string, env []string) (*listed, error) {
	out, stderr, err := runContext(ctx, dir, env, "list", listFields, pattern)
	if err != nil {
		msg := strings.TrimSpace(stderr)
		switch {
		case !inModule(ctx, dir, env):
			return nil, errors.New("the schema package must be in a module")
		case strings.Contains(pattern, "..."):
			// Several packages, or none: velox-gen reads exactly one.
			return nil, fmt.Errorf("go list %s: %s", pattern, msg)
		case msg == "":
			return nil, fmt.Errorf("go list %s: %w", pattern, err)
		}
		return nil, errors.New(msg)
	}
	dec := json.NewDecoder(strings.NewReader(out))
	var pkg listed
	if err := dec.Decode(&pkg); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%s names no package", pattern)
		}
		return nil, fmt.Errorf("reading go list output: %w", err)
	}
	if dec.More() {
		return nil, fmt.Errorf("%s names more than one package; name the schema package", pattern)
	}
	if pkg.Error != nil {
		return nil, fmt.Errorf("%s: %s", pattern, pkg.Error.Err)
	}
	if pkg.Module == nil || pkg.Module.Dir == "" {
		return nil, errors.New("the schema package must be in a module")
	}
	return &pkg, nil
}

// inModule reports whether the go command in dir runs in a module.
func inModule(ctx context.Context, dir string, env []string) bool {
	out, _, err := runContext(ctx, dir, env, "env", "GOMOD")
	mod := strings.TrimSpace(out)
	return err != nil || (mod != "" && mod != os.DevNull)
}

// run runs go with args in dir and returns what it wrote to stdout and
// stderr. A nil env means os.Environ().
func run(dir string, env []string, args ...string) (stdout, stderr string, err error) {
	return runContext(context.Background(), dir, env, args...)
}

// runContext is run, whose command ctx stops.
func runContext(ctx context.Context, dir string, env []string, args ...string) (stdout, stderr string, err error) {
	var so, se bytes.Buffer
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir, cmd.Stdout, cmd.Stderr = dir, &so, &se
	if env != nil {
		cmd.Env = env
	}
	err = cmd.Run()
	return so.String(), se.String(), err
}

// program is the main package that marshals the named schemas of the package
// at importPath and prints the result.
func program(importPath string, names []string) []byte {
	var b strings.Builder
	b.WriteString("// Code generated by velox-gen, which deletes it after running.\n\npackage main\n\nimport (\n\t\"fmt\"\n\t\"os\"\n\n")
	b.WriteString("\t\"github.com/syssam/velox/schema\"\n\t\"github.com/syssam/velox/schema/load\"\n")
	b.WriteString("\ts " + strconv.Quote(importPath) + "\n)\n\nfunc main() {\n")
	b.WriteString("\tb, err := load.Marshal(" + strconv.Quote(importPath) + ", []schema.Interface{")
	for i, n := range names {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString("new(s." + n + ")")
	}
	b.WriteString("}...)\n\tif err != nil {\n\t\tfmt.Fprintln(os.Stderr, err)\n\t\tos.Exit(1)\n\t}\n\tos.Stdout.Write(b)\n}\n")
	return []byte(b.String())
}

// schemaName returns the name f gives the schema package, or "" when f does
// not import it by a usable name: a dot or underscore import does not count.
func schemaName(f *ast.File) string {
	for _, imp := range f.Imports {
		if p, err := strconv.Unquote(imp.Path.Value); err != nil || p != loadjson.SchemaPath {
			continue
		}
		switch {
		case imp.Name == nil:
			return "schema"
		case imp.Name.Name == "." || imp.Name.Name == "_":
			return ""
		default:
			return imp.Name.Name
		}
	}
	return ""
}

// embedsSchema reports whether st embeds <local>.Schema, or *<local>.Schema
// when ptr is set.
func embedsSchema(st *ast.StructType, local string, ptr bool) bool {
	if local == "" {
		return false
	}
	for _, fld := range st.Fields.List {
		if len(fld.Names) != 0 {
			continue
		}
		typ := fld.Type
		if star, ok := typ.(*ast.StarExpr); ok != ptr {
			continue
		} else if ok {
			typ = star.X
		}
		if sel, ok := typ.(*ast.SelectorExpr); ok && sel.Sel.Name == "Schema" {
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == local {
				return true
			}
		}
	}
	return false
}
