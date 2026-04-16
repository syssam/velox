package privacy_test

import (
	"context"
	"errors"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/privacy"

	"github.com/stretchr/testify/assert"
)

// TestComplexRuleChains verifies rule chain short-circuit and fallthrough
// behavior for common real-world policy patterns.
func TestComplexRuleChains(t *testing.T) {
	t.Parallel()

	adminViewer := &privacy.SimpleViewer{UserID: "admin-1", UserRoles: []string{"admin"}}
	regularViewer := &privacy.SimpleViewer{UserID: "user-1", UserRoles: []string{"user"}}

	adminCtx := privacy.WithViewer(context.Background(), adminViewer)
	regularCtx := privacy.WithViewer(context.Background(), regularViewer)
	noViewerCtx := context.Background()

	// Standard three-rule gate: DenyIfNoViewer → HasRole("admin") → AlwaysDeny.
	adminGatePolicy := privacy.MutationPolicy{
		privacy.DenyIfNoViewer(),
		privacy.HasRole("admin"),
		privacy.AlwaysDenyRule(),
	}

	tests := []struct {
		name       string
		policy     privacy.MutationPolicy
		ctx        context.Context
		mutation   *mockMutation
		wantResult error // nil means "no error" (allowed)
	}{
		{
			name:       "admin_bypasses_all_rules",
			policy:     adminGatePolicy,
			ctx:        adminCtx,
			mutation:   &mockMutation{},
			wantResult: nil,
		},
		{
			name:       "non_admin_hits_fallback_deny",
			policy:     adminGatePolicy,
			ctx:        regularCtx,
			mutation:   &mockMutation{},
			wantResult: privacy.Deny,
		},
		{
			name:       "no_viewer_denied_at_gate",
			policy:     adminGatePolicy,
			ctx:        noViewerCtx,
			mutation:   &mockMutation{},
			wantResult: privacy.Deny,
		},
		{
			name: "multiple_role_checks_first_match_wins",
			policy: privacy.MutationPolicy{
				privacy.HasRole("superadmin"),
				privacy.HasRole("admin"),
				privacy.AlwaysDenyRule(),
			},
			ctx:        adminCtx, // has "admin" but not "superadmin"
			mutation:   &mockMutation{},
			wantResult: nil, // HasRole("admin") allows → short-circuits
		},
		{
			name: "all_rules_skip_then_implicit_allow",
			policy: privacy.MutationPolicy{
				privacy.MutationRuleFunc(func(_ context.Context, _ velox.Mutation) error {
					return privacy.Skip
				}),
				privacy.MutationRuleFunc(func(_ context.Context, _ velox.Mutation) error {
					return privacy.Skip
				}),
			},
			ctx:        context.Background(),
			mutation:   &mockMutation{},
			wantResult: nil, // all skip → nil (allowed)
		},
		{
			name:       "empty_policy_allows",
			policy:     privacy.MutationPolicy{},
			ctx:        context.Background(),
			mutation:   &mockMutation{},
			wantResult: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.policy.EvalMutation(tt.ctx, tt.mutation)
			if tt.wantResult == nil {
				assert.NoError(t, err)
			} else {
				assert.True(t, errors.Is(err, tt.wantResult))
			}
		})
	}
}

// TestTenantIsolationCombinations exercises the TenantRule in table-driven form.
func TestTenantIsolationCombinations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		viewerTID  string
		fieldValue any
		hasField   bool
		wantResult error
	}{
		{
			name:       "matching_tenant_allowed",
			viewerTID:  "tenant-a",
			fieldValue: "tenant-a",
			hasField:   true,
			wantResult: nil, // Allow → nil
		},
		{
			name:       "mismatched_tenant_denied",
			viewerTID:  "tenant-a",
			fieldValue: "tenant-b",
			hasField:   true,
			wantResult: privacy.Deny,
		},
		{
			name:       "missing_tenant_field_skips_to_deny",
			viewerTID:  "tenant-a",
			fieldValue: nil,
			hasField:   false,
			wantResult: privacy.Deny, // Skip → AlwaysDeny fires
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			policy := privacy.MutationPolicy{
				privacy.DenyIfNoViewer(),
				privacy.TenantRule("tenant_id"),
				privacy.AlwaysDenyRule(),
			}
			viewer := &privacy.SimpleViewer{UserID: "u1", UserTenant: tt.viewerTID}
			ctx := privacy.WithViewer(context.Background(), viewer)
			m := &mockMutation{field: "tenant_id", value: tt.fieldValue, hasField: tt.hasField}

			err := policy.EvalMutation(ctx, m)
			if tt.wantResult == nil {
				assert.NoError(t, err)
			} else {
				assert.True(t, errors.Is(err, tt.wantResult))
			}
		})
	}
}

