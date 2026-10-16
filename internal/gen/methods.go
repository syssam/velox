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

// Methods reads the non-test .go files in dir that do not start with
// velox-gen's header, and reports which of entities have a Policy or Hooks
// method (receiver E or *E). A file that declares a type named like an
// entity is an error (Ruling 11). A missing dir is no files.
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
						return nil, fmt.Errorf("%s declares type %s, which velox-gen generates", path, n)
					}
				}
			case *ast.FuncDecl:
				e := receiverName(d)
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
				out[e] = b
			}
		}
	}
	return out, nil
}

// receiverName is the type name of d's receiver, E for (E) and (*E), and ""
// for a function or a receiver of another shape.
func receiverName(d *ast.FuncDecl) string {
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
