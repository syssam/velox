package gen_test

import (
	"math/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"velox.test/parity/gen"
	"velox.test/parity/op"
)

func TestBuild_EmptyInputIsEmptyOrTrivial(t *testing.T) {
	prog := gen.Build(nil)
	assert.LessOrEqual(t, len(prog), 1, "no bytes => at most a trivial program")
}

func TestBuild_IsReferentiallyValid(t *testing.T) {
	// Across many random byte streams, every emitted program must be
	// referentially valid: every ref points to an existing earlier handle of
	// the correct kind, and the first Create of a dependent kind is preceded
	// by its dependency.
	r := rand.New(rand.NewSource(1))
	for iter := 0; iter < 2000; iter++ {
		buf := make([]byte, r.Intn(80))
		r.Read(buf)
		prog := gen.Build(buf)
		require.NoError(t, gen.Validate(prog),
			"iter %d produced an invalid program:\n%s", iter, op.Format(prog))
	}
}

func TestBuild_Deterministic(t *testing.T) {
	buf := []byte{3, 7, 1, 9, 4, 2, 8, 5, 6, 0, 11, 13}
	a := op.Format(gen.Build(buf))
	b := op.Format(gen.Build(buf))
	assert.Equal(t, a, b, "Build must be a pure function of its input bytes")
}
