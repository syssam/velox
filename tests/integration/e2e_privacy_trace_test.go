package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/privacy"
	schema "github.com/syssam/velox/testschema"
)

// TestPrivacyTrace_FilterApplied verifies that the trace API captures
// rule evaluation entries when privacy enforcement and tracing are
// both enabled on the context.
func TestPrivacyTrace_FilterApplied(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	createUser(t, client, "Alice", "alice@test.com")
	createUser(t, client, "Bob", "bob@test.com")

	// Enable privacy enforcement + trace.
	ctx = schema.EnforceUserPrivacyContext(ctx)
	ctx = schema.AllowWriteContext(ctx)
	ctx = schema.FilterUserQueryToNameContext(ctx, "Alice")
	ctx = privacy.WithTrace(ctx)

	users, err := client.User.Query().All(ctx)
	require.NoError(t, err)
	assert.Len(t, users, 1)
	assert.Equal(t, "Alice", users[0].Name)

	// Verify trace captured rule evaluations.
	entries := privacy.TraceFrom(ctx)
	require.NotEmpty(t, entries, "trace should have captured rule evaluations")

	// Should have FilterFunc and ContextQueryMutationRule entries.
	var hasFilter, hasRule bool
	for _, e := range entries {
		if e.Rule == "FilterFunc" {
			hasFilter = true
		}
		if e.Rule == "ContextQueryMutationRule" {
			hasRule = true
		}
	}
	assert.True(t, hasFilter, "expected FilterFunc trace entry")
	assert.True(t, hasRule, "expected ContextQueryMutationRule trace entry")
}

// TestPrivacyTrace_NoTraceWithoutWithTrace verifies that the trace
// context is nil when WithTrace has not been called — ensuring zero
// overhead on non-traced requests.
func TestPrivacyTrace_NoTraceWithoutWithTrace(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	createUser(t, client, "Alice", "alice@test.com")

	ctx = schema.EnforceUserPrivacyContext(ctx)
	ctx = schema.AllowWriteContext(ctx)

	_, err := client.User.Query().All(ctx)
	require.NoError(t, err)

	entries := privacy.TraceFrom(ctx)
	assert.Nil(t, entries, "trace should be nil when WithTrace was not called")
}
