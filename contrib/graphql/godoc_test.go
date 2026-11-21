package graphql

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
)

// citation matches a reference to a design document, a section of one, a
// numbered ruling or an internal plan, named with its number. A reader of
// the godoc has none of them; what a comment or a message needs from them,
// it says. It does not match the ordinary words "design" and "spec". It is
// the root module's, with the internal plans added.
var citation = regexp.MustCompile(`\x{00a7}|\bRuling \d|\b(design|spec) (section|note)\b|\bsection \d|\b(schema|policy|routing|observability|core|graphql) (design|spec)\b|\bdocs/(specs|plans|reviews)\b|\b(GraphQL|API)\s+plan\s+\d`)

// TestGodocCitesNoSpec: no comment in the public packages of this module
// (graphql, veloxgql, gqlgraph and gqlgenx, tests included) and no string literal in
// them, which holds the messages a user sees, cites a design document or a
// ruling.
func TestGodocCitesNoSpec(t *testing.T) {
	_, self, _, _ := runtime.Caller(0)
	root := filepath.Dir(self)
	var files []string
	for _, dir := range []string{".", "veloxgql", "veloxgql/gqlgraph", "veloxgql/gqlgenx"} {
		m, err := filepath.Glob(filepath.Join(root, dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, m...)
	}
	if len(files) < 30 {
		t.Fatalf("scanned only %d files: %v", len(files), files)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		f, err := parser.ParseFile(fset, name, nil, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		for _, cg := range f.Comments {
			// The group's text, so that a citation across a line break
			// is found.
			if m := citation.FindString(cg.Text()); m != "" {
				t.Errorf("%s: comment cites %q", fset.Position(cg.Pos()), m)
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if l, ok := n.(*ast.BasicLit); ok && l.Kind == token.STRING {
				if m := citation.FindString(l.Value); m != "" {
					t.Errorf("%s: string cites %q", fset.Position(l.Pos()), m)
				}
			}
			return true
		})
	}
}
