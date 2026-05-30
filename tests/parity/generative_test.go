package parity_test

import (
	"math/rand"
	"testing"

	"velox.test/parity/gen"
	"velox.test/parity/op"
	"velox.test/parity/runner"
)

// runGenerative drives n seeded random programs through the three-way harness on
// the given backend. Any VeloxBug (velox != reference, ent == reference) or
// ReferenceSuspect (both diverge — oracle/executor gap) FAILS with the exact
// program for repro. EntDivergent is tolerated (it documents an Ent-side defect,
// not a velox bug). Deterministic via the fixed seed, so a failure is replayable.
func runGenerative(t *testing.T, backend runner.Backend, n int, seed int64) {
	t.Helper()
	r := rand.New(rand.NewSource(seed))
	for i := 0; i < n; i++ {
		buf := make([]byte, 16+r.Intn(96))
		r.Read(buf)
		prog := gen.Build(buf)
		if len(prog) == 0 {
			continue
		}
		rep := runner.RunParity(t, backend, prog)
		if vb, rs := rep.CountVeloxBugs(), rep.CountReferenceSuspect(); vb > 0 || rs > 0 {
			t.Fatalf("generated program #%d diverged on %s (veloxBugs=%d referenceSuspect=%d):\n%s\n--- program ---\n%s",
				i, backend, vb, rs, rep, op.Format(prog))
		}
	}
}

// TestGenerative_NoDivergence is the fast SQLite leg that runs on every build.
func TestGenerative_NoDivergence(t *testing.T) {
	runGenerative(t, runner.SQLite, 400, 0x5eed)
}

// TestGenerative_NoDivergence_Dialects runs the same generator against real
// Postgres and MySQL when configured (VELOX_TEST_POSTGRES / VELOX_TEST_MYSQL),
// skipping cleanly otherwise. Real DBs are slower, so a smaller program budget
// is used than the SQLite leg. This is the path that catches dialect-specific
// velox bugs the SQLite-only fuzzer cannot — the JSON-null-column append bug,
// for instance, was correct on SQLite but wrong on Postgres/MySQL.
func TestGenerative_NoDivergence_Dialects(t *testing.T) {
	for _, backend := range []runner.Backend{runner.Postgres, runner.MySQL} {
		if !runner.HasBackend(backend) {
			t.Run(backend.String(), func(t *testing.T) { t.Skipf("%s not configured", backend) })
			continue
		}
		t.Run(backend.String(), func(t *testing.T) {
			runGenerative(t, backend, 80, 0x5eed)
		})
	}
}
