package runner_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"velox.test/parity/runner"
)

func TestSQLite_FreshClientsMigrate(t *testing.T) {
	vc := runner.NewVeloxSQLite(t) // fresh, migrated, t.Cleanup-closed
	require.NotNil(t, vc)
	ec := runner.NewEntSQLite(t)
	require.NotNil(t, ec)
}
