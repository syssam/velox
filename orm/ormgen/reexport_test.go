package ormgen

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// An entity keeps its name: what the root package would have given of the
// library under that name is left out, and the file says so.
func TestReexports_AnEntityKeepsItsName(t *testing.T) {
	src := reexports(map[string]bool{"Tx": true, "Policy": true, "Check": true}, "velox", "context", "sql")
	for _, gone := range []string{"\tTx ", "\tPolicy ", "func Check("} {
		if strings.Contains(src, gone) {
			t.Errorf("%q is declared beside the entity of that name:\n%s", gone, src)
		}
	}
	for _, kept := range []string{"\tDB = velox.DB\n", "\tOpCreate = velox.OpCreate\n", "func IsNotFound(err error) bool { return velox.IsNotFound(err) }", "// Check, Policy, Tx. Import the library for them."} {
		if !strings.Contains(src, kept) {
			t.Errorf("want %q in:\n%s", kept, src)
		}
	}
}

// Every name RootNames lists is declared by the root package of a tree whose
// schema has no entity of that name, and is a name of the library: the list
// is what cmd/velox-migrate goes by.
func TestRootNames_AreDeclared(t *testing.T) {
	declared := func(dir string) map[string]bool {
		out := map[string]bool{}
		paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil || len(paths) == 0 {
			t.Fatal(dir, err)
		}
		for _, p := range paths {
			if strings.HasSuffix(p, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(token.NewFileSet(), p, nil, parser.SkipObjectResolution)
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range f.Decls {
				switch d := d.(type) {
				case *ast.FuncDecl:
					if d.Recv == nil {
						out[d.Name.Name] = true
					}
				case *ast.GenDecl:
					for _, s := range d.Specs {
						switch s := s.(type) {
						case *ast.TypeSpec:
							out[s.Name.Name] = true
						case *ast.ValueSpec:
							for _, n := range s.Names {
								out[n.Name] = true
							}
						}
					}
				}
			}
		}
		return out
	}
	generated, library := declared("../../tests/orm/velox"), declared("../..")
	names := RootNames()
	if len(names) < 40 {
		t.Fatalf("only %d names", len(names))
	}
	for _, n := range names {
		if !generated[n] {
			t.Errorf("tests/orm/velox does not declare %s", n)
		}
		if !library[n] {
			t.Errorf("package velox does not declare %s", n)
		}
	}
	// The shapes schema has an entity called Tx.
	shapes := declared("../../tests/orm/shapes/velox")
	if !shapes["Tx"] || !shapes["DB"] {
		t.Error("tests/orm/shapes/velox: want the entity Tx and the library's DB")
	}
}

// An import is written as a person writes it where the generator knows the
// package's name, and with the name where it is the application's package.
func TestImportSpec(t *testing.T) {
	const tree = "example.com/app/velox"
	for _, c := range []struct{ name, path, want string }{
		{"context", "context", `"context"`},
		{"sql", "database/sql", `"database/sql"`},
		{"velox", veloxPath, `"github.com/syssam/velox"`},
		{"orm", ormPath, `"github.com/syssam/velox/orm"`},
		{"schemafield", "github.com/syssam/velox/schema/field", `schemafield "github.com/syssam/velox/schema/field"`},
		{"predicate", tree + "/predicate", `"example.com/app/velox/predicate"`},
		{"userpkg", tree + "/user", `userpkg "example.com/app/velox/user"`},
		{"_", tree + "/schemainit", `_ "example.com/app/velox/schemainit"`},
		// The application's own: its package clause may say another name.
		{"uuid", "github.com/google/uuid", `uuid "github.com/google/uuid"`},
		{"schema", "example.com/app/schema", `schema "example.com/app/schema"`},
		{"time2", "time", `time2 "time"`},
	} {
		if got := importSpec(c.name, c.path, tree); got != c.want {
			t.Errorf("importSpec(%q, %q) = %s, want %s", c.name, c.path, got, c.want)
		}
	}
	decl := importDecl(map[string]string{"time": `"time"`, "context": `"context"`, ormPath: `"` + ormPath + `"`, "example.com/app/schema": `schema "example.com/app/schema"`})
	want := "import (\n\t\"context\"\n\t\"time\"\n\n\tschema \"example.com/app/schema\"\n\t\"" + ormPath + "\"\n)\n"
	if decl != want {
		t.Errorf("got:\n%s\nwant:\n%s", decl, want)
	}
}
