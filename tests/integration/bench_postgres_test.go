package integration_test

import (
	"context"
	"fmt"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/syssam/velox/tests/integration/user"
)

// pgBenchSeq is a process-global monotonic counter used to disambiguate
// unique-key inserts across Go's benchmark recalibration loop (which
// invokes the benchmark function multiple times with increasing b.N).
// Without this, row i=0 from the N=1 warmup would collide with row i=0
// from the N=10000 measurement run on UNIQUE(email).
var pgBenchSeq atomic.Int64

// Postgres-specific benchmarks. SQLite numbers are in bench_bulk_test.go
// and bench_updateone_test.go; these give the real-network story for the
// ops users actually issue in services. All skip cleanly without Postgres
// (openPostgresOrSkip) so `go test -bench=.` stays local-friendly.
//
// Run against a local docker postgres:
//
//	docker run -e POSTGRES_PASSWORD=postgres -p 5432:5432 -d postgres:18
//	VELOX_TEST_POSTGRES='postgres://postgres:postgres@localhost/postgres?sslmode=disable' \
//	    go test -bench=_Postgres -benchmem ./tests/integration/
//
// Expected shape (M3 Max, postgres 18 localhost docker; ~100µs baseline
// round-trip on loopback): each op is dominated by driver round-trip,
// not velox overhead. Compare to SQLite (~10–30µs/op for the same op)
// to see the real-network tax — that's the number users need to reason
// about service latency budgets.

// BenchmarkCreate_Postgres measures single-row insert latency end-to-end
// through the velox client (privacy + hooks + mutation + scan).
// Complement to BenchmarkCreate_SingleRowLoop/SQLite.
func BenchmarkCreate_Postgres(b *testing.B) {
	client, cleanup := openPostgresOrSkip(b)
	defer cleanup()
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		seq := pgBenchSeq.Add(1)
		if _, err := client.User.Create().
			SetName(fmt.Sprintf("u%d", seq)).
			SetEmail(fmt.Sprintf("u%d@ex.com", seq)).
			SetAge(30).
			SetRole(user.RoleUser).
			SetCreatedAt(now).
			SetUpdatedAt(now).
			Save(ctx); err != nil {
			b.Fatalf("create: %v", err)
		}
		_ = i
	}
}

// BenchmarkCreateBulk_Postgres exercises the mutator-chain bulk path
// with a single round-trip BatchCreate per Save. Sized at 100 and 1000
// to show per-row amortization across Postgres network cost.
func BenchmarkCreateBulk_Postgres(b *testing.B) {
	for _, size := range []int{100, 1000} {
		b.Run("n="+strconv.Itoa(size), func(b *testing.B) {
			client, cleanup := openPostgresOrSkip(b)
			defer cleanup()
			ctx := context.Background()

			names := make([]string, size)
			for j := range names {
				names[j] = "u" + strconv.Itoa(j)
			}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				// Monotonic per-iteration prefix guarantees unique emails
				// across Go's benchmark-recalibration re-invocation.
				prefix := "run" + strconv.FormatInt(pgBenchSeq.Add(1), 10) + "-"
				b.StartTimer()

				if _, err := client.User.MapCreateBulk(names, func(c *user.UserCreate, j int) {
					c.SetName(prefix + names[j]).
						SetEmail(prefix + names[j] + "@ex.com").
						SetAge(30).
						SetRole(user.RoleUser).
						SetCreatedAt(now).
						SetUpdatedAt(now)
				}).Save(ctx); err != nil {
					b.Fatalf("bulk save (size=%d): %v", size, err)
				}
				_ = i
			}
		})
	}
}

// BenchmarkQueryOnly_Postgres measures single-row read by primary key —
// the most common query shape (e.g. GraphQL node resolver, task lookup).
// The round-trip-dominated number here is what services budget against.
func BenchmarkQueryOnly_Postgres(b *testing.B) {
	client, cleanup := openPostgresOrSkip(b)
	defer cleanup()
	ctx := context.Background()

	u, err := client.User.Create().
		SetName("seed").
		SetEmail("seed@ex.com").
		SetAge(30).
		SetRole(user.RoleUser).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	if err != nil {
		b.Fatalf("seed: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := client.User.Query().
			Where(user.IDField.EQ(u.ID)).
			Only(ctx); err != nil {
			b.Fatalf("only: %v", err)
		}
	}
}

// BenchmarkQueryAll_Postgres measures multi-row read with a realistic
// result size (100 rows) — exercises scan/hydration throughput, not just
// protocol round-trip.
func BenchmarkQueryAll_Postgres(b *testing.B) {
	client, cleanup := openPostgresOrSkip(b)
	defer cleanup()
	ctx := context.Background()

	const rows = 100
	names := make([]string, rows)
	for j := range names {
		names[j] = "u" + strconv.Itoa(j)
	}
	if _, err := client.User.MapCreateBulk(names, func(c *user.UserCreate, j int) {
		c.SetName(names[j]).
			SetEmail(names[j] + "@ex.com").
			SetAge(30).
			SetRole(user.RoleUser).
			SetCreatedAt(now).
			SetUpdatedAt(now)
	}).Save(ctx); err != nil {
		b.Fatalf("seed: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out, err := client.User.Query().All(ctx)
		if err != nil {
			b.Fatalf("all: %v", err)
		}
		if len(out) < rows {
			b.Fatalf("unexpected result count: %d", len(out))
		}
	}
}
