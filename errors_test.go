package velox

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// TestSentinelsAreBarePhrases: sentinels are always returned wrapped in an
// error that says "velox: <Entity>: ", so their own text has no prefix and
// the prefix never doubles.
func TestSentinelsAreBarePhrases(t *testing.T) {
	for _, err := range []error{ErrNotFound, ErrNotSingular, ErrUnbounded, ErrNoWhere, ErrLimitTooLarge, ErrTooManyRows, ErrTimeout, ErrNoShard} {
		if s := err.Error(); strings.HasPrefix(s, "velox") || strings.Contains(s, ": ") {
			t.Errorf("%q is not a bare phrase", s)
		}
	}
}

func TestSchemaErrorNamesEntityAndField(t *testing.T) {
	cases := []struct {
		err  *SchemaError
		want string
	}{
		{&SchemaError{Entity: "billing.Invoice", Field: "Total", Reason: "bad"}, "velox: billing.Invoice.Total: bad"},
		{&SchemaError{Entity: "billing.Invoice", Reason: "no primary key"}, "velox: billing.Invoice: no primary key"},
	}
	for _, c := range cases {
		if got := c.err.Error(); got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
	var se *SchemaError
	if !errors.As(error(cases[0].err), &se) {
		t.Error("errors.As does not find *SchemaError")
	}
}

// TestSentinelDocs: every exported Err sentinel's doc opens with its name and
// " is returned", as Go's own error docs do.
func TestSentinelDocs(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "errors.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, s := range gd.Specs {
			vs := s.(*ast.ValueSpec)
			for _, id := range vs.Names {
				if !id.IsExported() || !strings.HasPrefix(id.Name, "Err") {
					continue
				}
				n++
				if vs.Doc == nil || !strings.HasPrefix(vs.Doc.Text(), id.Name+" is returned") {
					t.Errorf("%s: doc does not open with %q", id.Name, id.Name+" is returned")
				}
			}
		}
	}
	if n == 0 {
		t.Error("no sentinel found")
	}
}
