package privacy_test

import (
	"context"
	"errors"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/privacy"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSimpleViewer tests the SimpleViewer implementation.
func TestSimpleViewer(t *testing.T) {
	viewer := &privacy.SimpleViewer{
		UserID:   "user-123",
		Roles:    []string{"admin", "user"},
		TenantID: "tenant-abc",
	}

	assert.Equal(t, "user-123", viewer.GetID())
	assert.Equal(t, []string{"admin", "user"}, viewer.GetRoles())
	assert.Equal(t, "tenant-abc", viewer.GetTenantID())
}

// TestViewerContext tests viewer context functions.
func TestViewerContext(t *testing.T) {
	t.Run("WithViewer_and_ViewerFromContext", func(t *testing.T) {
		viewer := &privacy.SimpleViewer{UserID: "user-123"}
		ctx := privacy.WithViewer(context.Background(), viewer)

		retrieved := privacy.ViewerFromContext(ctx)
		require.NotNil(t, retrieved)
		assert.Equal(t, "user-123", retrieved.GetID())
	})

	t.Run("ViewerFromContext_returns_nil_without_viewer", func(t *testing.T) {
		ctx := context.Background()
		retrieved := privacy.ViewerFromContext(ctx)
		assert.Nil(t, retrieved)
	})

	t.Run("ViewerFromContext_returns_nil_with_wrong_type", func(t *testing.T) {
		type wrongKey struct{}
		ctx := context.WithValue(context.Background(), wrongKey{}, "not a viewer")
		retrieved := privacy.ViewerFromContext(ctx)
		assert.Nil(t, retrieved)
	})
}

// TestDenyIfNoViewer tests the DenyIfNoViewer rule.
func TestDenyIfNoViewer(t *testing.T) {
	rule := privacy.DenyIfNoViewer()

	t.Run("denies_without_viewer", func(t *testing.T) {
		ctx := context.Background()

		err := rule.EvalQuery(ctx, &mockQuery{})
		assert.True(t, errors.Is(err, privacy.Deny))

		err = rule.EvalMutation(ctx, &mockMutation{})
		assert.True(t, errors.Is(err, privacy.Deny))
	})

	t.Run("skips_with_viewer", func(t *testing.T) {
		viewer := &privacy.SimpleViewer{UserID: "user-123"}
		ctx := privacy.WithViewer(context.Background(), viewer)

		err := rule.EvalQuery(ctx, &mockQuery{})
		assert.True(t, errors.Is(err, privacy.Skip))

		err = rule.EvalMutation(ctx, &mockMutation{})
		assert.True(t, errors.Is(err, privacy.Skip))
	})
}

// TestHasRole tests the HasRole rule.
func TestHasRole(t *testing.T) {
	tests := []struct {
		name       string
		role       string
		viewer     *privacy.SimpleViewer
		wantResult error
	}{
		{
			name:       "allows_with_matching_role",
			role:       "admin",
			viewer:     &privacy.SimpleViewer{UserID: "u1", Roles: []string{"admin", "user"}},
			wantResult: privacy.Allow,
		},
		{
			name:       "skips_without_matching_role",
			role:       "superadmin",
			viewer:     &privacy.SimpleViewer{UserID: "u1", Roles: []string{"admin", "user"}},
			wantResult: privacy.Skip,
		},
		{
			name:       "skips_without_viewer",
			role:       "admin",
			viewer:     nil,
			wantResult: privacy.Skip,
		},
		{
			name:       "skips_with_empty_roles",
			role:       "admin",
			viewer:     &privacy.SimpleViewer{UserID: "u1", Roles: []string{}},
			wantResult: privacy.Skip,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := privacy.HasRole(tt.role)
			ctx := context.Background()
			if tt.viewer != nil {
				ctx = privacy.WithViewer(ctx, tt.viewer)
			}

			err := rule.EvalQuery(ctx, &mockQuery{})
			assert.True(t, errors.Is(err, tt.wantResult))

			err = rule.EvalMutation(ctx, &mockMutation{})
			assert.True(t, errors.Is(err, tt.wantResult))
		})
	}
}

