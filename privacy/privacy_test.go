package privacy_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/privacy"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockMutation implements velox.Mutation for testing.
type mockMutation struct {
	op       velox.Op
	typ      string
	field    string
	value    any
	hasField bool // If false, Field() always returns false for presence
}

func (m *mockMutation) Op() velox.Op     { return m.op }
func (m *mockMutation) Type() string     { return m.typ }
func (m *mockMutation) Fields() []string { return []string{m.field} }
func (m *mockMutation) Field(name string) (velox.Value, bool) {
	// If hasField is explicitly set to false, return false for presence
	// Otherwise use field name matching for backwards compatibility
	if m.field == name {
		return m.value, m.hasField || m.value != nil
	}
	return nil, false
}
func (m *mockMutation) SetField(name string, value velox.Value) error {
	m.field = name
	m.value = value
	return nil
}
func (m *mockMutation) AddedFields() []string                   { return nil }
func (m *mockMutation) AddedField(_ string) (velox.Value, bool) { return nil, false }
func (m *mockMutation) AddField(_ string, _ velox.Value) error  { return nil }
func (m *mockMutation) ClearedFields() []string                 { return nil }
func (m *mockMutation) FieldCleared(_ string) bool              { return false }
func (m *mockMutation) ClearField(_ string) error               { return nil }
func (m *mockMutation) ResetField(_ string) error               { return nil }
func (m *mockMutation) AddedEdges() []string                    { return nil }
func (m *mockMutation) AddedIDs(_ string) []velox.Value         { return nil }
func (m *mockMutation) RemovedEdges() []string                  { return nil }
func (m *mockMutation) RemovedIDs(_ string) []velox.Value       { return nil }
func (m *mockMutation) ClearedEdges() []string                  { return nil }
func (m *mockMutation) EdgeCleared(_ string) bool               { return false }
func (m *mockMutation) ClearEdge(_ string) error                { return nil }
func (m *mockMutation) ResetEdge(_ string) error                { return nil }
func (m *mockMutation) OldField(_ context.Context, _ string) (velox.Value, error) {
	return nil, nil
}

// mockQuery implements a basic query for testing.
type mockQuery struct{}

// TestDecisionErrors tests the decision error types and formatting.
func TestDecisionErrors(t *testing.T) {
	tests := []struct {
		name      string
		decision  error
		format    string
		args      []any
		wantAllow bool
		wantDeny  bool
		wantSkip  bool
	}{
		{
			name:      "allow_decision",
			decision:  privacy.Allow,
			wantAllow: true,
		},
		{
			name:     "deny_decision",
			decision: privacy.Deny,
			wantDeny: true,
		},
		{
			name:     "skip_decision",
			decision: privacy.Skip,
			wantSkip: true,
		},
		{
			name:      "allowf_formatted",
			decision:  privacy.Allowf("user %s allowed", "admin"),
			wantAllow: true,
		},
		{
			name:     "denyf_formatted",
			decision: privacy.Denyf("user %s denied", "guest"),
			wantDeny: true,
		},
		{
			name:     "skipf_formatted",
			decision: privacy.Skipf("rule %d skipped", 1),
			wantSkip: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.wantAllow, errors.Is(tt.decision, privacy.Allow))
			assert.Equal(t, tt.wantDeny, errors.Is(tt.decision, privacy.Deny))
			assert.Equal(t, tt.wantSkip, errors.Is(tt.decision, privacy.Skip))
		})
	}
}

// TestAlwaysRules tests AlwaysAllowRule and AlwaysDenyRule.
func TestAlwaysRules(t *testing.T) {
	ctx := context.Background()

	t.Run("AlwaysAllowRule", func(t *testing.T) {
		rule := privacy.AlwaysAllowRule()

		// Test query evaluation
		err := rule.EvalQuery(ctx, &mockQuery{})
		assert.True(t, errors.Is(err, privacy.Allow))

		// Test mutation evaluation
		err = rule.EvalMutation(ctx, &mockMutation{})
		assert.True(t, errors.Is(err, privacy.Allow))
	})

	t.Run("AlwaysDenyRule", func(t *testing.T) {
		rule := privacy.AlwaysDenyRule()

		// Test query evaluation
		err := rule.EvalQuery(ctx, &mockQuery{})
		assert.True(t, errors.Is(err, privacy.Deny))

		// Test mutation evaluation
		err = rule.EvalMutation(ctx, &mockMutation{})
		assert.True(t, errors.Is(err, privacy.Deny))
	})
}

