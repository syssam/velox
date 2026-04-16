package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	integration "github.com/syssam/velox/tests/integration"
)

// TestContext_ClientRoundTrip verifies NewContext/FromContext round-trips a
// *Client through a context.
func TestContext_ClientRoundTrip(t *testing.T) {
	client := openTestClient(t)

	ctx := integration.NewContext(context.Background(), client)
	got := integration.FromContext(ctx)
	assert.Same(t, client, got, "FromContext must return the same Client NewContext stored")
}

// TestContext_ClientFromEmpty verifies FromContext returns nil when no Client
// is attached.
func TestContext_ClientFromEmpty(t *testing.T) {
	assert.Nil(t, integration.FromContext(context.Background()))
}

// TestContext_TxRoundTrip verifies NewTxContext/TxFromContext round-trips a
// *Tx through a context.
func TestContext_TxRoundTrip(t *testing.T) {
	client := openTestClient(t)
	tx, err := client.Tx(context.Background())
	if err != nil {
		t.Fatalf("starting tx: %v", err)
	}
	defer tx.Rollback()

	ctx := integration.NewTxContext(context.Background(), tx)
	got := integration.TxFromContext(ctx)
	assert.Same(t, tx, got, "TxFromContext must return the same Tx NewTxContext stored")
}

// TestContext_TxFromEmpty verifies TxFromContext returns nil when no Tx is
// attached.
func TestContext_TxFromEmpty(t *testing.T) {
	assert.Nil(t, integration.TxFromContext(context.Background()))
}

// TestTx_ContextAccessor verifies Tx.Context() returns the context passed to
// client.Tx().
func TestTx_ContextAccessor(t *testing.T) {
	client := openTestClient(t)

	type key struct{}
	parent := context.WithValue(context.Background(), key{}, "parent-value")

	tx, err := client.Tx(parent)
	if err != nil {
		t.Fatalf("starting tx: %v", err)
	}
	defer tx.Rollback()

	assert.Equal(t, "parent-value", tx.Context().Value(key{}), "Tx.Context() must preserve the parent context value")
}

// TestClient_DebugMode verifies Client.Debug() returns a usable client that
// writes SQL to the log without breaking functionality.
func TestClient_DebugMode(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	debugClient := client.Debug()
	if debugClient == nil {
		t.Fatal("Debug() must return a non-nil client")
	}

	// A second call should be a no-op (returns same debug client).
	assert.Same(t, debugClient, debugClient.Debug(), "Debug() on an already-debug client is a no-op")

	// Debug client must still execute queries correctly.
	createUser(t, debugClient, "Alice", "alice@test.com")
	count, err := debugClient.User.Query().Count(ctx)
	if err != nil {
		t.Fatalf("debug client query: %v", err)
	}
	assert.Equal(t, 1, count)
}
