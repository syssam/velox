package compare_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"velox.test/parity/compare"
)

func TestVerdict_Table(t *testing.T) {
	// (veloxMatchesRef, entMatchesRef) -> Verdict
	assert.Equal(t, compare.Pass, compare.Classify(true, true))
	assert.Equal(t, compare.VeloxBug, compare.Classify(false, true))
	assert.Equal(t, compare.ReferenceSuspect, compare.Classify(false, false))
	assert.Equal(t, compare.EntDivergent, compare.Classify(true, false))
}