// TestHasAnyRole tests the HasAnyRole rule.
func TestHasAnyRole(t *testing.T) {
	tests := []struct {
		name       string
		roles      []string
		viewer     *privacy.SimpleViewer
		wantResult error
	}{
		{
			name:       "allows_with_first_matching_role",
			roles:      []string{"admin", "moderator"},
			viewer:     &privacy.SimpleViewer{UserID: "u1", Roles: []string{"admin"}},
			wantResult: privacy.Allow,
		},
		{
			name:       "allows_with_second_matching_role",
			roles:      []string{"admin", "moderator"},
			viewer:     &privacy.SimpleViewer{UserID: "u1", Roles: []string{"moderator"}},
			wantResult: privacy.Allow,
		},
		{
			name:       "allows_with_any_matching_role",
			roles:      []string{"admin", "moderator", "editor"},
			viewer:     &privacy.SimpleViewer{UserID: "u1", Roles: []string{"user", "editor"}},
			wantResult: privacy.Allow,
		},
		{
			name:       "skips_without_matching_role",
			roles:      []string{"admin", "moderator"},
			viewer:     &privacy.SimpleViewer{UserID: "u1", Roles: []string{"user"}},
			wantResult: privacy.Skip,
		},
		{
			name:       "skips_without_viewer",
			roles:      []string{"admin"},
			viewer:     nil,
			wantResult: privacy.Skip,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := privacy.HasAnyRole(tt.roles...)
			ctx := context.Background()
			if tt.viewer != nil {
				ctx = privacy.WithViewer(ctx, tt.viewer)
			}

			err := rule.EvalQuery(ctx, &mockQuery{})
			assert.True(t, errors.Is(err, tt.wantResult))

			err = rule.EvalMutation(ctx, &mockMutation{})
			assert.True(t, errors.Is(err, tt.wantResult))
		})
	}
}

// TestIsOwner tests the IsOwner rule.
func TestIsOwner(t *testing.T) {
	tests := []struct {
		name       string
		field      string
		fieldValue any
		hasField   bool
		viewer     *privacy.SimpleViewer
		wantResult error
	}{
		{
			name:       "allows_with_matching_string_id",
			field:      "user_id",
			fieldValue: "user-123",
			hasField:   true,
			viewer:     &privacy.SimpleViewer{UserID: "user-123"},
			wantResult: privacy.Allow,
		},
		{
			name:       "allows_with_matching_int64_id",
			field:      "user_id",
			fieldValue: int64(123),
			hasField:   true,
			viewer:     &privacy.SimpleViewer{UserID: "123"},
			wantResult: privacy.Allow,
		},
		{
			name:       "allows_with_matching_int_id",
			field:      "user_id",
			fieldValue: 456,
			hasField:   true,
			viewer:     &privacy.SimpleViewer{UserID: "456"},
			wantResult: privacy.Allow,
		},
		{
			name:       "skips_with_non_matching_id",
			field:      "user_id",
			fieldValue: "user-456",
			hasField:   true,
			viewer:     &privacy.SimpleViewer{UserID: "user-123"},
			wantResult: privacy.Skip,
		},
		{
			name:       "skips_without_field",
			field:      "user_id",
			fieldValue: nil,
			hasField:   false,
			viewer:     &privacy.SimpleViewer{UserID: "user-123"},
			wantResult: privacy.Skip,
		},
		{
			name:       "skips_without_viewer",
			field:      "user_id",
			fieldValue: "user-123",
			hasField:   true,
			viewer:     nil,
			wantResult: privacy.Skip,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := privacy.IsOwner(tt.field)
			ctx := context.Background()
			if tt.viewer != nil {
				ctx = privacy.WithViewer(ctx, tt.viewer)
			}

			mutation := &mockMutation{
				field:    tt.field,
				value:    tt.fieldValue,
				hasField: tt.hasField,
			}

			err := rule.EvalMutation(ctx, mutation)
			assert.True(t, errors.Is(err, tt.wantResult))
		})
	}
}

