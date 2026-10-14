package ormgen

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	generated, library, engine := declared("../../tests/orm/velox"), declared("../.."), declared("../../orm")
	names := RootNames()
	if len(names) < 40 {
		t.Fatalf("only %d names", len(names))
	}
	ofLibrary := map[string]bool{}
	for _, n := range libraryNames() {
		ofLibrary[n] = true
	}
	// A name the generated root gives is the library's, or one of package
	// orm's under the same name; three of the engine's have a name of their
	// own there, because the library has the plain one.
	renamed := map[string]string{"EntityQuery": "Query", "EntityMutation": "Mutation", "Traverse": "TraverseFunc", "BeforeQuery": "InterceptFunc"}
	for _, n := range names {
		if !generated[n] {
			t.Errorf("tests/orm/velox does not declare %s", n)
		}
		switch {
		case ofLibrary[n]:
			if !library[n] {
				t.Errorf("package velox does not declare %s", n)
			}
		case renamed[n] != "":
			if !engine[renamed[n]] {
				t.Errorf("package orm does not declare %s, which the root gives as %s", renamed[n], n)
			}
			if library[n] {
				t.Errorf("the library declares %s too: the generated root would give two things one name", n)
			}
		default:
			if !engine[n] {
				t.Errorf("package orm does not declare %s", n)
			}
			if library[n] {
				t.Errorf("the library declares %s too: the generated root would give two things one name", n)
			}
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

// What the generated root gives an application under its own name is the
// application's: no such name of package orm may say, in its doc comment,
// that it is for generated code (orm/stability_test.go has the other half:
// every exported name there says one or the other).
func TestForwardedEngineNamesAreTheApplications(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, filepath.Join("..", "..", "orm"), func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ParseComments)
	require.NoError(t, err)
	const sentence = "For generated code:"
	forGenerated := map[string]bool{}
	declared := map[string]bool{}
	note := func(name string, docs ...*ast.CommentGroup) {
		declared[name] = true
		for _, d := range docs {
			if d != nil && strings.Contains(d.Text(), sentence) {
				forGenerated[name] = true
			}
		}
	}
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				switch d := decl.(type) {
				case *ast.FuncDecl:
					if d.Recv == nil {
						note(d.Name.Name, d.Doc)
					}
				case *ast.GenDecl:
					for _, spec := range d.Specs {
						switch s := spec.(type) {
						case *ast.TypeSpec:
							note(s.Name.Name, d.Doc, s.Doc)
						case *ast.ValueSpec:
							for _, n := range s.Names {
								note(n.Name, s.Doc)
							}
						}
					}
				}
			}
		}
	}
	require.NotEmpty(t, forGenerated, "package orm marks what is for generated code with %q", sentence)

	var names []string
	for _, typ := range engineTypes {
		names = append(names, typ[1])
	}
	names = append(append(names, engineColumns...), engineConsts...)
	used := regexp.MustCompile(`%\[1\]s\.([A-Z]\w*)`)
	for _, f := range engineFuncs {
		for _, m := range used.FindAllStringSubmatch(f.sig+" "+f.body, -1) {
			names = append(names, m[1])
		}
	}
	require.Greater(t, len(names), 30)
	for _, n := range names {
		assert.True(t, declared[n], "the generated root forwards orm.%s, which package orm does not declare", n)
		assert.False(t, forGenerated[n], "the generated root gives an application orm.%s, whose doc comment says %q", n, sentence)
	}
}
