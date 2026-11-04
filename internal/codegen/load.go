package codegen

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"

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
	Imports        []string
	Module         *struct{ Dir string }
	Error          *struct{ Err string }
}

// listFields are the fields of listed, for go list.
const listFields = "-json=ImportPath,Name,Dir,GoFiles,IgnoredGoFiles,Imports,Module,Error"

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
// candidates is not an error: schemas is empty.
func Inspect(dir, pattern string, env []string) (importPath string, schemas []string, err error) {
	pkg, names, _, err := inspect(dir, pattern, env)
	if err != nil {
		return "", nil, err
	}
	return pkg.ImportPath, names, nil
}

// inspect is Inspect's work, which also returns the package's files by name.
// A file that only some builds include is refused: velox-gen generates one
// model for every build. A file no build includes, such as one behind
// //go:build ignore, and a _test.go file are not read.
func inspect(dir, pattern string, env []string) (*listed, []string, map[string][]byte, error) {
	pkg, err := list(dir, pattern, env)
	if err != nil {
		return nil, nil, nil, err
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
			return nil, nil, nil, err
		}
		if sys, line := GoFileSystems(name, src, goVersion); sys == SomeSystems {
			return nil, nil, nil, &Error{Pos: posString(token.Position{Filename: path, Line: max(line, 1)}),
				Reason: "go builds this file of the schema package only on some systems or with some tags; velox-gen reads the schemas from files that every build includes"}
		}
		if slices.Contains(pkg.GoFiles, name) {
			files[name] = src
		}
	}
	names, err := candidates(files)
	if err != nil {
		return nil, nil, nil, prefixFiles(err, pkg.Dir)
	}
	return pkg, names, files, nil
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
	pkg, cands, files, err := inspect(dir, pattern, env)
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
	overlay, originals, err := writeOverlay(tmp, pkg, cands, files, env)
	if err != nil {
		return nil, err
	}
	exp, err := exports(pkg.Module.Dir, pkg.ImportPath, overlay, env)
	if err != nil {
		return nil, fmt.Errorf("loading %s:\n%s", pkg.ImportPath, unoverlay(err.Error(), originals, pkg.Module.Dir))
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
	args := []string{"run"}
	if overlay != "" {
		args = append(args, "-overlay="+overlay)
	}
	out, stderr, err := run(pkg.Module.Dir, env, append(args, "./"+filepath.Base(tmp))...)
	if err != nil {
		msg := strings.ReplaceAll(unoverlay(stderr, originals, pkg.Module.Dir), tmp, "velox-gen")
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
	if len(m.Errors) > 0 {
		errs := make([]error, len(m.Errors))
		for i, e := range m.Errors {
			errs[i] = &Error{Pos: e.Pos, Schema: e.Schema, Where: e.Where, Reason: e.Reason}
		}
		return nil, errors.Join(errs...)
	}
	return m, nil
}

// writeOverlay writes, in tmp, the schema package's files without the
// entities' behaviour (see prune) and the go command's overlay file that puts
// them in place of the package's, and returns the overlay file's path; ""
// when the package declares no behaviour, which needs no overlay. originals
// maps each rewritten file's path to the user's file it stands for.
func writeOverlay(tmp string, pkg *listed, schemas []string, files map[string][]byte, env []string) (overlay string, originals map[string]string, err error) {
	var names map[string]string
	if len(pkg.Imports) > 0 {
		names = listNames(pkg.Module.Dir, pkg.Imports, env)
	}
	pruned, err := prune(files, schemas, names)
	if err != nil || len(pruned) == 0 {
		return "", nil, err
	}
	// The files go below tmp, with a name go does not read as Go: tmp holds
	// the program's package, which must have only its own files.
	dir := filepath.Join(tmp, "overlay")
	if err := os.Mkdir(dir, 0o755); err != nil {
		return "", nil, err
	}
	replace := map[string]string{}
	originals = map[string]string{}
	for name, src := range pruned {
		p := filepath.Join(dir, name+".txt")
		if err := os.WriteFile(p, src, 0o644); err != nil {
			return "", nil, err
		}
		replace[filepath.Join(pkg.Dir, name)] = p
		originals[p] = filepath.Join(pkg.Dir, name)
	}
	b, err := json.Marshal(struct{ Replace map[string]string }{replace})
	if err != nil {
		return "", nil, err
	}
	p := filepath.Join(tmp, "overlay.json")
	return p, originals, os.WriteFile(p, b, 0o644)
}

// unoverlay replaces, in msg, the paths of the rewritten files in originals
// with the user's files they stand for. The go command writes a path below
// modDir relative to it, as ./x; the rewrite keeps every line where it was,
// so a position stays true with the user's path.
func unoverlay(msg string, originals map[string]string, modDir string) string {
	for p, orig := range originals {
		msg = strings.ReplaceAll(msg, p, orig)
		rp, err1 := filepath.Rel(modDir, p)
		ro, err2 := filepath.Rel(modDir, orig)
		if err1 == nil && err2 == nil {
			msg = strings.ReplaceAll(msg, "./"+filepath.ToSlash(rp), "./"+filepath.ToSlash(ro))
			msg = strings.ReplaceAll(msg, filepath.ToSlash(rp), filepath.ToSlash(ro))
		}
	}
	return msg
}

// listNames returns the package names of the import paths, as go list
// reports them. A path go list cannot name, such as a generated package that
// does not exist yet, is left out. It is best effort: on an error, the result
// is empty, and prune guesses every name.
func listNames(dir string, imports []string, env []string) map[string]string {
	out, _, _ := run(dir, env, append([]string{"list", "-e", "-json=ImportPath,Name"}, imports...)...)
	names := map[string]string{}
	dec := json.NewDecoder(strings.NewReader(out))
	for {
		var p struct{ ImportPath, Name string }
		if dec.Decode(&p) != nil {
			return names
		}
		if p.Name != "" {
			names[p.ImportPath] = p.Name
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
	out, stderr, err := run(dir, env, "list", listFields, pattern)
	if err != nil {
		if pkg := listCycle(dir, pattern, env); pkg != nil {
			return pkg, nil
		}
		msg := strings.TrimSpace(stderr)
		switch {
		case !inModule(dir, env):
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

// listCycle is list's way past an import cycle through the schema package:
// one that declares Hooks or Policy imports the generated package, which,
// generated before the schema declared them, imports the schema package.
// go list refuses the cycle, but the loader compiles the package without what
// imports the generated one (see prune), and the generation that follows
// breaks the cycle. It returns the package go list -e reports when the
// package's own error is in an import, its import stack longer than the
// package; nil otherwise, and list reports go list's error.
func listCycle(dir, pattern string, env []string) *listed {
	out, _, err := run(dir, env, "list", "-e", listFields, pattern)
	if err != nil {
		return nil
	}
	var pkg struct {
		listed
		Error *struct {
			ImportStack []string
			Err         string
		}
	}
	dec := json.NewDecoder(strings.NewReader(out))
	if dec.Decode(&pkg) != nil || dec.More() {
		return nil
	}
	if pkg.Error == nil || len(pkg.Error.ImportStack) < 2 || !strings.Contains(pkg.Error.Err, "import cycle") {
		return nil
	}
	if pkg.Name == "" || len(pkg.GoFiles) == 0 || pkg.Module == nil || pkg.Module.Dir == "" {
		return nil
	}
	pkg.listed.Error = nil
	return &pkg.listed
}

// inModule reports whether the go command in dir runs in a module.
func inModule(dir string, env []string) bool {
	out, _, err := run(dir, env, "env", "GOMOD")
	mod := strings.TrimSpace(out)
	return err != nil || (mod != "" && mod != os.DevNull)
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