// TestOwnerQueryRule tests the OwnerQueryRule rule.
func TestOwnerQueryRule(t *testing.T) {
	rule := privacy.OwnerQueryRule()

	t.Run("denies_without_viewer", func(t *testing.T) {
		ctx := context.Background()
		err := rule.EvalQuery(ctx, &mockQuery{})
		assert.True(t, errors.Is(err, privacy.Deny))
	})

	t.Run("skips_with_viewer", func(t *testing.T) {
		viewer := &privacy.SimpleViewer{UserID: "user-123"}
		ctx := privacy.WithViewer(context.Background(), viewer)

		err := rule.EvalQuery(ctx, &mockQuery{})
		assert.True(t, errors.Is(err, privacy.Skip))
	})
}

// TestTenantRule tests the TenantRule rule.
func TestTenantRule(t *testing.T) {
	tests := []struct {
		name       string
		field      string
		fieldValue any
		hasField   bool
		viewer     *privacy.SimpleViewer
		wantResult error
	}{
		{
			name:       "allows_with_matching_tenant",
			field:      "tenant_id",
			fieldValue: "tenant-abc",
			hasField:   true,
			viewer:     &privacy.SimpleViewer{UserID: "u1", TenantID: "tenant-abc"},
			wantResult: privacy.Allow,
		},
		{
			name:       "denies_with_non_matching_tenant",
			field:      "tenant_id",
			fieldValue: "tenant-xyz",
			hasField:   true,
			viewer:     &privacy.SimpleViewer{UserID: "u1", TenantID: "tenant-abc"},
			wantResult: privacy.Deny,
		},
		{
			name:       "skips_without_field",
			field:      "tenant_id",
			fieldValue: nil,
			hasField:   false,
			viewer:     &privacy.SimpleViewer{UserID: "u1", TenantID: "tenant-abc"},
			wantResult: privacy.Skip,
		},
		{
			name:       "skips_without_viewer",
			field:      "tenant_id",
			fieldValue: "tenant-abc",
			hasField:   true,
			viewer:     nil,
			wantResult: privacy.Skip,
		},
		{
			name:       "skips_with_empty_tenant",
			field:      "tenant_id",
			fieldValue: "tenant-abc",
			hasField:   true,
			viewer:     &privacy.SimpleViewer{UserID: "u1", TenantID: ""},
			wantResult: privacy.Skip,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := privacy.TenantRule(tt.field)
			ctx := context.Background()
			if tt.viewer != nil {
				ctx = privacy.WithViewer(ctx, tt.viewer)
			}

			mutation := &mockMutation{
				field:    tt.field,
				value:    tt.fieldValue,
				hasField: tt.hasField,
			}

			err := rule.EvalMutation(ctx, mutation)
			assert.True(t, errors.Is(err, tt.wantResult))
		})
	}
}

// TestTenantQueryRule tests the TenantQueryRule rule.
func TestTenantQueryRule(t *testing.T) {
	rule := privacy.TenantQueryRule()

	t.Run("denies_without_viewer", func(t *testing.T) {
		ctx := context.Background()
		err := rule.EvalQuery(ctx, &mockQuery{})
		assert.True(t, errors.Is(err, privacy.Deny))
	})

	t.Run("denies_with_empty_tenant", func(t *testing.T) {
		viewer := &privacy.SimpleViewer{UserID: "user-123", TenantID: ""}
		ctx := privacy.WithViewer(context.Background(), viewer)

		err := rule.EvalQuery(ctx, &mockQuery{})
		assert.True(t, errors.Is(err, privacy.Deny))
	})

	t.Run("skips_with_viewer_and_tenant", func(t *testing.T) {
		viewer := &privacy.SimpleViewer{UserID: "user-123", TenantID: "tenant-abc"}
		ctx := privacy.WithViewer(context.Background(), viewer)

		err := rule.EvalQuery(ctx, &mockQuery{})
		assert.True(t, errors.Is(err, privacy.Skip))
	})
}

