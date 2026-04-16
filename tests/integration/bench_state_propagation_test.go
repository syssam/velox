package integration_test

import (
	"context"
	"testing"

	"github.com/syssam/velox/runtime"
	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/integration/entity"
)

// BenchmarkQueryConstruction measures the per-call cost and allocations
// of constructing a fresh client.User.Query(). Pre-SP-2 this copies the
// client interceptor slice into a per-query field; post-SP-2 it stores
// a shared *entity.InterceptorStore pointer instead. This benchmark is
// the primary success metric for the SP-2 refactor.
func BenchmarkQueryConstruction(b *testing.B) {
	client := openBenchClient(b)
	defer client.Close()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = client.User.Query()
	}
}

// BenchmarkQueryWithEagerLoad measures construction cost when an
// eager-load is chained on. Pre-SP-2 each child query also copies the
// interceptor slice; post-SP-2 each child gets a pointer copy.
func BenchmarkQueryWithEagerLoad(b *testing.B) {
	client := openBenchClient(b)
	defer client.Close()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = client.User.Query().WithPosts()
	}
}

// BenchmarkInterceptorChain measures the per-query execution cost of
// running through a chain of 5 registered interceptors. SP-2 should
// not regress this — it's the read path, which is the same shape
// before and after.
func BenchmarkInterceptorChain(b *testing.B) {
	client := openBenchClient(b)
	defer client.Close()
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		client.User.Intercept(runtime.InterceptFunc(func(next runtime.Querier) runtime.Querier {
			return runtime.QuerierFunc(func(ctx context.Context, q runtime.Query) (runtime.Value, error) {
				return next.Query(ctx, q)
			})
		}))
	}
	if _, err := client.User.Create().SetName("seed").SetEmail("s@x").SetAge(1).Save(ctx); err != nil {
		b.Fatalf("seed: %v", err)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := client.User.Query().All(ctx); err != nil {
			b.Fatalf("All: %v", err)
		}
	}
}

// BenchmarkClientIntercept measures the cost of registering a new
// interceptor on a client. Should be O(1) constant.
func BenchmarkClientIntercept(b *testing.B) {
	client := openBenchClient(b)
	defer client.Close()
	inter := runtime.InterceptFunc(func(next runtime.Querier) runtime.Querier {
		return next
	})
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		client.User.Intercept(inter)
	}
}

// Compile-time guard so unused imports don't drift if entity types are
// renamed mid-refactor.
var _ = entity.User{}
var _ integration.Mutator = nil

// SP-2 Phase 0 baseline (2026-04-11, Apple M3 Max, modernc.org/sqlite, in-memory):
//
//   BenchmarkQueryConstruction-14    ~112 ns/op    304 B/op    2 allocs/op
//   BenchmarkQueryWithEagerLoad-14   ~218 ns/op    624 B/op    4 allocs/op
//   BenchmarkInterceptorChain-14   ~10700 ns/op   3536 B/op   77 allocs/op
//   BenchmarkClientIntercept-14      ~48 ns/op     104 B/op    1 allocs/op
//
// QueryConstruction's 2 allocs = (1) the *UserQuery struct itself, (2) the
// slice copy of `inters []Interceptor` from c.Interceptors() into q.inters.
// SP-2 replaces the slice with a *entity.InterceptorStore pointer; expected
// post-refactor result: 1 alloc/op (just the struct, no slice copy) and
// ~half the bytes. QueryWithEagerLoad's 4 allocs = parent + child query;
// expected post-refactor: 2 allocs/op.
//
// InterceptorChain measures the read path (chain execution) and is expected
// to stay flat or improve marginally. ClientIntercept (slice append) is
// architecturally unchanged.
