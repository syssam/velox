package parity_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"velox.test/parity/op"
	"velox.test/parity/runner"
)

// curatedPrograms2 returns just the programs of the curated suite (dropping the
// per-case assertion metadata) so the coverage matrix can walk their ops.
func curatedPrograms2() []op.Program {
	cases := curatedPrograms()
	progs := make([]op.Program, len(cases))
	for i, tc := range cases {
		progs[i] = tc.prog
	}
	return progs
}

// TestCoverage_AllOpKindsExercised pins that the curated suite exercises every
// op kind at least once — turning "curated covers the known classes" into a
// measured fact, not an assumption. If this fails, ADD a curated case that uses
// the missing op (do NOT weaken the assertion).
func TestCoverage_AllOpKindsExercised(t *testing.T) {
	cov := runner.CoverProgramSet(curatedPrograms2())
	missing := cov.MissingOpKinds()
	assert.Empty(t, missing, "curated suite leaves op kinds unexercised: %v", missing)
}

// TestCoverage_EdgeAndFieldSurfaces documents the non-op surfaces the curated
// suite reaches (JSON label writes, edge relations), so a regression that drops
// one is visible. It is a softer assertion than op-kind coverage — it pins the
// surfaces the suite is KNOWN to reach today.
func TestCoverage_EdgeAndFieldSurfaces(t *testing.T) {
	cov := runner.CoverProgramSet(curatedPrograms2())
	assert.Contains(t, cov.Fields(), "post.labels", "suite must exercise JSON label writes")
	assert.Contains(t, cov.Edges(), "author.posts", "suite must exercise the author→posts O2M edge")
	assert.Contains(t, cov.Edges(), "post.tags", "suite must exercise the post↔tags M2M edge")
}