// TestContextQueryMutationRule tests context-based rules.
func TestContextQueryMutationRule(t *testing.T) {
	tests := []struct {
		name       string
		evalFunc   func(context.Context) error
		wantResult error
	}{
		{
			name:       "returns_allow",
			evalFunc:   func(ctx context.Context) error { return privacy.Allow },
			wantResult: privacy.Allow,
		},
		{
			name:       "returns_deny",
			evalFunc:   func(ctx context.Context) error { return privacy.Deny },
			wantResult: privacy.Deny,
		},
		{
			name:       "returns_skip",
			evalFunc:   func(ctx context.Context) error { return privacy.Skip },
			wantResult: privacy.Skip,
		},
		{
			name:       "returns_nil",
			evalFunc:   func(ctx context.Context) error { return nil },
			wantResult: nil,
		},
		{
			name: "context_value_check",
			evalFunc: func(ctx context.Context) error {
				if v := ctx.Value("user"); v != nil {
					return privacy.Allow
				}
				return privacy.Deny
			},
			wantResult: privacy.Deny,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := privacy.ContextQueryMutationRule(tt.evalFunc)
			ctx := context.Background()

			queryErr := rule.EvalQuery(ctx, &mockQuery{})
			mutationErr := rule.EvalMutation(ctx, &mockMutation{})

			if tt.wantResult == nil {
				assert.NoError(t, queryErr)
				assert.NoError(t, mutationErr)
			} else {
				assert.True(t, errors.Is(queryErr, tt.wantResult))
				assert.True(t, errors.Is(mutationErr, tt.wantResult))
			}
		})
	}
}

// TestOnMutationOperation tests operation-specific mutation rules.
func TestOnMutationOperation(t *testing.T) {
	tests := []struct {
		name         string
		ruleOp       velox.Op
		mutationOp   velox.Op
		ruleDecision error
		wantResult   error
	}{
		{
			name:         "matching_create_op",
			ruleOp:       velox.OpCreate,
			mutationOp:   velox.OpCreate,
			ruleDecision: privacy.Deny,
			wantResult:   privacy.Deny,
		},
		{
			name:         "non_matching_op_skips",
			ruleOp:       velox.OpCreate,
			mutationOp:   velox.OpUpdate,
			ruleDecision: privacy.Deny,
			wantResult:   privacy.Skip,
		},
		{
			name:         "matching_update_op",
			ruleOp:       velox.OpUpdate,
			mutationOp:   velox.OpUpdate,
			ruleDecision: privacy.Allow,
			wantResult:   privacy.Allow,
		},
		{
			name:         "matching_delete_op",
			ruleOp:       velox.OpDelete,
			mutationOp:   velox.OpDelete,
			ruleDecision: privacy.Deny,
			wantResult:   privacy.Deny,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baseRule := privacy.MutationRuleFunc(func(ctx context.Context, m velox.Mutation) error {
				return tt.ruleDecision
			})
			rule := privacy.OnMutationOperation(baseRule, tt.ruleOp)

			mutation := &mockMutation{op: tt.mutationOp}
			err := rule.EvalMutation(context.Background(), mutation)

			assert.True(t, errors.Is(err, tt.wantResult))
		})
	}
}

// TestDenyMutationOperationRule tests operation-specific deny rules.
func TestDenyMutationOperationRule(t *testing.T) {
	tests := []struct {
		name       string
		denyOp     velox.Op
		mutationOp velox.Op
		wantDeny   bool
	}{
		{
			name:       "deny_create",
			denyOp:     velox.OpCreate,
			mutationOp: velox.OpCreate,
			wantDeny:   true,
		},
		{
			name:       "allow_update_when_denying_create",
			denyOp:     velox.OpCreate,
			mutationOp: velox.OpUpdate,
			wantDeny:   false,
		},
		{
			name:       "deny_delete",
			denyOp:     velox.OpDelete,
			mutationOp: velox.OpDelete,
			wantDeny:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := privacy.DenyMutationOperationRule(tt.denyOp)
			mutation := &mockMutation{op: tt.mutationOp}
			err := rule.EvalMutation(context.Background(), mutation)

			if tt.wantDeny {
				assert.True(t, errors.Is(err, privacy.Deny))
			} else {
				assert.True(t, errors.Is(err, privacy.Skip))
			}
		})
	}
}

