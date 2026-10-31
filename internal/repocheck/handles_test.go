package repocheck

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/syssam/velox/internal/handlegen"
)

func TestHandlesFileIsCurrent(t *testing.T) {
	want, err := handlegen.Generate()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(repoRoot(), "handles.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Error("handles.go is out of date: run go generate .")
	}
}

// TestHandleMethodSets pins the method set of each handle kind, read from
// handles.go's source, to the handle table: a method granted to the
// wrong kind, or dropped from the right one, fails here.
func TestHandleMethodSets(t *testing.T) {
	want := map[string]string{
		"Col":            "Asc Desc EQ In Key NEQ NotIn Set ValueIn column",
		"OrderedCol":     "Asc Between Desc EQ GT GTE In Key LT LTE NEQ NotIn Set ValueIn column",
		"NumberCol":      "Add Asc Between Desc EQ GT GTE In Key LT LTE NEQ NotIn Set Sub ValueIn column",
		"StringCol":      "Asc Between Contains Desc EQ EqualFold GT GTE HasPrefix HasSuffix In Key LT LTE Like NEQ NotIn Set ValueIn column",
		"NullCol":        "Asc Desc EQ In IsNull Key NEQ NotIn NotNull Set SetNull ValueIn column",
		"NullOrderedCol": "Asc Between Desc EQ GT GTE In IsNull Key LT LTE NEQ NotIn NotNull Set SetNull ValueIn column",
		"NullNumberCol":  "Add Asc Between Desc EQ GT GTE In IsNull Key LT LTE NEQ NotIn NotNull Set SetNull Sub ValueIn column",
		"NullStringCol":  "Asc Between Contains Desc EQ EqualFold GT GTE HasPrefix HasSuffix In IsNull Key LT LTE Like NEQ NotIn NotNull Set SetNull ValueIn column",
		"JSONCol":        "Set ValueIn column",
		"NullJSONCol":    "IsNull NotNull Set SetNull ValueIn column",
	}
	f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(repoRoot(), "handles.go"), nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	have := map[string][]string{}
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Recv == nil {
			continue
		}
		rt := fd.Recv.List[0].Type
		if s, ok := rt.(*ast.StarExpr); ok {
			rt = s.X
		}
		recv := recvName(rt)
		have[recv] = append(have[recv], fd.Name.Name)
	}
	for recv, names := range have {
		sort.Strings(names)
		w, ok := want[recv]
		if !ok {
			t.Errorf("handles.go has methods on %s, which is not a handle kind in the table", recv)
			continue
		}
		if got := strings.Join(names, " "); got != w {
			t.Errorf("%s methods:\n got %s\nwant %s", recv, got, w)
		}
	}
	for recv := range want {
		if _, ok := have[recv]; !ok {
			t.Errorf("handles.go has no methods on %s", recv)
		}
	}
}
