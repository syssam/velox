package engine

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
)

// TraceEntry is one privacy rule's decision, which PrivacyTraceContext's
// function is given.
type TraceEntry struct {
	Rule     string // the rule's Go type, e.g. "privacy.QueryRuleFunc"
	Index    int    // its place in the list or combinator that ran it, from 0
	Entity   string // the statement's entity, e.g. "gen.Doc"; "" for a statement velox did not build
	Op       Op     // OpSelect for a read; OpInsert, OpUpdate or OpDelete for a write
	Decision string // "allow", "deny" or "skip", as the list reads Err
	Err      error  // what the rule returned: a Decision, nil or another error
}

// privacyTracing is set once any context has carried a trace function, so
// that a process that never traces reads no context for it.
var privacyTracing atomic.Bool

// privacyTraceKey is the context key of PrivacyTraceContext.
type privacyTraceKey struct{}

// PrivacyTraceContext returns parent carrying fn, which every privacy rule
// run under it then reports its decision to. A nil fn returns parent.
func PrivacyTraceContext(parent context.Context, fn func(TraceEntry)) context.Context {
	if fn == nil {
		return parent
	}
	privacyTracing.Store(true)
	return context.WithValue(parent, privacyTraceKey{}, fn)
}

// EvalQueryRule returns r.EvalQuery(ctx, q), reported to ctx's trace
// function as the i-th rule of its list.
func EvalQueryRule(ctx context.Context, r QueryRule, q PrivacyQuery, i int) error {
	err := r.EvalQuery(ctx, q)
	if privacyTracing.Load() {
		entity := ""
		if pq, ok := q.(privacyQuery); ok {
			entity = pq.f.in.Name
		}
		trace(ctx, r, i, entity, OpSelect, err)
	}
	return err
}

// EvalMutationRule returns r.EvalMutation(ctx, m), reported as
// EvalQueryRule reports.
func EvalMutationRule(ctx context.Context, r MutationRule, m PrivacyMutation, i int) error {
	err := r.EvalMutation(ctx, m)
	if privacyTracing.Load() {
		entity := ""
		if pm, ok := m.(privacyMutation); ok {
			entity = pm.in.Name
		}
		trace(ctx, r, i, entity, m.Op(), err)
	}
	return err
}

// trace reports rule r's result err to ctx's trace function, if any.
func trace(ctx context.Context, r any, i int, entity string, op Op, err error) {
	fn, ok := ctx.Value(privacyTraceKey{}).(func(TraceEntry))
	if !ok {
		return
	}
	d := "deny"
	switch {
	case err == nil || errors.Is(err, PrivacySkip):
		d = "skip"
	case errors.Is(err, PrivacyAllow):
		d = "allow"
	}
	fn(TraceEntry{Rule: fmt.Sprintf("%T", r), Index: i, Entity: entity, Op: op, Decision: d, Err: err})
}
