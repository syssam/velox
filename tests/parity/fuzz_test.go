package parity_test

import (
	"testing"

	"velox.test/parity/gen"
	"velox.test/parity/op"
	"velox.test/parity/runner"
)

// FuzzParity runs go's coverage-guided fuzzer over the program builder: each
// input []byte becomes a referentially-valid program, run through the three-way
// harness on SQLite. A VeloxBug or ReferenceSuspect fails the input; go's
// fuzzer then minimizes the failing []byte automatically. Run with:
//
//	go test -run x -fuzz FuzzParity -fuzztime 60s
func FuzzParity(f *testing.F) {
	// Seed corpus: a few byte streams that build non-trivial programs.
	f.Add([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16})
	f.Add([]byte{9, 9, 2, 2, 5, 5, 1, 1, 7, 7, 3, 3, 8, 8, 4, 4, 6, 6})
	f.Add(make([]byte, 64))
	f.Fuzz(func(t *testing.T, data []byte) {
		prog := gen.Build(data)
		if len(prog) == 0 {
			return
		}
		rep := runner.RunParity(t, runner.SQLite, prog)
		if vb, rs := rep.CountVeloxBugs(), rep.CountReferenceSuspect(); vb > 0 || rs > 0 {
			t.Fatalf("fuzz found divergence (veloxBugs=%d referenceSuspect=%d):\n%s\n--- program ---\n%s",
				vb, rs, rep, op.Format(prog))
		}
	})
}