// TestDecisionContext tests context-based decision passing.
func TestDecisionContext(t *testing.T) {
	tests := []struct {
		name         string
		decision     error
		expectStored bool
		expectValue  error
	}{
		{
			name:         "deny_stored_in_context",
			decision:     privacy.Deny,
			expectStored: true,
			expectValue:  privacy.Deny,
		},
		{
			name:         "allow_stored_returns_nil",
			decision:     privacy.Allow,
			expectStored: true,
			expectValue:  nil, // Allow converts to nil
		},
		{
			name:         "skip_not_stored",
			decision:     privacy.Skip,
			expectStored: false,
		},
		{
			name:         "nil_not_stored",
			decision:     nil,
			expectStored: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := privacy.DecisionContext(context.Background(), tt.decision)
			decision, ok := privacy.DecisionFromContext(ctx)

			assert.Equal(t, tt.expectStored, ok)
			if tt.expectStored {
				if tt.expectValue == nil {
					assert.NoError(t, decision)
				} else {
					assert.True(t, errors.Is(decision, tt.expectValue))
				}
			}
		})
	}
}

// TestQueryPolicy tests query policy evaluation.
func TestQueryPolicy(t *testing.T) {
	tests := []struct {
		name       string
		rules      []func(context.Context, velox.Query) error
		wantResult error
	}{
		{
			name:       "empty_policy_allows",
			rules:      nil,
			wantResult: nil,
		},
		{
			name: "first_allow_stops",
			rules: []func(context.Context, velox.Query) error{
				func(ctx context.Context, q velox.Query) error { return privacy.Allow },
				func(ctx context.Context, q velox.Query) error { panic("should not be called") },
			},
			wantResult: privacy.Allow,
		},
		{
			name: "first_deny_stops",
			rules: []func(context.Context, velox.Query) error{
				func(ctx context.Context, q velox.Query) error { return privacy.Deny },
				func(ctx context.Context, q velox.Query) error { panic("should not be called") },
			},
			wantResult: privacy.Deny,
		},
		{
			name: "skip_continues_to_next",
			rules: []func(context.Context, velox.Query) error{
				func(ctx context.Context, q velox.Query) error { return privacy.Skip },
				func(ctx context.Context, q velox.Query) error { return privacy.Allow },
			},
			wantResult: privacy.Allow,
		},
		{
			name: "nil_continues_to_next",
			rules: []func(context.Context, velox.Query) error{
				func(ctx context.Context, q velox.Query) error { return nil },
				func(ctx context.Context, q velox.Query) error { return privacy.Deny },
			},
			wantResult: privacy.Deny,
		},
		{
			name: "all_skip_allows",
			rules: []func(context.Context, velox.Query) error{
				func(ctx context.Context, q velox.Query) error { return privacy.Skip },
				func(ctx context.Context, q velox.Query) error { return privacy.Skip },
			},
			wantResult: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var policy privacy.QueryPolicy
			for _, r := range tt.rules {
				policy = append(policy, queryRuleFunc(r))
			}

			err := policy.EvalQuery(context.Background(), &mockQuery{})

			if tt.wantResult == nil {
				assert.NoError(t, err)
			} else {
				assert.True(t, errors.Is(err, tt.wantResult))
			}
		})
	}
}

// TestMutationPolicy tests mutation policy evaluation.
func TestMutationPolicy(t *testing.T) {
	tests := []struct {
		name       string
		rules      []func(context.Context, velox.Mutation) error
		wantResult error
	}{
		{
			name:       "empty_policy_allows",
			rules:      nil,
			wantResult: nil,
		},
		{
			name: "deny_stops_evaluation",
			rules: []func(context.Context, velox.Mutation) error{
				func(ctx context.Context, m velox.Mutation) error { return privacy.Deny },
				func(ctx context.Context, m velox.Mutation) error { panic("should not be called") },
			},
			wantResult: privacy.Deny,
		},
		{
			name: "allow_stops_evaluation",
			rules: []func(context.Context, velox.Mutation) error{
				func(ctx context.Context, m velox.Mutation) error { return privacy.Allow },
				func(ctx context.Context, m velox.Mutation) error { panic("should not be called") },
			},
			wantResult: privacy.Allow,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var policy privacy.MutationPolicy
			for _, r := range tt.rules {
				policy = append(policy, privacy.MutationRuleFunc(r))
			}

			err := policy.EvalMutation(context.Background(), &mockMutation{})

			if tt.wantResult == nil {
				assert.NoError(t, err)
			} else {
				assert.True(t, errors.Is(err, tt.wantResult))
			}
		})
	}
}

