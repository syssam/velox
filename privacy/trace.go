package privacy

import (
	"context"
	"errors"
	"slices"
	"sync"
)

type traceCtxKey struct{}

// TraceEntry records one privacy rule evaluation.
type TraceEntry struct {
	Rule     string // Rule type name (e.g., "FilterFunc", "ContextQueryMutationRule")
	Decision string // "allow", "deny", "skip", "filter"
}

// traceCollector accumulates trace entries. One traced context is shared by
// every rule evaluated under it, and gqlgen resolves fields concurrently, so
// access is serialized.
type traceCollector struct {
	mu      sync.Mutex
	entries []TraceEntry
}

// WithTrace returns a context that collects privacy rule trace entries.
// The context may be shared by concurrent evaluations.
func WithTrace(ctx context.Context) context.Context {
	return context.WithValue(ctx, traceCtxKey{}, &traceCollector{entries: []TraceEntry{}})
}

// RecordTrace appends a trace entry if tracing is enabled on the context.
// It is safe for concurrent use.
func RecordTrace(ctx context.Context, rule, decision string) {
	if t, ok := ctx.Value(traceCtxKey{}).(*traceCollector); ok {
		t.mu.Lock()
		t.entries = append(t.entries, TraceEntry{Rule: rule, Decision: decision})
		t.mu.Unlock()
	}
}

// TraceFrom returns a snapshot of the collected trace entries, or nil if
// tracing is not enabled. Entries recorded after the call are not reflected
// in the returned slice.
func TraceFrom(ctx context.Context) []TraceEntry {
	if t, ok := ctx.Value(traceCtxKey{}).(*traceCollector); ok {
		t.mu.Lock()
		defer t.mu.Unlock()
		return slices.Clone(t.entries)
	}
	return nil
}

// decisionString converts a rule evaluation error to a human-readable decision string.
func decisionString(err error) string {
	switch {
	case err == nil:
		return "filter"
	case errors.Is(err, Skip):
		return "skip"
	case errors.Is(err, Allow):
		return "allow"
	case errors.Is(err, Deny):
		return "deny"
	default:
		return "deny"
	}
}
