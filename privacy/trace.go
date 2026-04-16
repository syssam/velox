package privacy

import (
	"context"
	"errors"
)

type traceCtxKey struct{}

// TraceEntry records one privacy rule evaluation.
type TraceEntry struct {
	Rule     string // Rule type name (e.g., "FilterFunc", "ContextQueryMutationRule")
	Decision string // "allow", "deny", "skip", "filter"
}

// WithTrace returns a context that collects privacy rule trace entries.
func WithTrace(ctx context.Context) context.Context {
	return context.WithValue(ctx, traceCtxKey{}, &[]TraceEntry{})
}

// RecordTrace appends a trace entry if tracing is enabled on the context.
func RecordTrace(ctx context.Context, rule, decision string) {
	if t, ok := ctx.Value(traceCtxKey{}).(*[]TraceEntry); ok {
		*t = append(*t, TraceEntry{Rule: rule, Decision: decision})
	}
}

// TraceFrom returns the collected trace entries, or nil if tracing is not enabled.
func TraceFrom(ctx context.Context) []TraceEntry {
	if t, ok := ctx.Value(traceCtxKey{}).(*[]TraceEntry); ok {
		return *t
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
