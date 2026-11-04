package codegen

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// FileError is a problem in one of the user's files. Error prints the path as
// it was found; a caller that knows the working directory prints Path its own
// way.
type FileError struct {
	Path   string
	Line   int // the line of the problem, or 0 for the file's
	Reason string
}

// Error returns "path: reason", or "path:line: reason".
func (e *FileError) Error() string {
	if e.Line > 0 {
		return e.Path + ":" + strconv.Itoa(e.Line) + ": " + e.Reason
	}
	return e.Path + ": " + e.Reason
}

// CheckUserFiles reads the non-test .go files in dir, the generated package,
// that do not start with velox-gen's header. Such a file may add to the
// package, but not what velox-gen writes or binds: a file that declares a
// type named like an entity (its methods would clash with the generated
// ones), or one of reserved, the package-level names velox-gen writes such as
// a scope key's variable, is a *FileError. So is a Hooks or Policy method on
// an entity: those are declared in the schema package (see SchemaMethods),
// and the generated code binds them. A file no build includes is not read, and
// one only some builds include is a *FileError (see userFiles). A missing dir
// is no files.
func CheckUserFiles(dir string, entities, reserved []string) error {
	return userFiles(dir, 0, func(fset *token.FileSet, path string, f *ast.File) error {
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.GenDecl:
				for _, sp := range d.Specs {
					switch sp := sp.(type) {
					case *ast.TypeSpec:
						if n := sp.Name.Name; slices.Contains(entities, n) {
							return &FileError{Path: path, Reason: "declares type " + n + ", which velox-gen generates"}
						} else if slices.Contains(reserved, n) {
							return &FileError{Path: path, Reason: "declares " + n + ", a name velox-gen generates"}
						}
					case *ast.ValueSpec:
						for _, id := range sp.Names {
							if slices.Contains(reserved, id.Name) {
								return &FileError{Path: path, Reason: "declares " + id.Name + ", a name velox-gen generates"}
							}
						}
					}
				}
			case *ast.FuncDecl:
				if d.Recv == nil && slices.Contains(reserved, d.Name.Name) {
					return &FileError{Path: path, Reason: "declares " + d.Name.Name + ", a name velox-gen generates"}
				}
				e, _ := receiver(d)
				if e == "" || !slices.Contains(entities, e) || !slices.Contains(behaviourMethods, d.Name.Name) {
					continue
				}
				m := d.Name.Name
				want := "[]velox.Hook[" + f.Name.Name + "." + e + "]"
				if m == "Policy" {
					want = "velox.Policy[" + f.Name.Name + "." + e + "]"
				}
				return &FileError{Path: path, Line: fset.Position(d.Pos()).Line, Reason: fmt.Sprintf(
					"declares %s.%s in the generated package; declare it in the schema package, on %[1]s: func (%[1]s) %[2]s() %[3]s", e, m, want)}
			}
		}
		return nil
	})
}

// HasPackageDoc reports whether one of the non-test .go files in dir that do
// not start with velox-gen's header has a package comment with text, not only
// directives. A file no build includes does not count, and one only some
// builds include is a *FileError (see userFiles). A missing dir is no files.
func HasPackageDoc(dir string) (bool, error) {
	found := false
	err := userFiles(dir, parser.PackageClauseOnly|parser.ParseComments, func(_ *token.FileSet, _ string, f *ast.File) error {
		if f.Doc != nil && strings.TrimSpace(f.Doc.Text()) != "" {
			found = true
		}
		return nil
	})
	return found, err
}

// userFiles parses, with mode, each non-test .go file in dir that does not
// start with velox-gen's header, and calls fn with it. Whatever system
// velox-gen runs on, it leaves out a file that no build includes
// (GoFileSystems, at the go version of dir's go.mod): //go:build ignore, or a
// name that starts with _ or .. It refuses, as a *FileError, one that only
// some builds include, such as one behind a GOOS or a custom tag: the
// generated code, built on another system or with other tags, would bind what
// the package there does not have, or would leave a policy unbound in a build
// that has it. A missing dir is no files.
func userFiles(dir string, mode parser.Mode, fn func(fset *token.FileSet, path string, f *ast.File) error) error {
	des, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	goVersion := ModuleGoVersion(dir)
	fset := token.NewFileSet()
	for _, de := range des {
		name := de.Name()
		if de.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.HasPrefix(src, []byte(strings.TrimSuffix(header, "\n\n")+"\n")) {
			continue
		}
		switch sys, line := GoFileSystems(name, src, goVersion); sys {
		case NoSystem:
			continue
		case SomeSystems:
			return &FileError{Path: path, Line: line, Reason: "go leaves it out of some builds; velox-gen reads hooks and policies only from files that every build includes"}
		}
		f, err := parser.ParseFile(fset, path, src, mode|parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		if err := fn(fset, path, f); err != nil {
			return err
		}
	}
	return nil
}

// policyConds returns the position of the first Read or Write key, with a
// value other than nil, of a composite literal of type velox.Policy[gen.e] in
// the body of d, the Policy method of entity e in file f, and token.NoPos when
// there is none. velox is the name f imports the module under. A Policy
// built another way, by a positional literal, by assigning its fields or in
// another function, is left to the runtime's check when the entity loads.
func policyConds(f *ast.File, d *ast.FuncDecl, e string) token.Pos {
	name := ""
	for _, im := range f.Imports {
		if p, _ := strconv.Unquote(im.Path.Value); p == "github.com/syssam/velox" {
			name = "velox"
			if im.Name != nil {
				name = im.Name.Name
			}
		}
	}
	if name == "" || d.Body == nil {
		return token.NoPos
	}
	found := token.NoPos
	ast.Inspect(d.Body, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok || found.IsValid() {
			return !found.IsValid()
		}
		if !isPolicyType(lit.Type, name, e) {
			return true
		}
		for _, el := range lit.Elts {
			if kv, ok := el.(*ast.KeyValueExpr); ok {
				if v, ok := kv.Value.(*ast.Ident); ok && v.Name == "nil" {
					continue
				}
				if k, ok := kv.Key.(*ast.Ident); ok && (k.Name == "Read" || k.Name == "Write") {
					found = k.Pos()
					return false
				}
			}
		}
		return true
	})
	return found
}

// isPolicyType reports whether t is <velox>.Policy[e] or <velox>.Policy[<gen>.e].
func isPolicyType(t ast.Expr, velox, e string) bool {
	ix, ok := t.(*ast.IndexExpr)
	if !ok {
		return false
	}
	if arg, ok := ix.Index.(*ast.Ident); (!ok || arg.Name != e) && !entityArg(ix.Index, e) {
		return false
	}
	sel, ok := ix.X.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Policy" {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == velox
}

// receiver is the type name of d's receiver, E for (E) and (*E), and whether it
// is a pointer. The name is "" for a function or a receiver of another shape.
func receiver(d *ast.FuncDecl) (name string, ptr bool) {
	if d.Recv == nil || len(d.Recv.List) != 1 {
		return "", false
	}
	t := d.Recv.List[0].Type
	if star, ok := t.(*ast.StarExpr); ok {
		t, ptr = star.X, true
	}
	if id, ok := t.(*ast.Ident); ok {
		return id.Name, ptr
	}
	return "", false
}
