package graphqlgo

import (
	"runtime"
	"testing"
	"time"
	"weak"

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
	k := func() weak.Pointer[graphql.Executor] {
		e := graphql.NewExecutor(s, MutationTx(), graphql.WithMaxDepth(5))
		k := weak.Make(e)
		if _, ok := adapters.Load(k); !ok {
			t.Fatal("no adapter registered")
		}
		runtime.KeepAlive(e)
		return k
	}()
	for range 100 {
		runtime.GC()
		if _, ok := adapters.Load(k); !ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("the adapter is still registered after the executor is collected")
}
