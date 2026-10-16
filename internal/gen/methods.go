package gen

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
	"strings"
)

// FileError is a problem in one of the user's files. Error prints the path as
// it was found; a caller that knows the working directory prints Path its own
// way.
type FileError struct {
	Path   string
	Reason string
}

func (e *FileError) Error() string { return e.Path + ": " + e.Reason }

// Methods reads the non-test .go files in dir that do not start with
// velox-gen's header, and reports which of entities have a Policy or Hooks
// method. The receiver must be *E: the binding calls the method on a nil *E,
// which a method with a value receiver would panic on, so a method Policy or
// Hooks with receiver E is a *FileError, and so is a file that declares a type
// named like an entity (Ruling 11). A missing dir is no files.
//
// The result holds only the entities that have at least one of the methods.
func Methods(dir string, entities []string) (map[string]Bound, error) {
	des, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]Bound{}
	fset := token.NewFileSet()
	for _, de := range des {
		name := de.Name()
		if de.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		src, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if bytes.HasPrefix(src, []byte(strings.TrimSuffix(header, "\n\n")+"\n")) {
			continue
		}
		f, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.GenDecl:
				if d.Tok != token.TYPE {
					continue
				}
				for _, sp := range d.Specs {
					if n := sp.(*ast.TypeSpec).Name.Name; slices.Contains(entities, n) {
						return nil, &FileError{Path: path, Reason: "declares type " + n + ", which velox-gen generates"}
					}
				}
			case *ast.FuncDecl:
				e, ptr := receiver(d)
				if e == "" || !slices.Contains(entities, e) {
					continue
				}
				b := out[e]
				switch d.Name.Name {
				case "Policy":
					b.Policy = true
				case "Hooks":
					b.Hooks = true
				default:
					continue
				}
				if !ptr {
					m := d.Name.Name
					return nil, &FileError{Path: path, Reason: fmt.Sprintf("%s.%s must have a pointer receiver (func (*%[1]s) %[2]s())", e, m)}
				}
				out[e] = b
			}
		}
	}
	return out, nil
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
