package codegen

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestRuleTable: every number b.fail reports, and every "rule N" a comment of
// the package cites, is in rules, and every test rules names is a test of the
// package. A rule TestBuildRules covers has a case named after its number.
func TestRuleTable(t *testing.T) {
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string]bool{}
	used := map[int]string{}
	cited := regexp.MustCompile(`\brules? (\d+)\b`)
	var buildRules string
	for _, name := range files {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, name, src, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		for _, cg := range f.Comments {
			for _, m := range cited.FindAllStringSubmatch(cg.Text(), -1) {
				n, _ := strconv.Atoi(m[1])
				used[n] = fset.Position(cg.Pos()).String()
			}
		}
		ast.Inspect(f, func(x ast.Node) bool {
			switch x := x.(type) {
			case *ast.FuncDecl:
				if strings.HasPrefix(x.Name.Name, "Test") && x.Recv == nil {
					tests[x.Name.Name] = true
					if x.Name.Name == "TestBuildRules" {
						buildRules = string(src[fset.Position(x.Pos()).Offset:fset.Position(x.End()).Offset])
					}
				}
			case *ast.CallExpr:
				sel, ok := x.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "fail" || len(x.Args) == 0 {
					return true
				}
				if id, ok := sel.X.(*ast.Ident); !ok || id.Name != "b" {
					return true
				}
				lit, ok := x.Args[0].(*ast.BasicLit)
				if !ok {
					t.Errorf("%s: b.fail's rule is not a number", fset.Position(x.Pos()))
					return true
				}
				n, _ := strconv.Atoi(lit.Value)
				used[n] = fset.Position(x.Pos()).String()
			}
			return true
		})
	}
	for n, at := range used {
		if _, ok := rules[n]; !ok {
			t.Errorf("%s: rule %d is not in rules", at, n)
		}
	}
	for n, r := range rules {
		if !tests[r.test] {
			t.Errorf("rule %d names %s, which is not a test of the package", n, r.test)
		}
		if r.test == "TestBuildRules" && !strings.Contains(buildRules, `{"`+strconv.Itoa(n)+` `) {
			t.Errorf("rule %d names TestBuildRules, which has no case named after it", n)
		}
	}
}
