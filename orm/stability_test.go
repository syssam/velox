package orm_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"sort"
	"strings"
	"testing"
)

// forGenerated is the sentence the doc comment of a name carries when the
// name is exported for generated code and promised to nobody else. It is one
// sentence, the same everywhere, so that a reader and this test find it.
const forGenerated = "For generated code:"

// application lists the exported names of this package that an application,
// or a schema package, may write or reaches through a name of the generated
// tree. They are the package's API: changing one breaks code that people
// wrote, where changing any other breaks code that is generated again.
var application = map[string]string{
	// Forwarded by the generated root package under its own name
	// (internal/ormgen: engineTypes, engineColumns, engineConsts,
	// engineFuncs); a schema package, which cannot import the generated
	// root, names them here.
	"Agg": "forwarded", "Query": "forwarded", "Mutation": "forwarded", "Predicate": "forwarded",
	"Count": "forwarded", "Sum": "forwarded", "Min": "forwarded", "Max": "forwarded", "Mean": "forwarded",
	"And": "forwarded", "Or": "forwarded", "Not": "forwarded", "IsZero": "forwarded", "Compile": "forwarded",
	"Apply": "forwarded", "WithoutTotal": "forwarded", "StitchAll": "forwarded",
	"TraverseFunc": "forwarded", "InterceptFunc": "forwarded",
	"Op": "forwarded", "LockOption": "forwarded",

	// The builders, the variable of an entity, the edges and what they
	// return: a generated tree gives them the entity's names
	// (entity.UserQuery, velox.User, user.Posts), and an application
	// calls their methods.
	"ClientOf": "builder", "QueryOf": "builder", "CreateOf": "builder", "CreateBulkOf": "builder",
	"UpdateOf": "builder", "DeleteOf": "builder", "Selection": "builder",
	"Connection": "builder", "PageEdge": "builder",
	"ToOne": "edge", "ToMany": "edge", "LoadOf": "edge", "Loader": "edge", "EdgeCount": "edge",
	"SetterOf": "builder", "OrderOf": "builder",
	// Lazy is the constraint of the type parameter nobody writes; it is
	// in the signature of every method an application calls.
	"Lazy": "builder",

	// A condition, an assignment and an order term as values: what
	// predicate.User, predicate.UserSetter and predicate.UserOrderTerm
	// are, and what a hook reads from a mutation.
	"P": "value", "S": "value", "O": "value",
	// Written against the SQL builder, where the typed API cannot say it.
	"Raw": "value", "RawOrder": "value",
}

// columnTypes are the names of the column types, which columns.go declares:
// every level, nullable or not, set by the database or not.
func columnTypes() map[string]bool {
	out := map[string]bool{}
	for _, level := range []string{"Value", "Bool", "Enum", "Ordered", "Number", "String"} {
		for _, prefix := range []string{"", "Nullable", "Auto", "NullableAuto"} {
			out[prefix+level+"Column"] = true
		}
	}
	return out
}

// TestEveryExportedNameSaysWhoItIsFor pins the one rule of this package's
// stability: an exported name is the application's (it is in the list above,
// with why) or its doc comment says, in the fixed sentence, that it is for
// generated code. Neither, and nobody can tell whether changing it breaks
// people; both, and the sentence is false.
//
// A constant with a type is what its type is. Methods and fields are not
// read: they are their type's.
func TestEveryExportedNameSaysWhoItIsFor(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	marked := map[string]bool{} // name -> its doc comment carries the sentence
	typed := map[string]string{}
	columns := columnTypes()
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				switch d := decl.(type) {
				case *ast.FuncDecl:
					if d.Recv == nil && d.Name.IsExported() {
						marked[d.Name.Name] = says(d.Doc)
					}
				case *ast.GenDecl:
					last := ""
					for _, spec := range d.Specs {
						switch s := spec.(type) {
						case *ast.TypeSpec:
							if s.Name.IsExported() {
								marked[s.Name.Name] = says(d.Doc) || says(s.Doc)
							}
						case *ast.ValueSpec:
							// In a group with iota, a constant without
							// a type has the type of the one before.
							if id, ok := s.Type.(*ast.Ident); ok {
								last = id.Name
							} else if len(s.Values) > 0 {
								last = ""
							}
							for _, n := range s.Names {
								if !n.IsExported() {
									continue
								}
								if last != "" {
									typed[n.Name] = last
									continue
								}
								marked[n.Name] = says(d.Doc) || says(s.Doc)
							}
						}
					}
				}
			}
		}
	}
	if len(marked) < 60 {
		t.Fatalf("read %d exported names: the package was not parsed", len(marked))
	}
	var names []string
	for n := range marked {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		_, listed := application[n]
		app := listed || columns[n]
		switch {
		case app && marked[n]:
			t.Errorf("orm.%s is listed as the application's and its doc comment says %q: one of the two is wrong", n, forGenerated)
		case !app && !marked[n]:
			t.Errorf("orm.%s is exported and says who it is for nowhere: list it in application (stability_test.go) with why, or end its doc comment with the paragraph that starts %q", n, forGenerated)
		}
	}
	for n := range application {
		if _, ok := marked[n]; !ok {
			t.Errorf("application lists %s, which the package does not export", n)
		}
	}
	for n := range columns {
		if _, ok := marked[n]; !ok {
			t.Errorf("no column type %s", n)
		}
	}
	// A typed constant is its type's: the type must be known.
	for c, typ := range typed {
		if _, ok := marked[typ]; !ok {
			t.Errorf("constant %s has type %s, which is not an exported type of the package", c, typ)
		}
	}
}

// says reports whether a doc comment has a paragraph that starts with the
// sentence.
func says(doc *ast.CommentGroup) bool {
	if doc == nil {
		return false
	}
	for _, line := range strings.Split(doc.Text(), "\n") {
		if strings.HasPrefix(line, forGenerated) {
			return true
		}
	}
	return false
}
