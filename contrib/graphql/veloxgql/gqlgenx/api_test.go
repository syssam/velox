package gqlgenx_test

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

// publicAPI is the exported API of package gqlgenx, sorted. Every change to
// the API edits this list.
var publicAPI = []string{
	"Cost",
	"ErrorPresenter",
	"Extension",
	"Extension.ExtensionName",
	"Extension.InterceptField",
	"Extension.InterceptOperation",
	"Extension.InterceptResponse",
	"Extension.MutateOperationContext",
	"Extension.MutateOperationParameters",
	"Extension.NewContext",
	"Extension.Validate",
	"MarshalBytes",
	"MarshalFloat32",
	"MarshalInt64ID",
	"MarshalTime",
	"MarshalUUID",
	"MarshalUint",
	"MarshalUint64",
	"NewExtension",
	"OnCanceled",
	"Option",
	"PresenterOption",
	"Server",
	"Setup",
	"UnmarshalBytes",
	"UnmarshalFloat32",
	"UnmarshalInt64ID",
	"UnmarshalTime",
	"UnmarshalUUID",
	"UnmarshalUint",
	"UnmarshalUint64",
	"WithBatchScope",
	"WithLimits",
	"WithTxFilter",
	"WithTxOptions",
}

// exportedNames are the exported names of the package in dir, as "Name" and
// "Type.Method", sorted.
func exportedNames(t *testing.T, dir string) []string {
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
	d, err := doc.NewFromFiles(fset, files, "github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	values := func(vs []*doc.Value) {
		for _, v := range vs {
			out = append(out, v.Names...)
		}
	}
	funcs := func(prefix string, fs []*doc.Func) {
		for _, f := range fs {
			out = append(out, prefix+f.Name)
		}
	}
	values(d.Consts)
	values(d.Vars)
	funcs("", d.Funcs)
	for _, ty := range d.Types {
		out = append(out, ty.Name)
		values(ty.Consts)
		values(ty.Vars)
		funcs("", ty.Funcs)
		funcs(ty.Name+".", ty.Methods)
	}
	slices.Sort(out)
	return out
}

// TestPublicAPI pins the exported API of package gqlgenx.
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
