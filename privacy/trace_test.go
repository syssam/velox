package privacy

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithTrace_CollectsRuleResults(t *testing.T) {
	ctx := WithTrace(context.Background())
	RecordTrace(ctx, "FilterFunc", "skip")
	RecordTrace(ctx, "ContextQueryMutationRule", "allow")
	entries := TraceFrom(ctx)
	require.Len(t, entries, 2)
	assert.Equal(t, "FilterFunc", entries[0].Rule)
	assert.Equal(t, "skip", entries[0].Decision)
	assert.Equal(t, "ContextQueryMutationRule", entries[1].Rule)
	assert.Equal(t, "allow", entries[1].Decision)
}

func TestTraceFrom_NilContext(t *testing.T) {
	entries := TraceFrom(context.Background())
	assert.Nil(t, entries)
}

func TestDecisionString(t *testing.T) {
	assert.Equal(t, "skip", decisionString(Skip))
	assert.Equal(t, "allow", decisionString(Allow))
	assert.Equal(t, "deny", decisionString(Deny))
	assert.Equal(t, "filter", decisionString(nil))
	assert.Equal(t, "deny", decisionString(errors.New("custom error")))
}

func TestDecisionString_WrappedSentinels(t *testing.T) {
	assert.Equal(t, "skip", decisionString(Skipf("reason")))
	assert.Equal(t, "allow", decisionString(Allowf("reason")))
	assert.Equal(t, "deny", decisionString(Denyf("reason")))
}

func TestRecordTrace_NoopWithoutTrace(t *testing.T) {
	// Should not panic when tracing is not enabled.
	RecordTrace(context.Background(), "FilterFunc", "skip")
}
