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
		"Col":           "Asc Assign Count CountDistinct Desc EQ Fill Get In InSelect NEQ New NotIn NotInSelect Old Put Select Set SetPtr Value column",
		"TimeCol":       "Asc Assign Between Count CountDistinct Desc EQ Fill GT GTE Get In InSelect LT LTE Max Min NEQ New NotIn NotInSelect Old Put Select Set SetPtr Value column",
		"OtherCol":      "Asc Assign Between Count CountDistinct Desc EQ Fill GT GTE Get In InSelect LT LTE Max Min NEQ New NotIn NotInSelect Old Put Select Set SetPtr Value column",
		"NumberCol":     "Add Asc Assign Avg Between Count CountDistinct Desc EQ Fill GT GTE Get In InSelect LT LTE Max Min NEQ New NotIn NotInSelect Old Put Select Set SetPtr Sub Sum Value column",
		"StringCol":     "Asc Assign Between Contains ContainsFold Count CountDistinct Desc EQ EqualFold Fill GT GTE Get HasPrefix HasSuffix In InSelect LT LTE Like Max Min NEQ New NotIn NotInSelect Old Put Select Set SetPtr Value column",
		"NullCol":       "Asc Assign AssignNull Count CountDistinct Desc EQ Fill Get GetPtr In InSelect IsNull NEQ New NewPtr NotIn NotInSelect NotNull Old OldPtr Put PutNull Select Set SetNull SetPtr Value ValuePtr column",
		"NullTimeCol":   "Asc Assign AssignNull Between Count CountDistinct Desc EQ Fill GT GTE Get GetPtr In InSelect IsNull LT LTE Max Min NEQ New NewPtr NotIn NotInSelect NotNull Old OldPtr Put PutNull Select Set SetNull SetPtr Value ValuePtr column",
		"NullOtherCol":  "Asc Assign AssignNull Between Count CountDistinct Desc EQ Fill GT GTE Get GetPtr In InSelect IsNull LT LTE Max Min NEQ New NewPtr NotIn NotInSelect NotNull Old OldPtr Put PutNull Select Set SetNull SetPtr Value ValuePtr column",
		"NullNumberCol": "Add Asc Assign AssignNull Avg Between Count CountDistinct Desc EQ Fill GT GTE Get GetPtr In InSelect IsNull LT LTE Max Min NEQ New NewPtr NotIn NotInSelect NotNull Old OldPtr Put PutNull Select Set SetNull SetPtr Sub Sum Value ValuePtr column",
		"NullStringCol": "Asc Assign AssignNull Between Contains ContainsFold Count CountDistinct Desc EQ EqualFold Fill GT GTE Get GetPtr HasPrefix HasSuffix In InSelect IsNull LT LTE Like Max Min NEQ New NewPtr NotIn NotInSelect NotNull Old OldPtr Put PutNull Select Set SetNull SetPtr Value ValuePtr column",
		"JSONCol":       "Append Array Assign Bool Contains Fill Get Has HasEvery HasKey HasSome IsEmpty Len New Number Old Put Set SetPtr Text Value ValueIsNull column",
		"NullJSONCol":   "Append Array Assign AssignNull Bool Contains Fill Get GetPtr Has HasEvery HasKey HasSome IsEmpty IsNull Len New NewPtr NotNull Number Old OldPtr Put PutNull Set SetNull SetPtr Text Value ValueIsNull ValuePtr column",
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

// TestHandleDocsNameOwnMethods: a handle method's doc names another method
// of the handle (OldPtr, NewPtr) only on a kind that has it: the text the
// kinds share does not point a non-Null kind at a method it lacks.
func TestHandleDocsNameOwnMethods(t *testing.T) {
	t.Parallel()
	f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(repoRoot(), "handles.go"), nil, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	methods := map[string]map[string]bool{}
	docs := map[string]map[string]string{}
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
		if methods[recv] == nil {
			methods[recv], docs[recv] = map[string]bool{}, map[string]string{}
		}
		methods[recv][fd.Name.Name] = true
		docs[recv][fd.Name.Name] = fd.Doc.Text()
	}
	for recv, ds := range docs {
		for name, doc := range ds {
			for _, other := range []string{"OldPtr", "NewPtr"} {
				if other != name && strings.Contains(doc, other) && !methods[recv][other] {
					t.Errorf("%s.%s's doc names %s, which %s lacks", recv, name, other, recv)
				}
			}
		}
	}
}
