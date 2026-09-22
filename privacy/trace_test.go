package privacy

import (
	"context"
	"errors"
	"sync"
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

// TestRecordTrace_Concurrent pins that one traced context can be shared by
// concurrently evaluated rules — gqlgen resolves fields in parallel, all
// under the request's context. Run with -race.
func TestRecordTrace_Concurrent(t *testing.T) {
	ctx := WithTrace(context.Background())
	const workers, perWorker = 8, 100
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for range perWorker {
				RecordTrace(ctx, "FilterFunc", "skip")
				_ = TraceFrom(ctx)
			}
		})
	}
	wg.Wait()
	assert.Len(t, TraceFrom(ctx), workers*perWorker)
}

// TestTraceFrom_ReturnsSnapshot pins that the returned slice is a copy: a
// caller holding it must not observe (or race with) later entries.
func TestTraceFrom_ReturnsSnapshot(t *testing.T) {
	ctx := WithTrace(context.Background())
	RecordTrace(ctx, "FilterFunc", "skip")
	snap := TraceFrom(ctx)
	RecordTrace(ctx, "FilterFunc", "allow")
	require.Len(t, snap, 1)
	assert.Len(t, TraceFrom(ctx), 2)
}

func TestRecordTrace_NoopWithoutTrace(t *testing.T) {
	// Recording into an untraced context is dropped: nothing is stored on
	// that context, and nothing leaks into a trace started afterwards.
	ctx := context.Background()
	RecordTrace(ctx, "FilterFunc", "skip")
	assert.Nil(t, TraceFrom(ctx))

	traced := WithTrace(ctx)
	RecordTrace(ctx, "FilterFunc", "skip")
	assert.Empty(t, TraceFrom(traced))
}
