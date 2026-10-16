package gen

import (
	"bytes"
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
	"sort"
	"strconv"
	"strings"

	"github.com/syssam/velox2/schema/load"
)

// listed is the part of a go list package that Load reads.
type listed struct {
	ImportPath string
	Name       string
	Dir        string
	GoFiles    []string
	Module     *struct{ Dir string }
	Error      *struct{ Err string }
}

// ErrNotPackage is wrapped by the error of Inspect and Load when the pattern
// does not name exactly one package of a module, as opposed to a package that
// cannot be read.
var ErrNotPackage = errors.New("not a package")

// notPackage is an error that Is ErrNotPackage and keeps its own message.
type notPackage struct{ error }

func (e notPackage) Unwrap() error      { return e.error }
func (notPackage) Is(target error) bool { return target == ErrNotPackage }

// Inspect runs go list for pattern, in the module that holds dir, and reports
// the import path of the one package it names and the schemas the package
// declares: the exported structs that embed schema.Schema, sorted. It does not
// run any of the package's code. env is the environment of the go command; nil
// means os.Environ().
//
// An error that wraps ErrNotPackage says that pattern names no package, more
// than one, or one outside a module. A package that has no schemas is not an
// error: schemas is empty.
func Inspect(dir, pattern string, env []string) (importPath string, schemas []string, err error) {
	pkg, names, err := inspect(dir, pattern, env)
	if err != nil {
		return "", nil, err
	}
	return pkg.ImportPath, names, nil
}

func inspect(dir, pattern string, env []string) (*listed, []string, error) {
	pkg, err := list(dir, pattern, env)
	if err != nil {
		return nil, nil, err
	}
	files := make(map[string][]byte, len(pkg.GoFiles))
	for _, name := range pkg.GoFiles {
		src, err := os.ReadFile(filepath.Join(pkg.Dir, name))
		if err != nil {
			return nil, nil, err
		}
		files[name] = src
	}
	names, err := discover(files)
	if err != nil {
		return nil, nil, err
	}
	return pkg, names, nil
}

// Load loads the schema package that pattern names, from the module that
// holds dir, by compiling and running a program that marshals its schemas.
// env is the environment of the go commands it runs; nil means os.Environ().
func Load(dir, pattern string, env []string) (*load.Model, error) {
	pkg, names, err := inspect(dir, pattern, env)
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("no schemas in %s: a schema is a struct that embeds schema.Schema", pkg.ImportPath)
	}
	tmp, err := os.MkdirTemp(pkg.Module.Dir, ".velox-gen-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if err := os.WriteFile(filepath.Join(tmp, "main.go"), program(pkg.ImportPath, names), 0o644); err != nil {
		return nil, err
	}
	out, stderr, err := run(pkg.Module.Dir, env, "run", "./"+filepath.Base(tmp))
	if err != nil {
		msg := strings.ReplaceAll(stderr, tmp, "velox-gen")
		msg = strings.ReplaceAll(msg, filepath.Base(tmp), "velox-gen")
		if strings.TrimSpace(msg) == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("loading %s:\n%s", pkg.ImportPath, strings.TrimRight(msg, "\n"))
	}
	m, err := decodeModel([]byte(out))
	if err != nil {
		return nil, fmt.Errorf("loading %s: %w", pkg.ImportPath, err)
	}
	return m, nil
}

// decodeModel decodes the loader's output. It checks the version before it
// decodes the rest, so a model whose shape changed with its version reports
// the version.
func decodeModel(b []byte) (*load.Model, error) {
	var v struct{ Version int }
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, fmt.Errorf("decoding the model: %w", err)
	}
	if v.Version != load.Version {
		return nil, fmt.Errorf("the loader wrote model version %d; velox-gen reads version %d: update velox-gen and the velox2 module together", v.Version, load.Version)
	}
	var m load.Model
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("decoding the model: %w", err)
	}
	return &m, nil
}

// list runs go list for pattern and returns the one package it names.
func list(dir, pattern string, env []string) (*listed, error) {
	out, stderr, err := run(dir, env, "list", "-json=ImportPath,Name,Dir,GoFiles,Module,Error", pattern)
	if err != nil {
		return nil, notPackage{fmt.Errorf("go list %s: %w\n%s", pattern, err, strings.TrimRight(stderr, "\n"))}
	}
	dec := json.NewDecoder(strings.NewReader(out))
	var pkg listed
	if err := dec.Decode(&pkg); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, notPackage{fmt.Errorf("%s names no package", pattern)}
		}
		return nil, fmt.Errorf("reading go list output: %w", err)
	}
	if dec.More() {
		return nil, notPackage{fmt.Errorf("%s names more than one package; name the schema package", pattern)}
	}
	if pkg.Error != nil {
		return nil, notPackage{fmt.Errorf("%s: %s", pattern, pkg.Error.Err)}
	}
	if pkg.Module == nil || pkg.Module.Dir == "" {
		return nil, notPackage{errors.New("the schema package must be in a module")}
	}
	return &pkg, nil
}

// run runs go with args in dir and returns what it wrote to stdout and
// stderr. A nil env means os.Environ().
func run(dir string, env []string, args ...string) (stdout, stderr string, err error) {
	var so, se bytes.Buffer
	cmd := exec.Command("go", args...)
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
	b.WriteString("\t\"github.com/syssam/velox2/schema\"\n\t\"github.com/syssam/velox2/schema/load\"\n")
	b.WriteString("\ts " + strconv.Quote(importPath) + "\n)\n\nfunc main() {\n")
	b.WriteString("\tb, err := load.Marshal(" + strconv.Quote(importPath) + ", []schema.Interface{")
	for i, n := range names {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString("s." + n + "{}")
	}
	b.WriteString("}...)\n\tif err != nil {\n\t\tfmt.Fprintln(os.Stderr, err)\n\t\tos.Exit(1)\n\t}\n\tos.Stdout.Write(b)\n}\n")
	return []byte(b.String())
}

// discover returns the exported struct types of the package's files that
// embed schema.Schema, sorted, and an error for an unexported one. Only a type
// that embeds schema.Schema directly is found: a type that embeds another
// struct which embeds schema.Schema is not, and is not loaded.
func discover(files map[string][]byte) ([]string, error) {
	fnames := make([]string, 0, len(files))
	for name := range files {
		fnames = append(fnames, name)
	}
	sort.Strings(fnames)
	fset := token.NewFileSet()
	seen := map[string]bool{}
	var out []string
	for _, fname := range fnames {
		f, err := parser.ParseFile(fset, fname, files[fname], parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		local := schemaName(f)
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, sp := range gd.Specs {
				ts := sp.(*ast.TypeSpec)
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					continue
				}
				if embedsSchema(st, local, true) {
					return nil, fmt.Errorf("schema type %s must embed schema.Schema by value, not a pointer", ts.Name.Name)
				}
				if !embedsSchema(st, local, false) {
					continue
				}
				if !ts.Name.IsExported() {
					return nil, fmt.Errorf("schema type %s is unexported; a schema must be exported", ts.Name.Name)
				}
				if !seen[ts.Name.Name] {
					seen[ts.Name.Name] = true
					out = append(out, ts.Name.Name)
				}
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// schemaName returns the name f gives the schema package, or "" when f does
// not import it by a usable name: a dot or underscore import does not count.
func schemaName(f *ast.File) string {
	for _, imp := range f.Imports {
		if p, err := strconv.Unquote(imp.Path.Value); err != nil || p != load.SchemaPath {
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
