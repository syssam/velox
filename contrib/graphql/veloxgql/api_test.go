package veloxgql_test

import (
	"go/ast"
	"go/doc"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// publicAPI is the exported API of package veloxgql, sorted. Every change to
// the API edits this list: a name that generated code alone calls lives in
// package gqlgraph, and one of gqlgen alone in package gqlgenx.
var publicAPI = []string{
	"Batch",
	"Bytes",
	"Bytes.MarshalGQL",
	"Bytes.UnmarshalGQL",
	"CodeBadUserInput",
	"CodeForbidden",
	"CodeInternal",
	"CodeNotFound",
	"CodeTimeout",
	"CodeTooCostly",
	"CodeUnauthenticated",
	"Cursor",
	"Cursor.MarshalGQL",
	"Cursor.MarshalGQLContext",
	"Cursor.UnmarshalGQL",
	"Cursor.UnmarshalGQLContext",
	"CursorTerm",
	"DB",
	"DBFromContext",
	"DecodeCursor",
	"DefaultLimits",
	"EncodeCursor",
	"ErrBadCursor",
	"ErrBadPagination",
	"ErrFilterTooDeep",
	"ErrFilterTooLarge",
	"ErrFilterTooWide",
	"ErrNoDB",
	"ErrNoSelectionSource",
	"ErrPageTooLarge",
	"ErrRequestTooLarge",
	"ErrResponseTooLarge",
	"ErrTimeout",
	"ErrTooCostly",
	"ErrTooManyErrors",
	"GeneratedCodeIsVersion27",
	"JSON",
	"JSON.MarshalGQL",
	"JSON.UnmarshalGQL",
	"Limits",
	"LimitsFromContext",
	"OrderDirection",
	"OrderDirection.IsValid",
	"OrderDirection.MarshalGQL",
	"OrderDirection.String",
	"OrderDirection.UnmarshalGQL",
	"OrderDirectionAsc",
	"OrderDirectionDesc",
	"PageInfo",
	"Present",
	"Service",
	"ServiceWithLimits",
	"Time",
	"Time.MarshalGQL",
	"Time.UnmarshalGQL",
	"UUID",
	"UUID.MarshalGQL",
	"UUID.String",
	"UUID.UnmarshalGQL",
	"Uint64",
	"Uint64.MarshalGQL",
	"Uint64.UnmarshalGQL",
}

// parseDoc is the documentation of the non-test files of the package in dir.
func parseDoc(t *testing.T, dir, path string) *doc.Package {
	t.Helper()
	names, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, n := range names {
		if strings.HasSuffix(n, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, n, nil, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	p, err := doc.NewFromFiles(fset, files, path)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// exportedNames are the exported names of the package in dir, as "Name" and
// "Type.Method", sorted.
func exportedNames(t *testing.T, dir string) []string {
	t.Helper()
	d := parseDoc(t, dir, "github.com/syssam/velox/contrib/graphql/veloxgql")
	var names []string
	values := func(vs []*doc.Value) {
		for _, v := range vs {
			names = append(names, v.Names...)
		}
	}
	funcs := func(prefix string, fs []*doc.Func) {
		for _, f := range fs {
			names = append(names, prefix+f.Name)
		}
	}
	values(d.Consts)
	values(d.Vars)
	funcs("", d.Funcs)
	for _, ty := range d.Types {
		names = append(names, ty.Name)
		values(ty.Consts)
		values(ty.Vars)
		funcs("", ty.Funcs)
		funcs(ty.Name+".", ty.Methods)
	}
	slices.Sort(names)
	return names
}

// TestPublicAPI pins the exported API of package veloxgql. Whatever only
// generated code calls is in package gqlgraph.
func TestPublicAPI(t *testing.T) {
	got := exportedNames(t, ".")
	want := slices.Sorted(slices.Values(publicAPI))
	if !slices.Equal(got, want) {
		for _, n := range got {
			if !slices.Contains(want, n) {
				t.Errorf("exported but not in the list: %s", n)
			}
		}
		for _, n := range want {
			if !slices.Contains(got, n) {
				t.Errorf("in the list but not exported: %s", n)
			}
		}
	}
}

// TestGqlgraphIsForGeneratedCode: package gqlgraph says it is for generated
// code, and what guards the pair; the version constant says so too, just
// after its first sentence.
func TestGqlgraphIsForGeneratedCode(t *testing.T) {
	d := parseDoc(t, "gqlgraph", "github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph")
	if !strings.HasPrefix(d.Doc, "Package gqlgraph is for generated code.") {
		t.Errorf("gqlgraph's doc starts %q", firstLine(d.Doc))
	}
	if !strings.Contains(d.Doc, "veloxgql.GeneratedCodeIsVersion27") {
		t.Error("gqlgraph's doc does not name veloxgql.GeneratedCodeIsVersion27")
	}

	found := false
	for _, v := range parseDoc(t, ".", "github.com/syssam/velox/contrib/graphql/veloxgql").Consts {
		if slices.Contains(v.Names, "GeneratedCodeIsVersion27") {
			found = true
			if _, rest, ok := strings.Cut(strings.Join(strings.Fields(v.Doc), " "), ". "); !ok || !strings.HasPrefix(rest, "For generated code.") {
				t.Errorf("GeneratedCodeIsVersion27's doc %q does not say %q after its first sentence", firstLine(v.Doc), "For generated code.")
			}
		}
	}
	if !found {
		t.Error("veloxgql has no GeneratedCodeIsVersion27")
	}
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
