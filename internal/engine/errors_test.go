package engine

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net"
	"strings"
	"testing"
)

// TestSentinelsAreBarePhrases: sentinels are always returned wrapped in an
// error that says "velox: <Entity>: ", so their own text has no prefix and
// the prefix never doubles.
func TestSentinelsAreBarePhrases(t *testing.T) {
	for _, err := range []error{ErrNotFound, ErrNotSingular, ErrUnbounded, ErrNoWhere, ErrLimitTooLarge, ErrTooManyRows, ErrOutOfRange, ErrTimeout, ErrNoShard} {
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
	if _, ok := errors.AsType[*SchemaError](error(cases[0].err)); !ok {
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

// TestIsNotFound: IsNotFound is errors.Is(err, ErrNotFound) that skips a nil
// pointer in err's tree, where errors.Is would call Unwrap on it and panic
// (the typed nils of errors_internal_test.go's TestTypedNilDriverErrors).
func TestIsNotFound(t *testing.T) {
	for _, c := range []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"sentinel", ErrNotFound, true},
		{"wrapped", fmt.Errorf("x: %w", ErrNotFound), true},
		{"joined", errors.Join(errors.New("a"), ErrNotFound), true},
		{"other", errors.New("not found"), false},
		{"typed nil before", errors.Join((*net.OpError)(nil), ErrNotFound), true},
		{"typed nil after", errors.Join(errors.New("x"), (*net.OpError)(nil)), false},
		{"typed nil wrapped by %w", fmt.Errorf("x: %w", (*net.OpError)(nil)), false},
		{"typed nil wrapped by %w, then the sentinel", fmt.Errorf("x: %w, %w", (*net.OpError)(nil), ErrNotFound), true},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := IsNotFound(c.err); got != c.want {
				t.Errorf("IsNotFound(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}