// TestPolicy tests the combined Policy type.
func TestPolicy(t *testing.T) {
	t.Run("forwards_to_query_policy", func(t *testing.T) {
		policy := privacy.Policy{
			Query: privacy.QueryPolicy{
				queryRuleFunc(func(ctx context.Context, q velox.Query) error {
					return privacy.Allow
				}),
			},
		}

		err := policy.EvalQuery(context.Background(), &mockQuery{})
		assert.True(t, errors.Is(err, privacy.Allow))
	})

	t.Run("forwards_to_mutation_policy", func(t *testing.T) {
		policy := privacy.Policy{
			Mutation: privacy.MutationPolicy{
				privacy.MutationRuleFunc(func(ctx context.Context, m velox.Mutation) error {
					return privacy.Deny
				}),
			},
		}

		err := policy.EvalMutation(context.Background(), &mockMutation{})
		assert.True(t, errors.Is(err, privacy.Deny))
	})
}

// TestPolicies tests multiple policy combination.
func TestPolicies(t *testing.T) {
	t.Run("allow_from_one_policy_stops_evaluation", func(t *testing.T) {
		var callCount int
		policies := privacy.Policies{
			&countingPolicy{count: &callCount, queryResult: privacy.Allow},
			&countingPolicy{count: &callCount, queryResult: privacy.Deny},
		}

		err := policies.EvalQuery(context.Background(), &mockQuery{})
		assert.NoError(t, err) // Allow converts to nil
		assert.Equal(t, 1, callCount, "should stop after first allow")
	})

	t.Run("deny_from_one_policy_stops_evaluation", func(t *testing.T) {
		var callCount int
		policies := privacy.Policies{
			&countingPolicy{count: &callCount, mutationResult: privacy.Deny},
			&countingPolicy{count: &callCount, mutationResult: privacy.Allow},
		}

		err := policies.EvalMutation(context.Background(), &mockMutation{})
		assert.True(t, errors.Is(err, privacy.Deny))
		assert.Equal(t, 1, callCount, "should stop after first deny")
	})

	t.Run("skip_continues_to_next_policy", func(t *testing.T) {
		var callCount int
		policies := privacy.Policies{
			&countingPolicy{count: &callCount, queryResult: privacy.Skip},
			&countingPolicy{count: &callCount, queryResult: privacy.Allow},
		}

		err := policies.EvalQuery(context.Background(), &mockQuery{})
		assert.NoError(t, err)
		assert.Equal(t, 2, callCount, "should call both policies")
	})

	t.Run("context_decision_overrides_policies", func(t *testing.T) {
		ctx := privacy.DecisionContext(context.Background(), privacy.Deny)
		policies := privacy.Policies{
			&countingPolicy{queryResult: privacy.Allow},
		}

		err := policies.EvalQuery(ctx, &mockQuery{})
		assert.True(t, errors.Is(err, privacy.Deny))
	})
}

// TestNewPolicies tests the NewPolicies constructor.
func TestNewPolicies(t *testing.T) {
	t.Run("creates_from_providers", func(t *testing.T) {
		type ctxKey string
		key := ctxKey("counter")

		provider := &policyProvider{
			policy: &countingPolicy{
				count:       new(int),
				queryResult: nil,
			},
		}

		policy := privacy.NewPolicies(provider, provider, provider)

		ctx := context.WithValue(context.Background(), key, new(int))
		err := policy.EvalQuery(ctx, &mockQuery{})
		assert.NoError(t, err)
	})

	t.Run("skips_nil_policies", func(t *testing.T) {
		provider := &policyProvider{policy: nil}
		policy := privacy.NewPolicies(provider)

		err := policy.EvalQuery(context.Background(), &mockQuery{})
		assert.NoError(t, err)
	})

	t.Run("evaluates_all_non_nil_policies", func(t *testing.T) {
		var callCount int
		counter := &callCount

		providers := []privacy.PolicyProvider{
			&policyProvider{policy: &countingPolicy{count: counter, queryResult: nil}},
			&policyProvider{policy: nil}, // Should be skipped
			&policyProvider{policy: &countingPolicy{count: counter, queryResult: nil}},
		}

		policy := privacy.NewPolicies(providers...)
		err := policy.EvalQuery(context.Background(), &mockQuery{})

		assert.NoError(t, err)
		assert.Equal(t, 2, callCount, "should call both non-nil policies")
	})
}

// Helper types for testing.

