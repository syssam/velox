package veloxgql_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
	"github.com/syssam/velox/scope"
	"github.com/vektah/gqlparser/v2/ast"
)

// TestPresent: the code and message a client sees, of each kind of error,
// and "internal error" for anything else. An engine's own errors are its
// adapter's: gqlgen's integer error is internal here.
func TestPresent(t *testing.T) {
	for _, c := range []struct {
		name      string
		err       error
		code, msg string
	}{
		{"coded", gqlcore.InputErrorf("X must be %s", "Y"), veloxgql.CodeBadUserInput, "X must be Y"},
		{"no scope", fmt.Errorf("velox: gen.Post: %w", scope.ErrNoScope), veloxgql.CodeUnauthenticated, "unauthenticated"},
		{"denied", fmt.Errorf("velox: gen.Post: %w", velox.ErrDenied), veloxgql.CodeForbidden, "forbidden"},
		{"scope mismatch", scope.ErrMismatch, veloxgql.CodeForbidden, "forbidden"},
		{"linked", &velox.EdgeError{Edge: "tags", Err: velox.ErrLinked}, veloxgql.CodeBadUserInput, "edge tags: already linked to another owner"},
		{"one key not found", &velox.EdgeError{Edge: "tags", Keys: []any{1}, Err: velox.ErrNotFound}, veloxgql.CodeNotFound, "edge tags: no node has this ID"},
		{"keys not found", &velox.EdgeError{Edge: "tags", Keys: []any{1, 2}, Err: velox.ErrNotFound}, veloxgql.CodeNotFound, "edge tags: an ID names no node"},
		{"another edge error", &velox.EdgeError{Edge: "tags", Err: errors.New("driver")}, veloxgql.CodeInternal, "internal error"},
		{"validation", &velox.ValidationError{Entity: "gen.User", Field: "Email", Err: errors.New("must hold an @")}, veloxgql.CodeBadUserInput, "invalid value: must hold an @"},
		{"validation of no error", &velox.ValidationError{Entity: "gen.User", Field: "Email"}, veloxgql.CodeBadUserInput, "invalid value"},
		{"constraint", &velox.ConstraintError{Entity: "gen.User", Kind: velox.ConstraintUnique, Constraint: "users_email_key"}, veloxgql.CodeBadUserInput, "unique constraint violated"},
		{"no DB", veloxgql.ErrNoDB, veloxgql.CodeInternal, "internal error"},
		{"no source", veloxgql.ErrNoSelectionSource, veloxgql.CodeInternal, "internal error"},
	} {
		if code, msg := veloxgql.Present(c.err); code != c.code || msg != c.msg {
			t.Errorf("%s: %s %q, want %s %q", c.name, code, msg, c.code, c.msg)
		}
	}
}

// TestConfig: NewConfig starts from DefaultLimits() and applies each
// option, the last of two that set one thing winning, appends each
// AdapterOption's value, and panics on a zero Option.
func TestConfig(t *testing.T) {
	filter := func(context.Context, *ast.OperationDefinition) bool { return false }
	scoped := func(context.Context) any { return 1 }
	c := veloxgql.NewConfig(veloxgql.WithLimits(veloxgql.Limits{MaxPage: 3}), veloxgql.WithTxOptions(velox.TxReadOnly()),
		veloxgql.WithTxFilter(filter), veloxgql.WithBatchScope(scoped), veloxgql.WithLimits(veloxgql.Limits{MaxPage: 4}))
	if c.Limits.MaxPage != 4 || c.Limits.MaxCost != veloxgql.DefaultLimits().MaxCost || len(c.TxOptions) != 1 || c.TxFilter == nil || c.BatchScope == nil {
		t.Errorf("NewConfig = %+v", c)
	}
	if d := veloxgql.NewConfig(); d.Limits != veloxgql.DefaultLimits() || d.TxOptions != nil || d.TxFilter != nil || d.BatchScope != nil || d.Adapter != nil {
		t.Errorf("NewConfig() = %+v", d)
	}
	if a := veloxgql.NewConfig(veloxgql.AdapterOption("a"), veloxgql.WithLimits(veloxgql.Limits{}), veloxgql.AdapterOption(2)).Adapter; !slices.Equal(a, []any{"a", 2}) {
		t.Errorf("AdapterOption: Adapter = %v, want [a 2], in order", a)
	}
	defer func() {
		if recover() == nil {
			t.Error("NewConfig(Option{}) did not panic")
		}
	}()
	veloxgql.NewConfig(veloxgql.Option{})
}

// TestCursorPlainMarshalers: MarshalGQL and UnmarshalGQL, graphql-go's, are
// the context pair's, and a cursor that does not encode writes null.
func TestCursorPlainMarshalers(t *testing.T) {
	c := veloxgql.Cursor{Type: "Post", Key: int64(7)}
	var plain, withCtx bytes.Buffer
	c.MarshalGQL(&plain)
	if err := c.MarshalGQLContext(context.Background(), &withCtx); err != nil || plain.String() != withCtx.String() {
		t.Errorf("MarshalGQL %s, MarshalGQLContext %s, %v", plain.String(), withCtx.String(), err)
	}
	var back veloxgql.Cursor
	if err := back.UnmarshalGQL(plain.String()[1 : plain.Len()-1]); err != nil || back.Type != "Post" || fmt.Sprint(back.Key) != "7" {
		t.Errorf("UnmarshalGQL = %+v, %v", back, err)
	}
	if err := back.UnmarshalGQL(1); !errors.Is(err, veloxgql.ErrBadCursor) {
		t.Errorf("UnmarshalGQL(1) = %v", err)
	}
	var bad bytes.Buffer
	veloxgql.Cursor{Type: "Post", Key: make(chan int)}.MarshalGQL(&bad)
	if bad.String() != "null" {
		t.Errorf("a cursor that does not encode wrote %s", bad.String())
	}
	if veloxgql.OrderDirectionAsc.String() != "ASC" {
		t.Error("OrderDirection.String")
	}
}

// inField marks a context fieldSource finds a field in.
type inField struct{}

// fieldSource finds a field in a context marked inField.
type fieldSource struct{}

func (fieldSource) Field(ctx context.Context) (gqlcore.Field, bool) {
	return gqlcore.Field{Object: "Q", Name: "rows"}, ctx.Value(inField{}) != nil
}
func (fieldSource) Wait(context.Context) func() { return func() {} }

// TestBatch: Batch makes rows one group of the response's table inside a
// field, and does nothing outside one.
func TestBatch(t *testing.T) {
	ctx := gqlcore.WithTable(gqlcore.Install(context.Background(), &velox.DB{}, nil, fieldSource{}), nil)
	a, b := new(int), new(int)
	veloxgql.Batch(ctx, []*int{a})
	if _, _, _, ok := gqlcore.Find(gqlcore.TableFromContext(ctx), any(a)); ok {
		t.Error("Batch outside a field grouped a row")
	}
	in := context.WithValue(ctx, inField{}, true)
	veloxgql.Batch(in, []*int{a, b})
	if _, rows, i, ok := gqlcore.Find(gqlcore.TableFromContext(ctx), any(b)); !ok || len(rows) != 2 || i != 1 {
		t.Errorf("Batch in a field: %v %d %v", rows, i, ok)
	}
}