// TestOwnerTenantAdminChain tests the classic 3-factor policy:
// (1) require viewer, (2) allow admin, (3) require matching tenant, (4) allow owner, (5) deny all.
func TestOwnerTenantAdminChain(t *testing.T) {
	t.Parallel()

	policy := privacy.MutationPolicy{
		privacy.DenyIfNoViewer(),
		privacy.HasRole("admin"),
		privacy.TenantRule("tenant_id"),
		privacy.IsOwner("user_id"),
		privacy.AlwaysDenyRule(),
	}

	tests := []struct {
		name     string
		viewer   *privacy.SimpleViewer
		mutation *mockMutation
		wantNil  bool // true = expect nil (allowed), false = expect Deny
	}{
		{
			name:   "admin_always_allowed",
			viewer: &privacy.SimpleViewer{UserID: "a1", UserRoles: []string{"admin"}, UserTenant: "t1"},
			mutation: &mockMutation{
				field: "tenant_id", value: "t-other", hasField: true,
			},
			wantNil: true,
		},
		{
			name:   "owner_in_correct_tenant_allowed",
			viewer: &privacy.SimpleViewer{UserID: "u1", UserRoles: []string{"user"}, UserTenant: "t1"},
			mutation: &mockMutation{
				field: "tenant_id", value: "t1", hasField: true,
			},
			wantNil: true, // TenantRule allows → short-circuit
		},
		{
			name:   "owner_in_wrong_tenant_denied",
			viewer: &privacy.SimpleViewer{UserID: "u1", UserRoles: []string{"user"}, UserTenant: "t1"},
			mutation: &mockMutation{
				field: "tenant_id", value: "t2", hasField: true,
			},
			wantNil: false,
		},
		{
			name:    "no_viewer_denied",
			viewer:  nil,
			wantNil: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			if tt.viewer != nil {
				ctx = privacy.WithViewer(ctx, tt.viewer)
			}
			m := tt.mutation
			if m == nil {
				m = &mockMutation{}
			}
			err := policy.EvalMutation(ctx, m)
			if tt.wantNil {
				assert.NoError(t, err)
			} else {
				assert.True(t, errors.Is(err, privacy.Deny))
			}
		})
	}
}

// TestDecisionContextSkipAndNilNotStored verifies that Skip and nil decisions
// are not stored in the context (and thus don't override future policy calls).
func TestDecisionContextSkipAndNilNotStored(t *testing.T) {
	t.Parallel()

	t.Run("skip_not_stored", func(t *testing.T) {
		t.Parallel()
		ctx := privacy.DecisionContext(context.Background(), privacy.Skip)
		_, ok := privacy.DecisionFromContext(ctx)
		assert.False(t, ok, "Skip should not be stored in context")
	})

	t.Run("nil_not_stored", func(t *testing.T) {
		t.Parallel()
		ctx := privacy.DecisionContext(context.Background(), nil)
		_, ok := privacy.DecisionFromContext(ctx)
		assert.False(t, ok, "nil should not be stored in context")
	})
}

// TestPoliciesShortCircuitOnAllow verifies that Policies stops evaluating after
// the first Allow is returned.
func TestPoliciesShortCircuitOnAllow(t *testing.T) {
	t.Parallel()

	panicRule := queryRuleFunc(func(_ context.Context, _ velox.Query) error {
		panic("should not be evaluated after Allow")
	})

	policies := privacy.QueryPolicy{
		queryRuleFunc(func(_ context.Context, _ velox.Query) error { return privacy.Allow }),
		panicRule,
	}

	assert.NotPanics(t, func() {
		err := policies.EvalQuery(context.Background(), &mockQuery{})
		assert.NoError(t, err)
	})
}

// TestFormattedDecisionWrapping verifies that Allowf/Denyf/Skipf wrap their
// sentinel correctly and carry the formatted message.
func TestFormattedDecisionWrapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		err       error
		sentinel  error
		msgSubstr string
	}{
		{
			name:      "allowf_wraps_allow",
			err:       privacy.Allowf("user %s cleared", "alice"),
			sentinel:  privacy.Allow,
			msgSubstr: "user alice cleared",
		},
		{
			name:      "denyf_wraps_deny",
			err:       privacy.Denyf("role %s insufficient", "guest"),
			sentinel:  privacy.Deny,
			msgSubstr: "role guest insufficient",
		},
		{
			name:      "skipf_wraps_skip",
			err:       privacy.Skipf("rule %d not applicable", 7),
			sentinel:  privacy.Skip,
			msgSubstr: "rule 7 not applicable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.True(t, errors.Is(tt.err, tt.sentinel))
			assert.Contains(t, tt.err.Error(), tt.msgSubstr)
		})
	}
}
