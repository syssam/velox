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
// package gqlimpl.
var publicAPI = []string{
	"Bytes",
	"Bytes.MarshalGQL",
	"Bytes.UnmarshalGQL",
	"Cursor",
	"Cursor.MarshalGQL",
	"Cursor.UnmarshalGQL",
	"CursorTerm",
	"DBFrom",
	"DecodeCursor",
	"DecodeID",
	"DefaultLimits",
	"EncodeCursor",
	"EncodeID",
	"ErrBadCursor",
	"ErrBadID",
	"ErrBadPagination",
	"ErrFilterTooDeep",
	"ErrFilterTooLarge",
	"ErrFilterTooWide",
	"ErrNoDB",
	"ErrPageTooLarge",
	"ErrWrongIDType",
	"Extension",
	"Extension.Context",
	"Extension.ExtensionName",
	"Extension.InterceptField",
	"Extension.InterceptOperation",
	"Extension.InterceptResponse",
	"Extension.Validate",
	"GeneratedCodeIsVersion1",
	"IDType",
	"JSON",
	"JSON.MarshalGQL",
	"JSON.UnmarshalGQL",
	"Limits",
	"LimitsFrom",
	"MarshalBytes",
	"MarshalFloat32",
	"MarshalTime",
	"MarshalUUID",
	"MarshalUint",
	"MarshalUint64",
	"NewExtension",
	"Noder",
	"Option",
	"OrderDirection",
	"OrderDirection.IsValid",
	"OrderDirection.MarshalGQL",
	"OrderDirection.String",
	"OrderDirection.UnmarshalGQL",
	"OrderDirectionAsc",
	"OrderDirectionDesc",
	"PageInfo",
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
	"UnmarshalBytes",
	"UnmarshalFloat32",
	"UnmarshalTime",
	"UnmarshalUUID",
	"UnmarshalUint",
	"UnmarshalUint64",
	"WithLimits",
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
	d := parseDoc(t, dir, "github.com/syssam/velox2/contrib/graphql/veloxgql")
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
// generated code calls is in package gqlimpl.
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

// TestGqlimplIsForGeneratedCode: package gqlimpl says it is for generated
// code, and what guards the pair; the version constant says so too.
func TestGqlimplIsForGeneratedCode(t *testing.T) {
	d := parseDoc(t, "gqlimpl", "github.com/syssam/velox2/contrib/graphql/veloxgql/gqlimpl")
	if !strings.HasPrefix(d.Doc, "Package gqlimpl is for generated code.") {
		t.Errorf("gqlimpl's doc starts %q", firstLine(d.Doc))
	}
	if !strings.Contains(d.Doc, "veloxgql.GeneratedCodeIsVersion1") {
		t.Error("gqlimpl's doc does not name veloxgql.GeneratedCodeIsVersion1")
	}

	found := false
	for _, v := range parseDoc(t, ".", "github.com/syssam/velox2/contrib/graphql/veloxgql").Consts {
		if slices.Contains(v.Names, "GeneratedCodeIsVersion1") {
			found = true
			if !strings.HasPrefix(v.Doc, "For generated code.") {
				t.Errorf("GeneratedCodeIsVersion1's doc starts %q", firstLine(v.Doc))
			}
		}
	}
	if !found {
		t.Error("veloxgql has no GeneratedCodeIsVersion1")
	}
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
