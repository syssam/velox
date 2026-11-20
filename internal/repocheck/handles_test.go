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
	t.Parallel()
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
	t.Parallel()
	want := map[string]string{
		"Col":           "Asc Count CountDistinct Desc EQ In InSelect Key NEQ New NotIn NotInSelect Old Select Set column",
		"TimeCol":       "Asc Between Count CountDistinct Desc EQ GT GTE In InSelect Key LT LTE Max Min NEQ New NotIn NotInSelect Old Select Set column",
		"OtherCol":      "Asc Between Count CountDistinct Desc EQ GT GTE In InSelect Key LT LTE Max Min NEQ New NotIn NotInSelect Old Select Set column",
		"NumberCol":     "Add Asc Avg Between Count CountDistinct Desc EQ GT GTE In InSelect Key LT LTE Max Min NEQ New NotIn NotInSelect Old Select Set Sub Sum column",
		"StringCol":     "Asc Between Contains ContainsFold Count CountDistinct Desc EQ EqualFold GT GTE HasPrefix HasSuffix In InSelect Key LT LTE Like Max Min NEQ New NotIn NotInSelect Old Select Set column",
		"NullCol":       "Asc Count CountDistinct Desc EQ In InSelect IsNull Key NEQ New NewPtr NotIn NotInSelect NotNull Old OldPtr Select Set SetNull column",
		"NullTimeCol":   "Asc Between Count CountDistinct Desc EQ GT GTE In InSelect IsNull Key LT LTE Max Min NEQ New NewPtr NotIn NotInSelect NotNull Old OldPtr Select Set SetNull column",
		"NullOtherCol":  "Asc Between Count CountDistinct Desc EQ GT GTE In InSelect IsNull Key LT LTE Max Min NEQ New NewPtr NotIn NotInSelect NotNull Old OldPtr Select Set SetNull column",
		"NullNumberCol": "Add Asc Avg Between Count CountDistinct Desc EQ GT GTE In InSelect IsNull Key LT LTE Max Min NEQ New NewPtr NotIn NotInSelect NotNull Old OldPtr Select Set SetNull Sub Sum column",
		"NullStringCol": "Asc Between Contains ContainsFold Count CountDistinct Desc EQ EqualFold GT GTE HasPrefix HasSuffix In InSelect IsNull Key LT LTE Like Max Min NEQ New NewPtr NotIn NotInSelect NotNull Old OldPtr Select Set SetNull column",
		"JSONCol":       "Bool Has HasEvery HasKey HasSome IsEmpty New Number Old Set Text column",
		"NullJSONCol":   "Bool Has HasEvery HasKey HasSome IsEmpty IsNull New NewPtr NotNull Number Old OldPtr Set SetNull Text column",
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
