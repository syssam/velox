package parity_test

import (
	"math/rand"
	"testing"

	"velox.test/parity/gen"
	"velox.test/parity/op"
	"velox.test/parity/runner"
)

// TestGenerative_NoDivergence runs N seeded random programs through the
// three-way harness on SQLite. Any VeloxBug (velox != reference, ent ==
// reference) or ReferenceSuspect (both diverge — oracle/executor gap) FAILS
// with the exact program for repro. EntDivergent is tolerated (it documents an
// Ent-side defect, not a velox bug). Deterministic via the fixed seed.
func TestGenerative_NoDivergence(t *testing.T) {
	const n = 400
	r := rand.New(rand.NewSource(0x5eed))
	for i := 0; i < n; i++ {
		buf := make([]byte, 16+r.Intn(96))
		r.Read(buf)
		prog := gen.Build(buf)
		if len(prog) == 0 {
			continue
		}
		rep := runner.RunParity(t, runner.SQLite, prog)
		if vb, rs := rep.CountVeloxBugs(), rep.CountReferenceSuspect(); vb > 0 || rs > 0 {
			t.Fatalf("generated program #%d diverged (veloxBugs=%d referenceSuspect=%d):\n%s\n--- program ---\n%s",
				i, vb, rs, rep, op.Format(prog))
		}
	}
}
