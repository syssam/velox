package gqlimpl

import (
	"context"
	"errors"
	"testing"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
)

// codeOf is the code of err's *gqlcore.Error, or "".
func codeOf(err error) string {
	var e *gqlcore.Error
	if !errors.As(err, &e) {
		return ""
	}
	return e.Code
}

// TestMatched: a write that matched fewer rows than its distinct IDs fails
// as CheckExists does; one that matched them all, or more, or whose count is
// unknown, does not fail as NOT_FOUND.
func TestMatched(t *testing.T) {
	for _, c := range []struct {
		got  int64
		n    int
		want string
	}{
		{2, 2, ""},
		{0, 0, ""},
		{0, 1, "veloxgql: CreateUserInput.postIDs: no Post has this ID"},
		{1, 3, "veloxgql: CreateUserInput.postIDs: 2 of the 3 IDs name no Post"},
	} {
		err := matched(c.got, c.n, "CreateUserInput.postIDs", "Post")
		switch {
		case c.want == "" && err != nil:
			t.Errorf("matched(%d, %d) = %v, want nil", c.got, c.n, err)
		case c.want != "" && (err == nil || err.Error() != c.want || codeOf(err) != veloxgql.CodeNotFound):
			t.Errorf("matched(%d, %d) = %v (code %q), want %q, NOT_FOUND", c.got, c.n, err, codeOf(err), c.want)
		}
	}
	if err := matched(3, 2, "at", "Post"); err == nil || codeOf(err) != "" {
		t.Errorf("matched(3, 2) = %v (code %q), want an internal error", err, codeOf(err))
	}
	// -1, a count the driver did not report, is not a short count.
	if err := matched(-1, 1, "at", "Post"); err == nil || err.Error() != "veloxgql: at: row count unknown" || codeOf(err) != "" {
		t.Errorf("matched(-1, 1) = %v (code %q), want the internal error \"veloxgql: at: row count unknown\"", err, codeOf(err))
	}
}

// TestNotZero: only the field's zero value is refused.
func TestNotZero(t *testing.T) {
	if err := NotZero("x", ""); codeOf(err) != "BAD_USER_INPUT" {
		t.Errorf(`NotZero("") = %v`, err)
	}
	if err := NotZero("x", 0); codeOf(err) != "BAD_USER_INPUT" {
		t.Errorf("NotZero(0) = %v", err)
	}
	if err := NotZero("x", "a"); err != nil {
		t.Errorf(`NotZero("a") = %v`, err)
	}
}

// TestGivenNull: a null is what the request says, as a literal or by a variable;
// a field or a variable left out is not one.
func TestGivenNull(t *testing.T) {
	object := func(c *ast.Value) *ast.Value {
		v := &ast.Value{Kind: ast.ObjectValue}
		if c != nil {
			v.Children = ast.ChildValueList{{Name: "n", Value: c}}
		}
		return v
	}
	input := &ast.Value{Kind: ast.Variable, Raw: "in"}
	for name, c := range map[string]struct {
		v    *ast.Value
		vars map[string]any
		want bool
	}{
		"no value":             {nil, nil, false},
		"a scalar":             {&ast.Value{Kind: ast.IntValue, Raw: "1"}, nil, false},
		"literal, left out":    {object(nil), nil, false},
		"literal null":         {object(&ast.Value{Kind: ast.NullValue}), nil, true},
		"literal value":        {object(&ast.Value{Kind: ast.IntValue, Raw: "1"}), nil, false},
		"inner variable null":  {object(&ast.Value{Kind: ast.Variable, Raw: "v"}), map[string]any{"v": nil}, true},
		"inner variable set":   {object(&ast.Value{Kind: ast.Variable, Raw: "v"}), map[string]any{"v": 1}, false},
		"inner variable unset": {object(&ast.Value{Kind: ast.Variable, Raw: "v"}), nil, false},
		"variable null":        {input, map[string]any{"in": map[string]any{"n": nil}}, true},
		"variable set":         {input, map[string]any{"in": map[string]any{"n": 1}}, false},
		"variable, left out":   {input, map[string]any{"in": map[string]any{}}, false},
		"variable unset":       {input, nil, false},
	} {
		if got := givenNull(c.v, "n", c.vars); got != c.want {
			t.Errorf("%s: givenNull = %v, want %v", name, got, c.want)
		}
	}
}

// TestNotNull: it reads the running field's argument, and refuses nothing outside
// a gqlgen field or for an argument of another name.
func TestNotNull(t *testing.T) {
	if err := NotNull(context.Background(), "x", "input", "n"); err != nil {
		t.Errorf("outside a field: %v", err)
	}
	in := &ast.Value{Kind: ast.ObjectValue, Children: ast.ChildValueList{{Name: "n", Value: &ast.Value{Kind: ast.NullValue}}}}
	ctx := graphql.WithOperationContext(context.Background(), &graphql.OperationContext{})
	ctx = graphql.WithFieldContext(ctx, &graphql.FieldContext{Field: graphql.CollectedField{Field: &ast.Field{
		Arguments: ast.ArgumentList{{Name: "input", Value: in}},
	}}})
	if err := NotNull(ctx, "x", "input", "n"); codeOf(err) != "BAD_USER_INPUT" {
		t.Errorf("a given null: %v", err)
	}
	if err := NotNull(ctx, "x", "other", "n"); err != nil {
		t.Errorf("another argument: %v", err)
	}
	if err := NotNull(ctx, "x", "input", "m"); err != nil {
		t.Errorf("another field: %v", err)
	}
}