type queryRuleFunc func(context.Context, velox.Query) error

func (f queryRuleFunc) EvalQuery(ctx context.Context, q velox.Query) error {
	return f(ctx, q)
}

type countingPolicy struct {
	count          *int
	queryResult    error
	mutationResult error
}

func (p *countingPolicy) EvalQuery(ctx context.Context, q velox.Query) error {
	*p.count++
	return p.queryResult
}

func (p *countingPolicy) EvalMutation(ctx context.Context, m velox.Mutation) error {
	*p.count++
	return p.mutationResult
}

type policyProvider struct {
	policy velox.Policy
}

func (p *policyProvider) Policy() velox.Policy {
	return p.policy
}

// BenchmarkPrivacy benchmarks privacy rule evaluation.
func BenchmarkPrivacy(b *testing.B) {
	ctx := context.Background()
	query := &mockQuery{}
	mutation := &mockMutation{op: velox.OpCreate}

	b.Run("AlwaysAllowRule", func(b *testing.B) {
		rule := privacy.AlwaysAllowRule()
		for i := 0; i < b.N; i++ {
			_ = rule.EvalQuery(ctx, query)
		}
	})

	b.Run("AlwaysDenyRule", func(b *testing.B) {
		rule := privacy.AlwaysDenyRule()
		for i := 0; i < b.N; i++ {
			_ = rule.EvalMutation(ctx, mutation)
		}
	})

	b.Run("ContextQueryMutationRule", func(b *testing.B) {
		rule := privacy.ContextQueryMutationRule(func(ctx context.Context) error {
			return privacy.Allow
		})
		for i := 0; i < b.N; i++ {
			_ = rule.EvalQuery(ctx, query)
		}
	})

	b.Run("PolicyChain_5Rules", func(b *testing.B) {
		policy := privacy.QueryPolicy{
			queryRuleFunc(func(ctx context.Context, q velox.Query) error { return privacy.Skip }),
			queryRuleFunc(func(ctx context.Context, q velox.Query) error { return privacy.Skip }),
			queryRuleFunc(func(ctx context.Context, q velox.Query) error { return privacy.Skip }),
			queryRuleFunc(func(ctx context.Context, q velox.Query) error { return privacy.Skip }),
			queryRuleFunc(func(ctx context.Context, q velox.Query) error { return privacy.Allow }),
		}
		for i := 0; i < b.N; i++ {
			_ = policy.EvalQuery(ctx, query)
		}
	})

	b.Run("DecisionContext", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			dCtx := privacy.DecisionContext(ctx, privacy.Allow)
			_, _ = privacy.DecisionFromContext(dCtx)
		}
	})
}

// TestMutationRuleFunc tests the MutationRuleFunc adapter.
func TestMutationRuleFunc(t *testing.T) {
	called := false
	rule := privacy.MutationRuleFunc(func(ctx context.Context, m velox.Mutation) error {
		called = true
		return privacy.Allow
	})

	err := rule.EvalMutation(context.Background(), &mockMutation{})
	assert.True(t, called)
	assert.True(t, errors.Is(err, privacy.Allow))
}

// TestErrorMessages tests that error messages are properly formatted.
func TestErrorMessages(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantContain string
	}{
		{
			name:        "allowf_message",
			err:         privacy.Allowf("user %s granted access", "admin"),
			wantContain: "user admin granted access",
		},
		{
			name:        "denyf_message",
			err:         privacy.Denyf("access denied for role %s", "guest"),
			wantContain: "access denied for role guest",
		},
		{
			name:        "skipf_message",
			err:         privacy.Skipf("skipping rule %d", 42),
			wantContain: "skipping rule 42",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Contains(t, tt.err.Error(), tt.wantContain)
		})
	}
}

// TestPoliciesContextPropagation tests that context is properly propagated.
func TestPoliciesContextPropagation(t *testing.T) {
	type ctxKey string
	key := ctxKey("testValue")

	policy := privacy.QueryPolicy{
		queryRuleFunc(func(ctx context.Context, q velox.Query) error {
			if v := ctx.Value(key); v != "expected" {
				return fmt.Errorf("unexpected context value: %v", v)
			}
			return privacy.Allow
		}),
	}

	ctx := context.WithValue(context.Background(), key, "expected")
	err := policy.EvalQuery(ctx, &mockQuery{})
	// Allow is returned as the decision - it's wrapped in the error
	assert.True(t, errors.Is(err, privacy.Allow))
}
