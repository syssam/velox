package codegen

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
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
// ones), is a *FileError. So is a Hooks or Policy method on
// an entity: those are declared in the schema package (see SchemaMethods),
// and the generated code binds them. A file no build includes is not read, and
// one only some builds include is a *FileError (see userFiles). A missing dir
// is no files.
func CheckUserFiles(dir string, entities []string) error {
	return NewCache().CheckUserFiles(dir, entities)
}

// CheckUserFiles is the package's CheckUserFiles, which reads dir through c.
func (c *Cache) CheckUserFiles(dir string, entities []string) error {
	return c.userFiles(dir, 0, func(fset *token.FileSet, path string, f *ast.File) error {
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.GenDecl:
				for _, sp := range d.Specs {
					if sp, ok := sp.(*ast.TypeSpec); ok && slices.Contains(entities, sp.Name.Name) {
						return &FileError{Path: path, Reason: "declares type " + sp.Name.Name + ", which velox-gen generates"}
					}
				}
			case *ast.FuncDecl:
				e := receiver(d)
				if e == "" || !slices.Contains(entities, e) || !slices.Contains(behaviourMethods, d.Name.Name) {
					continue
				}
				m := d.Name.Name
				return &FileError{Path: path, Line: fset.Position(d.Pos()).Line, Reason: fmt.Sprintf(
					"declares %s.%s in the generated package; declare it in the schema package, on %[1]s: func (%[1]s) %[2]s() %[3]s", e, m, behaviourResult(m))}
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
	return NewCache().HasPackageDoc(dir)
}

// HasPackageDoc is the package's HasPackageDoc, which reads dir through c.
func (c *Cache) HasPackageDoc(dir string) (bool, error) {
	found := false
	err := c.userFiles(dir, parser.PackageClauseOnly|parser.ParseComments, func(_ *token.FileSet, _ string, f *ast.File) error {
		if f.Doc != nil && strings.TrimSpace(f.Doc.Text()) != "" {
			found = true
		}
		return nil
	})
	return found, err
}

// writeKeys and readKeys are the keys of a velox.Policy that hold
// conditions: policyConds looks for writeKeys, readsConds for readKeys.
var (
	writeKeys = []string{"Read", "Write", "Query", "Mutation"}
	readKeys  = []string{"Read", "Query"}
)

// readsConds is policyConds of the Read and Query keys alone: the position
// of a condition on the rows a read sees.
func readsConds(f *ast.File, d *ast.FuncDecl) token.Pos { return policyKeys(f, d, readKeys) }

// policyConds returns the position of the first Read, Write, Query or
// Mutation key, with a value other than nil, of a composite literal of type
// velox.Policy or privacy.Policy (its alias) in the body of d, the Policy
// method of an entity in file f, and token.NoPos when there is none. A
// Policy built another way, by a positional literal, by assigning its fields
// or in another function, is left to the runtime's check when the entity
// loads.
func policyConds(f *ast.File, d *ast.FuncDecl) token.Pos { return policyKeys(f, d, writeKeys) }

// policyKeys is policyConds of the keys given.
func policyKeys(f *ast.File, d *ast.FuncDecl, keys []string) token.Pos {
	names := policyPkgNames(f)
	if len(names) == 0 || d.Body == nil {
		return token.NoPos
	}
	found := token.NoPos
	ast.Inspect(d.Body, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok || found.IsValid() {
			return !found.IsValid()
		}
		if !isPolicyType(lit.Type, names) {
			return true
		}
		for _, el := range lit.Elts {
			if kv, ok := el.(*ast.KeyValueExpr); ok {
				if v, ok := kv.Value.(*ast.Ident); ok && v.Name == "nil" {
					continue
				}
				if k, ok := kv.Key.(*ast.Ident); ok && slices.Contains(keys, k.Name) {
					found = k.Pos()
					return false
				}
			}
		}
		return true
	})
	return found
}

// policyPkgNames is the names file f imports velox and velox's privacy
// package under, each of which names a Policy.
func policyPkgNames(f *ast.File) []string {
	var names []string
	for _, im := range f.Imports {
		p, _ := strconv.Unquote(im.Path.Value)
		if p != veloxPkg && p != privacyPkg {
			continue
		}
		name := guessName(p)
		if im.Name != nil {
			name = im.Name.Name
		}
		names = append(names, name)
	}
	return names
}

// isPolicyType reports whether t is <name>.Policy, name one of names.
func isPolicyType(t ast.Expr, names []string) bool {
	sel, ok := t.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Policy" {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && slices.Contains(names, id.Name)
}

// receiver is the type name of d's receiver, E for (E) and (*E). It is ""
// for a function or a receiver of another shape.
func receiver(d *ast.FuncDecl) string {
	if d.Recv == nil || len(d.Recv.List) != 1 {
		return ""
	}
	t := d.Recv.List[0].Type
	if star, ok := t.(*ast.StarExpr); ok {
		t = star.X
	}
	if id, ok := t.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}
