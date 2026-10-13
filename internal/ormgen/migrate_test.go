package ormgen

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// migrateSource is the shape of what velox's migration generator writes: the
// columns and the table of each table, the list of tables, and one init
// function that connects the foreign keys.
func migrateSource(keys int) []byte {
	var b strings.Builder
	b.WriteString("package migrate\n\nimport (\n\t\"github.com/syssam/velox/dialect/sql/schema\"\n)\n\n")
	for _, t := range []string{"User", "Post", "PostTags"} {
		fmt.Fprintf(&b, "// %[1]sColumns holds the columns for the %[2]q table.\nvar %[1]sColumns = []*schema.Column{{\n\tName: \"id\",\n}}\n\n", t, strings.ToLower(t)+"s")
		fmt.Fprintf(&b, "// %[1]sTable holds the schema information for the %[2]q table.\nvar %[1]sTable = &schema.Table{\n\tColumns: %[1]sColumns,\n\tIndexes: []*schema.Index{{\n\t\tColumns: []*schema.Column{%[1]sColumns[0]},\n\t}},\n}\n\n", t, strings.ToLower(t)+"s")
	}
	b.WriteString("// Tables holds all the tables in the schema.\nvar Tables = []*schema.Table{UserTable, PostTable, PostTagsTable}\n\nfunc init() {\n")
	for i := range keys {
		// One statement in three is of several lines, as an annotation is.
		if i%3 == 0 {
			fmt.Fprintf(&b, "\tPostTable.Annotation = &schema.Annotation{\n\t\tTable: \"p%d\",\n\t}\n", i)
			continue
		}
		fmt.Fprintf(&b, "\tPostTable.ForeignKeys[%d].RefTable = UserTable\n", i)
	}
	b.WriteString("}\n")
	return []byte(b.String())
}

// What is rewritten of the migration generator's text: a table to its
// entity's file and into a function of its own, the join table left in
// schema.go, the init function cut where a statement ends.
func TestArrangeTables(t *testing.T) {
	const keys = 2*initChunk + 10
	files := map[string]string{"users": "user", "posts": "post"}
	out, err := arrangeTables(migrateSource(keys), files)
	if err != nil {
		t.Fatal(err)
	}
	parts, err := split(out, "schema")
	if err != nil {
		t.Fatalf("the result does not split into files: %v\n%s", err, out)
	}
	for file, want := range map[string][]string{
		"user.go":   {"var UserColumns = ", "var UserTable = newUserTable()", "func newUserTable() *schema.Table {", "Columns: []*schema.Column{UserColumns[0]},"},
		"post.go":   {"var PostTable = newPostTable()", "func newPostTable() *schema.Table {"},
		"schema.go": {"var PostTagsTable = newPostTagsTable()", "var Tables = []*schema.Table{UserTable, PostTable, PostTagsTable}", "func init() {"},
	} {
		for _, w := range want {
			if !bytes.Contains(parts[file], []byte(w)) {
				t.Errorf("%s does not have %q:\n%s", file, w, parts[file])
			}
		}
	}
	if bytes.Contains(parts["schema.go"], []byte("UserColumns = ")) {
		t.Error("schema.go still declares the columns of an entity's table")
	}
	// Every file is Go, and the statements of the init function are all
	// there, in order, in functions of no more than initChunk.
	f, err := parser.ParseFile(token.NewFileSet(), "schema.go", parts["schema.go"], 0)
	if err != nil {
		t.Fatalf("schema.go: %v\n%s", err, parts["schema.go"])
	}
	inits, total := 0, 0
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "init" {
			continue
		}
		inits++
		total += len(fn.Body.List)
		if len(fn.Body.List) > initChunk {
			t.Errorf("an init function of %d statements, more than %d", len(fn.Body.List), initChunk)
		}
	}
	if inits != 3 || total != keys {
		t.Errorf("%d init functions with %d statements, want 3 with %d", inits, total, keys)
	}

	// A table whose declaration is not in the form this relies on is an
	// error: left as it was, it would be compiled into the function the
	// rewriting is there to avoid, and nothing would say so.
	changed := bytes.Replace(migrateSource(1), []byte("var PostTable = &schema.Table{"), []byte("var PostTable = schema.NewTable(schema.Table{"), 1)
	if _, err := arrangeTables(changed, files); err == nil || !strings.Contains(err.Error(), "3 tables announced, 2 declared") {
		t.Errorf("a table that is not recognized: %v", err)
	}
	if _, err := arrangeTables([]byte("package migrate\n"), files); err == nil {
		t.Error("no table at all, for two entities, is not an error")
	}
}

// TestNoFunctionGrowsWithTheSchema pins the rule arrangeTables is one case
// of: nothing generated is one function, or one initializer, for the whole
// schema. The compiler's time is quadratic in the size of a function, so
// such a function is fast for the schemas of the tests and what a schema of
// 2,000 entities waits for. In the tree of the 87-entity schema, no function
// has more statements than a wide entity needs, and no package-level
// variable of the migrate package is initialized by an expression that has
// to run.
func TestNoFunctionGrowsWithTheSchema(t *testing.T) {
	const limit = 150 // the widest entity of the shapes schema needs 60
	root := "../../tests/orm/shapes/velox"
	paths, err := filepath.Glob(filepath.Join(root, "*", "*.go"))
	if err != nil || len(paths) < 250 {
		t.Fatal(len(paths), err)
	}
	more, err := filepath.Glob(filepath.Join(root, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	funcs := 0
	for _, p := range append(paths, more...) {
		f, err := parser.ParseFile(token.NewFileSet(), p, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Body == nil {
					continue
				}
				funcs++
				n := 0
				ast.Inspect(d.Body, func(x ast.Node) bool {
					if _, ok := x.(ast.Stmt); ok {
						n++
					}
					return true
				})
				if n > limit {
					t.Errorf("%s: %s has %d statements: a function that grows with the schema?", p, d.Name.Name, n)
				}
			case *ast.GenDecl:
				if d.Tok != token.VAR || filepath.Base(filepath.Dir(p)) != "migrate" {
					continue
				}
				for _, spec := range d.Specs {
					vs := spec.(*ast.ValueSpec)
					for i, v := range vs.Values {
						ast.Inspect(v, func(x ast.Node) bool {
							if _, ok := x.(*ast.IndexExpr); ok && !strings.HasPrefix(vs.Names[i].Name, "_") {
								t.Errorf("%s: the initializer of %s indexes a variable: it runs in the one function that initializes the package", p, vs.Names[i].Name)
								return false
							}
							return true
						})
					}
				}
			}
		}
	}
	if funcs < 500 {
		t.Errorf("%d functions read: the test no longer reads the tree", funcs)
	}
}
