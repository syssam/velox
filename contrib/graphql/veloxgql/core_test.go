package veloxgql_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
	"github.com/syssam/velox/scope"
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
	} {
		if code, msg := veloxgql.Present(c.err); code != c.code || msg != c.msg {
			t.Errorf("%s: %s %q, want %s %q", c.name, code, msg, c.code, c.msg)
		}
	}
}

// TestCursorPlainMarshalers: MarshalGQL and UnmarshalGQL, graphql-go's, are
// the context pair's. A cursor that does not encode panics in MarshalGQL
// (TestEncodeCursorRefuses).
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
	if veloxgql.OrderDirectionAsc.String() != "ASC" {
		t.Error("OrderDirection.String")
	}
}

// fieldKey marks a context fieldReader finds a field in; its value is the
// field's table.
type fieldKey struct{}

// fieldReader is a gqlcore.Reader of a fake engine: it finds a field, with
// the table the context holds, in a context marked fieldKey.
type fieldReader struct{}

func (fieldReader) In(ctx context.Context) bool { return ctx.Value(fieldKey{}) != nil }

func (r fieldReader) Field(ctx context.Context) (gqlcore.Field, bool) {
	t, _ := ctx.Value(fieldKey{}).(*gqlcore.Table)
	return gqlcore.Field{Object: "Q", Name: "rows", Table: t}, r.In(ctx)
}

func (fieldReader) Wait(context.Context) func() { return func() {} }

var registerFieldReader sync.Once

// TestBatch: Batch makes rows one group of the response's table inside a
// field, and does nothing outside one.
func TestBatch(t *testing.T) {
	registerFieldReader.Do(func() { gqlcore.Register(fieldReader{}) })
	table := gqlcore.NewTable()
	a, b := new(int), new(int)
	veloxgql.Batch(context.Background(), []*int{a})
	if _, _, _, ok := gqlcore.Find(table, any(a)); ok {
		t.Error("Batch outside a field grouped a row")
	}
	in := context.WithValue(context.Background(), fieldKey{}, table)
	veloxgql.Batch(in, []*int{a, b})
	if _, rows, i, ok := gqlcore.Find(table, any(b)); !ok || len(rows) != 2 || i != 1 {
		t.Errorf("Batch in a field: %v %d %v", rows, i, ok)
	}
}
