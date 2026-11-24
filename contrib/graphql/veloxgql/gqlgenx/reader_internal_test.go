package gqlgenx

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/ast"
)

// rootCount is the number of roots with a state.
func rootCount() int {
	n := 0
	roots.Range(func(any, any) bool { n++; return true })
	return n
}

// TestReaderRoots: the reader gives every field under one root the root's
// table, another root another table, nothing outside a gqlgen field, and
// drops a root's state once the root is unreachable, so a server with no
// Extension keeps no rows after its responses.
func TestReaderRoots(t *testing.T) {
	base := rootCount()
	oc := &graphql.OperationContext{Doc: &ast.QueryDocument{}}
	ctx := graphql.WithOperationContext(context.Background(), oc)
	field := func(ctx context.Context, name string) context.Context {
		return graphql.WithFieldContext(ctx, &graphql.FieldContext{Object: "Query", Field: graphql.CollectedField{Field: &ast.Field{Name: name}}})
	}
	func() {
		a, b := field(ctx, "users"), field(ctx, "posts")
		child := field(a, "edges")
		fa, ok := reader{}.Field(a)
		fc, _ := reader{}.Field(child)
		fb, _ := reader{}.Field(b)
		if !ok || fa.Table == nil || fc.Table != fa.Table || fb.Table == fa.Table {
			t.Errorf("tables: root %p, its child %p, another root %p: want the child's the root's, the other its own", fa.Table, fc.Table, fb.Table)
		}
		if n := rootCount() - base; n != 2 {
			t.Errorf("%d roots with a state, want 2", n)
		}
		runtime.KeepAlive(a)
		runtime.KeepAlive(b)
		runtime.KeepAlive(child)
	}()
	if _, ok := (reader{}).Field(ctx); ok || (reader{}).In(ctx) {
		t.Error("a field outside a gqlgen field")
	}
	for deadline := time.Now().Add(10 * time.Second); rootCount() > base; {
		if time.Now().After(deadline) {
			t.Fatalf("%d roots kept after their fields became unreachable", rootCount()-base)
		}
		runtime.GC()
		time.Sleep(time.Millisecond)
	}
}
