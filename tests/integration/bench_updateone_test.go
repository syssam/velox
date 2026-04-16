package integration_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/syssam/velox/tests/integration/user"
)

// BenchmarkUpdateOne_SQLite measures UpdateOne latency on pure-Go SQLite
// through the full velox client path (privacy + hooks + mutation + schema
// validation + scan). Measured 2026-04-15 (modernc.org/sqlite, M3 Max):
// ~22µs/op, ~7200 B/op, 168 allocs/op.
//
// This is the realistic production-path baseline, not the 11.8µs number
// from the 2026-04-14 memo (that was a raw-sqlgraph micro-harness that
// skipped velox's client layer).
//
// Regression guard notes:
//   - sqlgraph.UpdateNode (singular) vs UpdateNodes (plural) are internally
//     equivalent for single-row updates — both do UPDATE + separate SELECT
//     (see dialect/sql/sqlgraph/graph.go:1105-1182). Neither uses
//     RETURNING. Switching between them gains nothing measurable.
//   - Real RETURNING speedup (~50% on Postgres per raw-SQL microbenchmark)
//     would require bypassing sqlgraph entirely in codegen — much bigger
//     project than swapping sqlgraph entry points.
func BenchmarkUpdateOne_SQLite(b *testing.B) {
	client := openBenchClient(b)
	defer client.Close()
	ctx := context.Background()

	u, err := client.User.Create().
		SetName("bench").
		SetEmail("b@b.com").
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
		if _, err := client.User.UpdateOneID(u.ID).
			SetName(fmt.Sprintf("bench-%d", i)).
			Save(ctx); err != nil {
			b.Fatalf("update: %v", err)
		}
	}
}

// BenchmarkUpdateOne_Postgres measures UpdateOne latency on real Postgres
// via VELOX_TEST_POSTGRES through the full velox client path.
//
// Measured 2026-04-15 (Postgres 18 localhost docker, M3 Max):
// ~705-755µs/op (both sqlgraph.UpdateNodes and sqlgraph.UpdateNode —
// they're internally equivalent, both do UPDATE + separate SELECT).
//
// Raw-SQL comparison (for reference, not reachable from velox code):
//   - UPDATE + SELECT via db.Exec+QueryRow: ~587µs/op
//   - UPDATE ... RETURNING * via db.QueryRow: ~349µs/op
//
// The ~50% RETURNING win is only reachable by bypassing sqlgraph
// entirely. sqlgraph never emits RETURNING for updates — changing
// this requires either an upstream sqlgraph PR or bypassing sqlgraph
// in velox codegen (both much larger projects than swapping between
// sqlgraph entry points, which gained nothing when attempted).
//
// See memory file project_updateone_returning_not_worth.md for the
// full history of measurements and rejected refactors.
func BenchmarkUpdateOne_Postgres(b *testing.B) {
	client, cleanup := openPostgresOrSkip(b)
	defer cleanup()
	ctx := context.Background()

	u, err := client.User.Create().
		SetName("bench").
		SetEmail("b@b.com").
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
		if _, err := client.User.UpdateOneID(u.ID).
			SetName(fmt.Sprintf("bench-%d", i)).
			Save(ctx); err != nil {
			b.Fatalf("update: %v", err)
		}
	}
}
