package veloxotel

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
// numbered ruling, a success criterion, a review's numbered finding or an
// internal plan, named with its number. A reader of the godoc has none of
// them; what a comment or a message needs from them, it says. It does not
// match the ordinary words "design" and "spec".
var citation = regexp.MustCompile(`\x{00a7}|\bRuling \d|\bReview Focus \d|\bspec success criterion\b|\b(design|spec) (section|note)\b|\bsection \d|\b(schema|policy|routing|observability|core|graphql) (design|spec)\b|\bdocs/(specs|plans|reviews)\b|\b(GraphQL|API)\s+plan\s+\d`)

// TestGodocCitesNoSpec: no comment in this module's public package (tests
// included) and no string literal in it, which holds the messages a user
// sees, cites a design document, a ruling or a review.
func TestGodocCitesNoSpec(t *testing.T) {
	_, self, _, _ := runtime.Caller(0)
	files, err := filepath.Glob(filepath.Join(filepath.Dir(self), "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 19 {
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
