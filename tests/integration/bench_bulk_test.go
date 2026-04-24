package integration_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/syssam/velox/dialect"
	integration "github.com/syssam/velox/tests/integration"
	userclient "github.com/syssam/velox/tests/integration/client/user"
	"github.com/syssam/velox/tests/integration/user"

	_ "modernc.org/sqlite"
)

// BenchmarkCreateBulk measures bulk create throughput across the
// dimensions that matter for the mutator-chain rewrite: batch size
// and whether hooks are registered. If the chain is working, "hook"
// and "no-hook" at the same batch size should be close in time and
// allocations — a hook should not regress the batch path.
func BenchmarkCreateBulk(b *testing.B) {
	// Sizes kept under SQLite's default "too many SQL variables" cap
	// (32766 parameters in modernc.org/sqlite). User has 6 insertable
	// columns, so 5000 rows × 6 = 30000 params fits; 10000 rows × 6 =
	// 60000 params does not. TestCreateBulk_SQLiteParamLimit
	// documents the limit and the chunking workaround.
	for _, size := range []int{100, 1000, 5000} {
		for _, withHook := range []bool{false, true} {
			name := "n=" + strconv.Itoa(size)
			if withHook {
				name += "/hook"
			} else {
				name += "/no-hook"
			}
			b.Run(name, func(b *testing.B) {
				ctx := context.Background()
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					b.StopTimer()
					client := openBenchClient(b)
					if withHook {
						client.User.Use(noopBulkHook)
					}
					names := make([]string, size)
					for j := range names {
						names[j] = "u" + strconv.Itoa(j)
					}
					b.StartTimer()

					if _, err := client.User.MapCreateBulk(names, func(c *userclient.UserCreate, j int) {
						c.SetName(names[j]).
							SetEmail(names[j] + "@ex.com").
							SetAge(30).
							SetRole(user.RoleUser).
							SetCreatedAt(now).
							SetUpdatedAt(now)
					}).Save(ctx); err != nil {
						b.Fatalf("bulk save: %v", err)
					}

					b.StopTimer()
					client.Close()
				}
			})
		}
	}
}

// BenchmarkCreate_SingleRowLoop is the worst-case baseline: N
// single-row Saves in a for-loop, the shape you would write if bulk
// did not exist. The bulk benchmark should beat this by a large
// margin — that margin is the batching payoff the mutator chain
// rewrite preserves.
func BenchmarkCreate_SingleRowLoop(b *testing.B) {
	for _, size := range []int{100, 1000} {
		b.Run("n="+strconv.Itoa(size), func(b *testing.B) {
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				client := openBenchClient(b)
				b.StartTimer()

				for j := 0; j < size; j++ {
					name := "u" + strconv.Itoa(j)
					if _, err := client.User.Create().
						SetName(name).
						SetEmail(name + "@ex.com").
						SetAge(30).
						SetRole(user.RoleUser).
						SetCreatedAt(now).
						SetUpdatedAt(now).
						Save(ctx); err != nil {
						b.Fatalf("single-row save: %v", err)
					}
				}

				b.StopTimer()
				client.Close()
			}
		})
	}
}

// openBenchClient opens a fresh in-memory SQLite client for each
// benchmark iteration so tables are empty and unique constraints
// do not fire across runs.
func openBenchClient(b *testing.B) *integration.Client {
	b.Helper()
	client, err := integration.Open(dialect.SQLite, ":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	if err := client.Schema.Create(context.Background()); err != nil {
		b.Fatalf("schema: %v", err)
	}
	return client
}

// noopBulkHook is a pass-through hook used by the benchmark to prove
// that registering a hook does not force the bulk path off the batch
// fast lane.
func noopBulkHook(next integration.Mutator) integration.Mutator {
	return integration.MutateFunc(func(ctx context.Context, m integration.Mutation) (integration.Value, error) {
		return next.Mutate(ctx, m)
	})
}
