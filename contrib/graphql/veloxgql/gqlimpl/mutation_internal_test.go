package gqlimpl

import (
	"context"
	"errors"
	"strings"
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

// fake is a Mutation of the input In, for the tests of its helpers.
type fake struct{ s inputState }

func (f *fake) state() *inputState { return &f.s }

func newFake(ctx context.Context) *fake {
	return &fake{s: inputState{ctx: ctx, input: "In", typ: "T", entity: "gen.T"}}
}

// TestNotZero: only the field's zero value is refused.
func TestNotZero(t *testing.T) {
	codeAfter := func(f func(m *fake)) string {
		m := newFake(context.Background())
		f(m)
		return codeOf(m.s.err)
	}
	if got := codeAfter(func(m *fake) { NotZero(m, "x", "") }); got != "BAD_USER_INPUT" {
		t.Errorf(`NotZero("") = %q`, got)
	}
	if got := codeAfter(func(m *fake) { NotZero(m, "x", 0) }); got != "BAD_USER_INPUT" {
		t.Errorf("NotZero(0) = %q", got)
	}
	if got := codeAfter(func(m *fake) { NotZero(m, "x", "a") }); got != "" {
		t.Errorf(`NotZero("a") = %q`, got)
	}
	m := newFake(context.Background())
	NotZero(m, "x", "")
	if m.s.err == nil || !strings.HasPrefix(m.s.err.Error(), "veloxgql: In.x: invalid value: ") {
		t.Errorf("NotZero names %v, want In.x", m.s.err)
	}
}

// TestDisjoint: a key in both lists is BAD_USER_INPUT naming the input and
// both fields, and echoes no ID; lists with no key in common, empty ones
// included, are not an error.
func TestDisjoint(t *testing.T) {
	for _, c := range []struct{ add, remove []int64 }{{nil, nil}, {[]int64{1}, nil}, {nil, []int64{1}}, {[]int64{1, 2}, []int64{3}}} {
		if err := disjoint("UpdatePostInput", "addTagIDs", "removeTagIDs", c.add, c.remove); err != nil {
			t.Errorf("disjoint(%v, %v) = %v, want nil", c.add, c.remove, err)
		}
	}
	m := newFake(context.Background())
	m.s.input = "UpdatePostInput"
	Disjoint(m, "addTagIDs", "removeTagIDs", []int64{1, 2, 3}, []int64{4, 3})
	if err := m.s.err; codeOf(err) != veloxgql.CodeBadUserInput || err.Error() != "veloxgql: UpdatePostInput: an ID is in both addTagIDs and removeTagIDs" {
		t.Errorf("Disjoint = %v (code %q)", err, codeOf(err))
	}
}

// TestErrorOrder: a decode error wins over any other, whichever came first,
// and the first of each kind wins over the later ones.
func TestErrorOrder(t *testing.T) {
	m := newFake(context.Background())
	NotZero(m, "a", "")
	NotZero(m, "b", "")
	_ = Key[int64](m, "c", "not an ID", "T")
	_ = Key[int64](m, "d", "not an ID", "T")
	if err := m.s.failed(); err == nil || !strings.HasPrefix(err.Error(), "veloxgql: In.c: ") {
		t.Errorf("failed = %v, want In.c's decode error", err)
	}
	m = newFake(context.Background())
	NotZero(m, "a", "")
	NotZero(m, "b", "")
	if err := m.s.failed(); err == nil || !strings.HasPrefix(err.Error(), "veloxgql: In.a: ") {
		t.Errorf("failed = %v, want In.a's error", err)
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

// fieldCtx is a context in a gqlgen field whose arguments are args, with
// the operation's variables vars.
func fieldCtx(vars map[string]any, args ...*ast.Argument) context.Context {
	ctx := graphql.WithOperationContext(context.Background(), &graphql.OperationContext{Variables: vars})
	return graphql.WithFieldContext(ctx, &graphql.FieldContext{Field: graphql.CollectedField{Field: &ast.Field{Arguments: args}}})
}

var inDef = &ast.Definition{Kind: ast.InputObject, Name: "In", Fields: ast.FieldList{{Name: "n", Type: ast.NamedType("Int", nil)}}}

// nullIn is a literal of the input In that gives n as null.
func nullIn() *ast.Value {
	return &ast.Value{Kind: ast.ObjectValue, Definition: inDef, Children: ast.ChildValueList{{Name: "n", Value: &ast.Value{Kind: ast.NullValue}}}}
}

// TestNotNull: it reads the running field's arguments for a value of the
// input's type, whatever the argument's name, and refuses nothing outside a
// gqlgen field, for a value of another type, for another field, or for a field
// given in Go.
func TestNotNull(t *testing.T) {
	refused := func(ctx context.Context, field string, given bool) bool {
		m := newFake(ctx)
		NotNull(m, field, given)
		return codeOf(m.s.err) == "BAD_USER_INPUT"
	}
	if refused(context.Background(), "n", false) {
		t.Error("outside a field: refused")
	}
	ctx := fieldCtx(nil, &ast.Argument{Name: "data", Value: nullIn()})
	if !refused(ctx, "n", false) {
		t.Error("a given null: not refused")
	}
	if refused(ctx, "n", true) {
		t.Error("a field given in Go: refused")
	}
	if refused(ctx, "m", false) {
		t.Error("another field: refused")
	}
	other := nullIn()
	other.Definition = &ast.Definition{Kind: ast.InputObject, Name: "Other"}
	if refused(fieldCtx(nil, &ast.Argument{Name: "input", Value: other}), "n", false) {
		t.Error("another input type: refused")
	}
}

// TestCleared: one value of the input clears by its null, a literal or a
// variable, at any depth; several refuse a null in any of them, naming the
// clear field, and are fine without one; none clears nothing.
func TestCleared(t *testing.T) {
	schema := &ast.Schema{Types: map[string]*ast.Definition{
		"In":   inDef,
		"Wrap": {Kind: ast.InputObject, Name: "Wrap", Fields: ast.FieldList{{Name: "in", Type: ast.NamedType("In", nil)}, {Name: "ins", Type: ast.ListType(ast.NamedType("In", nil), nil)}}},
	}}
	wrap := schema.Types["Wrap"]
	cleared := func(ctx context.Context) (bool, error) {
		m := newFake(ctx)
		got := Cleared(m, "n", false, "clearN", false)
		return got, m.s.err
	}
	setIn := &ast.Value{Kind: ast.ObjectValue, Definition: inDef, Children: ast.ChildValueList{{Name: "n", Value: &ast.Value{Kind: ast.IntValue, Raw: "1"}}}}
	listOf := func(vs ...*ast.Value) *ast.Value {
		l := &ast.Value{Kind: ast.ListValue, Definition: inDef}
		for _, v := range vs {
			l.Children = append(l.Children, &ast.ChildValue{Value: v})
		}
		return l
	}
	withSchema := func(ctx context.Context) context.Context {
		return gqlcore.WithTable(ctx, schema, nil)
	}
	for name, c := range map[string]struct {
		ctx       context.Context
		want      bool
		wantError bool
	}{
		"outside a field":  {context.Background(), false, false},
		"one literal null": {fieldCtx(nil, &ast.Argument{Name: "x", Value: nullIn()}), true, false},
		"one variable null": {fieldCtx(map[string]any{"v": map[string]any{"n": nil}},
			&ast.Argument{Name: "x", Value: &ast.Value{Kind: ast.Variable, Raw: "v", Definition: inDef}}), true, false},
		"nested in a literal": {fieldCtx(nil, &ast.Argument{Name: "x", Value: &ast.Value{Kind: ast.ObjectValue, Definition: wrap,
			Children: ast.ChildValueList{{Name: "in", Value: nullIn()}}}}), true, false},
		"nested in a variable": {withSchema(fieldCtx(map[string]any{"w": map[string]any{"in": map[string]any{"n": nil}}},
			&ast.Argument{Name: "x", Value: &ast.Value{Kind: ast.Variable, Raw: "w", Definition: wrap}})), true, false},
		"several, one null": {fieldCtx(nil, &ast.Argument{Name: "x", Value: listOf(setIn, nullIn())}), false, true},
		"several in a variable, one null": {withSchema(fieldCtx(map[string]any{"w": map[string]any{"ins": []any{map[string]any{"n": 1}, map[string]any{"n": nil}}}},
			&ast.Argument{Name: "x", Value: &ast.Value{Kind: ast.Variable, Raw: "w", Definition: wrap}})), false, true},
		"two arguments, one null": {fieldCtx(nil, &ast.Argument{Name: "a", Value: setIn}, &ast.Argument{Name: "b", Value: nullIn()}), false, true},
		"several, no null":        {fieldCtx(nil, &ast.Argument{Name: "x", Value: listOf(setIn, setIn)}), false, false},
	} {
		got, err := cleared(c.ctx)
		if got != c.want || (err != nil) != c.wantError {
			t.Errorf("%s: Cleared = %v, %v; want %v, error %v", name, got, err, c.want, c.wantError)
		}
		if err != nil && (codeOf(err) != "BAD_USER_INPUT" || !strings.Contains(err.Error(), "clearN")) {
			t.Errorf("%s: %v, want BAD_USER_INPUT naming clearN", name, err)
		}
	}
	m := newFake(context.Background())
	if Cleared(m, "n", true, "clearN", true) || m.s.err == nil || m.s.err.Error() != "veloxgql: In: n and clearN together" {
		t.Errorf("both given: %v", m.s.err)
	}
}

// TestClearEdge: a null for the edge's ID is refused, naming the clear field;
// both given is refused; outside a field nothing is.
func TestClearEdge(t *testing.T) {
	m := newFake(context.Background())
	if ClearEdge(m, "n", false, "clearN", false) || m.s.err != nil {
		t.Errorf("outside a field: %v", m.s.err)
	}
	m = newFake(fieldCtx(nil, &ast.Argument{Name: "x", Value: nullIn()}))
	if ClearEdge(m, "n", false, "clearN", false) || codeOf(m.s.err) != "BAD_USER_INPUT" || !strings.Contains(m.s.err.Error(), "clearN") {
		t.Errorf("a null: %v", m.s.err)
	}
	m = newFake(context.Background())
	if !ClearEdge(m, "n", false, "clearN", true) || m.s.err != nil {
		t.Errorf("clear: %v", m.s.err)
	}
	m = newFake(context.Background())
	if ClearEdge(m, "n", true, "clearN", true) || m.s.err == nil || m.s.err.Error() != "veloxgql: In: n and clearN together" {
		t.Errorf("both given: %v", m.s.err)
	}
}
