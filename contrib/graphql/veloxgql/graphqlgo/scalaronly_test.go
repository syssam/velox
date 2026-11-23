package graphqlgo_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/syssam/graphql-go"

	"github.com/syssam/velox/contrib/graphql/veloxgql/graphqlgo"
)

// TestScalarsOnly: a schema that declares Time and none of UUID, Bytes and
// Uint64 fails NewSchema with Scalars, and builds with ScalarsOnly("Time"),
// which binds Time as Scalars does, and Int to the small integer types. A
// name that is not one of velox's scalars panics.
func TestScalarsOnly(t *testing.T) {
	const sdl = `scalar Time
type Query { at(t: Time!): Time! small(n: Int!): Int! }`
	type atArgs struct{ T time.Time }
	type smallArgs struct{ N int8 }
	query := graphql.Query(
		graphql.ResolveArgs("at", func(_ context.Context, _ graphql.Root, a atArgs) (time.Time, error) { return a.T, nil }),
		graphql.ResolveArgs("small", func(_ context.Context, _ graphql.Root, a smallArgs) (int8, error) { return a.N, nil }),
	)
	if _, err := graphql.NewSchema(graphql.SDL(sdl), graphqlgo.Scalars(), query); err == nil || !strings.Contains(err.Error(), `"UUID"`) {
		t.Errorf("Scalars on a schema without UUID: %v, want NewSchema's refusal naming UUID", err)
	}
	s, err := graphql.NewSchema(graphql.SDL(sdl), graphqlgo.ScalarsOnly("Time"), query)
	if err != nil {
		t.Fatal(err)
	}
	srv := executorServer(newExecutor(s))
	if out, want := srv(t, context.Background(), `{ at(t: "2026-11-23T10:00:00+08:00") small(n: 7) }`, nil), `{"data":{"at":"2026-11-23T10:00:00+08:00","small":7}}`; out != want {
		t.Errorf("ScalarsOnly(\"Time\"):\n got %s\nwant %s", out, want)
	}
	defer func() {
		const want = `graphqlgo.ScalarsOnly: "Decimal" is not one of velox's scalars: Time, UUID, Bytes, Uint64`
		if got := recover(); got != want {
			t.Errorf("ScalarsOnly(\"Decimal\") panicked with %v, want %q", got, want)
		}
	}()
	graphqlgo.ScalarsOnly("Decimal")
	t.Error("ScalarsOnly(\"Decimal\") did not panic")
}
