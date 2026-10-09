package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	integration "github.com/syssam/velox/tests/integration"
)

// TestHandles_RoundTrip pins the opaque handles: a *Client is a ClientHandle
// and a *Tx is a TxHandle, the Of functions return the same pointer, and a
// nil handle — how a caller says "no transaction" — yields nil rather than a
// panic.
func TestHandles_RoundTrip(t *testing.T) {
	ctx := context.Background()
	client := openTestClient(t)

	var ch integration.ClientHandle = client
	require.Same(t, client, integration.ClientOf(ch))
	require.Nil(t, integration.ClientOf(nil))

	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	var th integration.TxHandle = tx
	require.Same(t, tx, integration.TxOf(th))
	require.Nil(t, integration.TxOf(nil))

	// A typed nil pointer in the handle comes back as that nil pointer.
	var none *integration.Tx
	require.Nil(t, integration.TxOf(none))
}