// TestAllowMutationOperationRule tests the AllowMutationOperationRule rule.
func TestAllowMutationOperationRule(t *testing.T) {
	tests := []struct {
		name       string
		allowOp    velox.Op
		mutationOp velox.Op
		wantAllow  bool
	}{
		{
			name:       "allows_create",
			allowOp:    velox.OpCreate,
			mutationOp: velox.OpCreate,
			wantAllow:  true,
		},
		{
			name:       "skips_when_op_doesnt_match",
			allowOp:    velox.OpCreate,
			mutationOp: velox.OpUpdate,
			wantAllow:  false,
		},
		{
			name:       "allows_update",
			allowOp:    velox.OpUpdate,
			mutationOp: velox.OpUpdate,
			wantAllow:  true,
		},
		{
			name:       "allows_delete",
			allowOp:    velox.OpDelete,
			mutationOp: velox.OpDelete,
			wantAllow:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := privacy.AllowMutationOperationRule(tt.allowOp)
			mutation := &mockMutation{op: tt.mutationOp}
			err := rule.EvalMutation(context.Background(), mutation)

			if tt.wantAllow {
				assert.True(t, errors.Is(err, privacy.Allow))
			} else {
				assert.True(t, errors.Is(err, privacy.Skip))
			}
		})
	}
}

// TestIntegratedPolicyChain tests rules combined in a policy chain.
func TestIntegratedPolicyChain(t *testing.T) {
	t.Run("admin_allowed_through_role", func(t *testing.T) {
		policy := privacy.MutationPolicy{
			privacy.DenyIfNoViewer(),
			privacy.HasRole("admin"),
			privacy.AlwaysDenyRule(),
		}

		viewer := &privacy.SimpleViewer{
			UserID: "admin-1",
			Roles:  []string{"admin"},
		}
		ctx := privacy.WithViewer(context.Background(), viewer)

		err := policy.EvalMutation(ctx, &mockMutation{})
		assert.True(t, errors.Is(err, privacy.Allow))
	})

	t.Run("user_denied_without_admin_role", func(t *testing.T) {
		policy := privacy.MutationPolicy{
			privacy.DenyIfNoViewer(),
			privacy.HasRole("admin"),
			privacy.AlwaysDenyRule(),
		}

		viewer := &privacy.SimpleViewer{
			UserID: "user-1",
			Roles:  []string{"user"},
		}
		ctx := privacy.WithViewer(context.Background(), viewer)

		err := policy.EvalMutation(ctx, &mockMutation{})
		assert.True(t, errors.Is(err, privacy.Deny))
	})

	t.Run("owner_allowed", func(t *testing.T) {
		policy := privacy.MutationPolicy{
			privacy.DenyIfNoViewer(),
			privacy.HasRole("admin"),
			privacy.IsOwner("user_id"),
			privacy.AlwaysDenyRule(),
		}

		viewer := &privacy.SimpleViewer{
			UserID: "user-123",
			Roles:  []string{"user"},
		}
		ctx := privacy.WithViewer(context.Background(), viewer)

		mutation := &mockMutation{
			field:    "user_id",
			value:    "user-123",
			hasField: true,
		}

		err := policy.EvalMutation(ctx, mutation)
		assert.True(t, errors.Is(err, privacy.Allow))
	})

	t.Run("unauthenticated_denied", func(t *testing.T) {
		policy := privacy.MutationPolicy{
			privacy.DenyIfNoViewer(),
			privacy.HasRole("admin"),
			privacy.AlwaysDenyRule(),
		}

		ctx := context.Background()
		err := policy.EvalMutation(ctx, &mockMutation{})
		assert.True(t, errors.Is(err, privacy.Deny))
	})

	t.Run("tenant_isolation", func(t *testing.T) {
		policy := privacy.MutationPolicy{
			privacy.DenyIfNoViewer(),
			privacy.TenantRule("tenant_id"),
			privacy.AlwaysDenyRule(),
		}

		viewer := &privacy.SimpleViewer{
			UserID:   "user-1",
			TenantID: "tenant-a",
		}
		ctx := privacy.WithViewer(context.Background(), viewer)

		// Same tenant - allowed
		mutation := &mockMutation{
			field:    "tenant_id",
			value:    "tenant-a",
			hasField: true,
		}
		err := policy.EvalMutation(ctx, mutation)
		assert.True(t, errors.Is(err, privacy.Allow))

		// Different tenant - denied
		mutation = &mockMutation{
			field:    "tenant_id",
			value:    "tenant-b",
			hasField: true,
		}
		err = policy.EvalMutation(ctx, mutation)
		assert.True(t, errors.Is(err, privacy.Deny))
	})
}

