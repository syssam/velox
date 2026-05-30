package runner_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"velox.test/parity/runner"
)

func TestPostgres_ClientsOrSkip(t *testing.T) {
	vc, ec, ok := runner.NewPostgresClients(t)
	if !ok {
		t.Skip("VELOX_TEST_POSTGRES not set; skipping")
	}
	require.NotNil(t, vc)
	require.NotNil(t, ec)
}

func TestMySQL_ClientsOrSkip(t *testing.T) {
	vc, ec, ok := runner.NewMySQLClients(t)
	if !ok {
		t.Skip("VELOX_TEST_MYSQL not set; skipping")
	}
	require.NotNil(t, vc)
	require.NotNil(t, ec)
}
