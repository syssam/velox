package runner_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"velox.test/parity/runner"
)

// RunParity runs all three executors on fresh sqlite clients and returns a
// per-op verdict report; with current (correct) velox every op is Pass.
func TestDriver_AllPassOnSQLite(t *testing.T) {
	rep := runner.RunParity(t, runner.SQLite, paginateProg())
	assert.True(t, rep.AllPass(), "expected all-pass, got:\n%s", rep)
	assert.Zero(t, rep.CountVeloxBugs())
}