// BenchmarkRules benchmarks privacy rule evaluation.
func BenchmarkRules(b *testing.B) {
	viewer := &privacy.SimpleViewer{
		UserID:   "user-123",
		Roles:    []string{"admin", "user"},
		TenantID: "tenant-abc",
	}
	ctx := privacy.WithViewer(context.Background(), viewer)
	ctxNoViewer := context.Background()
	query := &mockQuery{}
	mutation := &mockMutation{
		field:    "user_id",
		value:    "user-123",
		hasField: true,
	}

	b.Run("DenyIfNoViewer_with_viewer", func(b *testing.B) {
		rule := privacy.DenyIfNoViewer()
		for i := 0; i < b.N; i++ {
			_ = rule.EvalQuery(ctx, query)
		}
	})

	b.Run("DenyIfNoViewer_without_viewer", func(b *testing.B) {
		rule := privacy.DenyIfNoViewer()
		for i := 0; i < b.N; i++ {
			_ = rule.EvalQuery(ctxNoViewer, query)
		}
	})

	b.Run("HasRole", func(b *testing.B) {
		rule := privacy.HasRole("admin")
		for i := 0; i < b.N; i++ {
			_ = rule.EvalQuery(ctx, query)
		}
	})

	b.Run("HasAnyRole_3_roles", func(b *testing.B) {
		rule := privacy.HasAnyRole("admin", "moderator", "editor")
		for i := 0; i < b.N; i++ {
			_ = rule.EvalQuery(ctx, query)
		}
	})

	b.Run("IsOwner", func(b *testing.B) {
		rule := privacy.IsOwner("user_id")
		for i := 0; i < b.N; i++ {
			_ = rule.EvalMutation(ctx, mutation)
		}
	})

	b.Run("TenantRule", func(b *testing.B) {
		tenantMutation := &mockMutation{
			field:    "tenant_id",
			value:    "tenant-abc",
			hasField: true,
		}
		rule := privacy.TenantRule("tenant_id")
		for i := 0; i < b.N; i++ {
			_ = rule.EvalMutation(ctx, tenantMutation)
		}
	})

	b.Run("PolicyChain_5_rules", func(b *testing.B) {
		policy := privacy.MutationPolicy{
			privacy.DenyIfNoViewer(),
			privacy.HasRole("superadmin"),
			privacy.HasAnyRole("admin", "moderator"),
			privacy.IsOwner("user_id"),
			privacy.AlwaysDenyRule(),
		}
		for i := 0; i < b.N; i++ {
			_ = policy.EvalMutation(ctx, mutation)
		}
	})
}

// Note: mockMutation and mockQuery are defined in privacy_test.go
