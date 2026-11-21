package graphqlgo

import (
	"runtime"
	"testing"
	"time"

	"github.com/syssam/graphql-go"
)

// TestAdapterDropped: an executor's adapter is dropped from adapters when
// the executor is collected, so the registry holds no executor alive.
func TestAdapterDropped(t *testing.T) {
	s, err := graphql.NewSchema(graphql.SDL(`type Query { n: Int }`),
		graphql.Query(graphql.Field("n", func(graphql.Root) int { return 1 })))
	if err != nil {
		t.Fatal(err)
	}
	count := func() (n int) {
		adapters.Range(func(any, any) bool { n++; return true })
		return n
	}
	before := count()
	func() {
		e := graphql.NewExecutor(s, Cost(), graphql.WithMaxDepth(5))
		if count() != before+1 {
			t.Fatal("no adapter registered")
		}
		runtime.KeepAlive(e)
	}()
	for range 50 {
		runtime.GC()
		if count() == before {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("%d adapters, want %d after the executor is collected", count(), before)
}
