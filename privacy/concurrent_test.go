package privacy_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/privacy"

	"github.com/stretchr/testify/assert"
)

// TestConcurrentQueryPolicyEvaluation verifies that multiple goroutines can
// safely evaluate the same QueryPolicy simultaneously without data races.
func TestConcurrentQueryPolicyEvaluation(t *testing.T) {
	t.Parallel()

	policy := privacy.QueryPolicy{
		privacy.ContextQueryMutationRule(func(_ context.Context) error {
			return privacy.Skip
		}),
		privacy.ContextQueryMutationRule(func(_ context.Context) error {
			return privacy.Allow
		}),
	}

	const goroutines = 100
	var wg sync.WaitGroup
	errs := make([]error, goroutines)

	for i := range goroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = policy.EvalQuery(context.Background(), &mockQuery{})
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		assert.NoError(t, err, "goroutine %d got unexpected error", i)
	}
}

// TestConcurrentMutationPolicyWithDifferentViewers verifies that MutationPolicy
// evaluates correctly when different goroutines use different viewer contexts.
func TestConcurrentMutationPolicyWithDifferentViewers(t *testing.T) {
	t.Parallel()

	policy := privacy.MutationPolicy{
		privacy.DenyIfNoViewer(),
		privacy.HasRole("admin"),
		privacy.AlwaysDenyRule(),
	}

	const goroutines = 100
	var wg sync.WaitGroup
	results := make([]error, goroutines)

	for i := range goroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			var ctx context.Context
			if idx%2 == 0 {
				// Even goroutines: admin viewer — should be allowed (nil).
				viewer := &privacy.SimpleViewer{
					UserID:    fmt.Sprintf("admin-%d", idx),
					UserRoles: []string{"admin"},
				}
				ctx = privacy.WithViewer(context.Background(), viewer)
			} else {
				// Odd goroutines: no viewer — should be denied.
				ctx = context.Background()
			}
			results[idx] = policy.EvalMutation(ctx, &mockMutation{})
		}(i)
	}
	wg.Wait()

	for i, err := range results {
		if i%2 == 0 {
			assert.NoError(t, err, "goroutine %d (admin) should be allowed", i)
		} else {
			assert.True(t, errors.Is(err, privacy.Deny), "goroutine %d (no viewer) should be denied", i)
		}
	}
}

// TestConcurrentDecisionContextIsolation verifies that DecisionContext values
// are isolated per goroutine and do not bleed across contexts.
func TestConcurrentDecisionContextIsolation(t *testing.T) {
	t.Parallel()

	const goroutines = 50
	var wg sync.WaitGroup

	type result struct {
		decision error
		ok       bool
	}
	results := make([]result, goroutines)

	for i := range goroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			var decision error
			if idx%2 == 0 {
				decision = privacy.Allow
			} else {
				decision = privacy.Deny
			}
			ctx := privacy.DecisionContext(context.Background(), decision)
			d, ok := privacy.DecisionFromContext(ctx)
			results[idx] = result{decision: d, ok: ok}
		}(i)
	}
	wg.Wait()

	for i, r := range results {
		assert.True(t, r.ok, "goroutine %d: decision should be stored", i)
		if i%2 == 0 {
			// Allow is converted to nil by DecisionFromContext.
			assert.NoError(t, r.decision, "goroutine %d: Allow should become nil", i)
		} else {
			assert.True(t, errors.Is(r.decision, privacy.Deny), "goroutine %d: Deny should be preserved", i)
		}
	}
}

// statelessPolicy is a race-safe policy for concurrent tests — it never
// mutates shared state, only returns a fixed decision.
type statelessPolicy struct {
	queryResult    error
	mutationResult error
}

func (p *statelessPolicy) EvalQuery(_ context.Context, _ velox.Query) error {
	return p.queryResult
}

func (p *statelessPolicy) EvalMutation(_ context.Context, _ velox.Mutation) error {
	return p.mutationResult
}

// TestConcurrentPoliciesEvaluation verifies Policies.EvalQuery is safe under
// concurrent access from many goroutines simultaneously.
func TestConcurrentPoliciesEvaluation(t *testing.T) {
	t.Parallel()

	// Use stateless (no shared mutable state) policies to avoid races.
	policies := privacy.Policies{
		&statelessPolicy{queryResult: privacy.Skip},
		&statelessPolicy{queryResult: privacy.Allow},
	}

	const goroutines = 80
	var wg sync.WaitGroup
	errs := make([]error, goroutines)

	for i := range goroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = policies.EvalQuery(context.Background(), &mockQuery{})
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		assert.NoError(t, err, "goroutine %d: Policies should allow", i)
	}
}
