package runner_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"velox.test/parity/compare"
	"velox.test/parity/op"
	"velox.test/parity/runner"
)

func prog() op.Program {
	return op.Program{
		op.CreateAuthor{Name: "A", Role: "user"},
		op.CreatePost{Title: "P", Status: "draft", ViewCount: 5, AuthorRef: 0},
		op.QueryPostsByStatus{Status: "draft"},
	}
}

// runReference vs itself => no diff (the framework agrees with ground truth).
func TestSelfTest_ReferenceAgreesWithItself(t *testing.T) {
	a := runner.Reference(t, prog())
	b := runner.Reference(t, prog())
	assert.Empty(t, compare.Diff(a, b))
}

// Inject a divergence into a copy of the reference results and prove the
// comparator REPORTS it. A comparator that cannot fail is security theater.
func TestSelfTest_InjectedDivergenceIsCaught(t *testing.T) {
	base := runner.Reference(t, prog())
	perturbed := runner.CloneResults(base)
	// Corrupt the queried post's view_count.
	require.NotEmpty(t, perturbed[2].Rows)
	perturbed[2].Rows[0]["view_count"] = 999
	d := compare.Diff(base, perturbed)
	require.Len(t, d, 1)
	assert.Equal(t, "view_count", d[0].Field)
	// And the verdict logic flags velox (perturbed) as the bug when ent matches ref.
	assert.Equal(t, compare.VeloxBug, compare.Classify(false, true))
}
