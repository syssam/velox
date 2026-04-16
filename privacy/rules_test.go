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
		UserID:     "user-123",
		UserRoles:  []string{"admin", "user"},
		UserTenant: "tenant-abc",
	}

	assert.Equal(t, "user-123", viewer.ID())
	assert.Equal(t, []string{"admin", "user"}, viewer.Roles())
	assert.Equal(t, "tenant-abc", viewer.TenantID())
}

// TestViewerContext tests viewer context functions.
func TestViewerContext(t *testing.T) {
	t.Run("WithViewer_and_ViewerFromContext", func(t *testing.T) {
		viewer := &privacy.SimpleViewer{UserID: "user-123"}
		ctx := privacy.WithViewer(context.Background(), viewer)

		retrieved := privacy.ViewerFromContext(ctx)
		require.NotNil(t, retrieved)
		assert.Equal(t, "user-123", retrieved.ID())
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
			viewer:     &privacy.SimpleViewer{UserID: "u1", UserRoles: []string{"admin", "user"}},
			wantResult: privacy.Allow,
		},
		{
			name:       "skips_without_matching_role",
			role:       "superadmin",
			viewer:     &privacy.SimpleViewer{UserID: "u1", UserRoles: []string{"admin", "user"}},
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
			viewer:     &privacy.SimpleViewer{UserID: "u1", UserRoles: []string{}},
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
			viewer:     &privacy.SimpleViewer{UserID: "u1", UserRoles: []string{"admin"}},
			wantResult: privacy.Allow,
		},
		{
			name:       "allows_with_second_matching_role",
			roles:      []string{"admin", "moderator"},
			viewer:     &privacy.SimpleViewer{UserID: "u1", UserRoles: []string{"moderator"}},
			wantResult: privacy.Allow,
		},
		{
			name:       "allows_with_any_matching_role",
			roles:      []string{"admin", "moderator", "editor"},
			viewer:     &privacy.SimpleViewer{UserID: "u1", UserRoles: []string{"user", "editor"}},
			wantResult: privacy.Allow,
		},
		{
			name:       "skips_without_matching_role",
			roles:      []string{"admin", "moderator"},
			viewer:     &privacy.SimpleViewer{UserID: "u1", UserRoles: []string{"user"}},
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

// TestIsOwner_Update_SkipsBecauseCannotVerify tests that IsOwner skips for update operations
// because ownership cannot be verified without a database query.
func TestIsOwner_Update_SkipsBecauseCannotVerify(t *testing.T) {
	rule := privacy.IsOwner("user_id")

	m := &mockMutation{
		op:       velox.OpUpdate,
		field:    "user_id",
		value:    "attacker-id",
		hasField: true,
	}
	ctx := privacy.WithViewer(context.Background(), &privacy.SimpleViewer{UserID: "attacker-id"})

	result := rule.EvalMutation(ctx, m)
	assert.True(t, errors.Is(result, privacy.Skip), "should Skip for updates (cannot verify ownership)")
}

// TestIsOwnerOnCreate tests the canonical function directly (not via deprecated alias).
func TestIsOwnerOnCreate(t *testing.T) {
	rule := privacy.IsOwnerOnCreate("user_id")
	ctx := privacy.WithViewer(context.Background(), &privacy.SimpleViewer{UserID: "user-1"})

	// Allows matching owner on create.
	m := &mockMutation{op: velox.OpCreate, field: "user_id", value: "user-1", hasField: true}
	assert.True(t, errors.Is(rule.EvalMutation(ctx, m), privacy.Allow))

	// Skips on non-matching owner.
	m2 := &mockMutation{op: velox.OpCreate, field: "user_id", value: "user-2", hasField: true}
	assert.True(t, errors.Is(rule.EvalMutation(ctx, m2), privacy.Skip))

	// Skips on update (cannot verify).
	m3 := &mockMutation{op: velox.OpUpdate, field: "user_id", value: "user-1", hasField: true}
	assert.True(t, errors.Is(rule.EvalMutation(ctx, m3), privacy.Skip))
}

// TestIsOwnerOnCreate_UnsupportedType verifies that unsupported field value types
// cause denial rather than silent false-negative (security-critical branch).
func TestIsOwnerOnCreate_UnsupportedType(t *testing.T) {
	rule := privacy.IsOwnerOnCreate("user_id")
	ctx := privacy.WithViewer(context.Background(), &privacy.SimpleViewer{UserID: "user-1"})

	// A raw [16]byte does not implement fmt.Stringer — should deny.
	m := &mockMutation{op: velox.OpCreate, field: "user_id", value: [16]byte{1, 2, 3}, hasField: true}
	result := rule.EvalMutation(ctx, m)
	assert.True(t, errors.Is(result, privacy.Deny), "unsupported type should deny, got: %v", result)

	// A float64 is also unsupported — should deny.
	m2 := &mockMutation{op: velox.OpCreate, field: "user_id", value: 3.14, hasField: true}
	result2 := rule.EvalMutation(ctx, m2)
	assert.True(t, errors.Is(result2, privacy.Deny), "float64 should deny, got: %v", result2)

	// A struct is unsupported — should deny.
	m3 := &mockMutation{op: velox.OpCreate, field: "user_id", value: struct{ x int }{x: 1}, hasField: true}
	result3 := rule.EvalMutation(ctx, m3)
	assert.True(t, errors.Is(result3, privacy.Deny), "struct should deny, got: %v", result3)
}

// TestIsOwnerOnCreate_UintTypes verifies uint and uint64 field values are compared correctly.
func TestIsOwnerOnCreate_UintTypes(t *testing.T) {
	rule := privacy.IsOwnerOnCreate("user_id")
	ctx := privacy.WithViewer(context.Background(), &privacy.SimpleViewer{UserID: "42"})

	// uint matching
	m1 := &mockMutation{op: velox.OpCreate, field: "user_id", value: uint(42), hasField: true}
	assert.True(t, errors.Is(rule.EvalMutation(ctx, m1), privacy.Allow))

	// uint64 matching
	m2 := &mockMutation{op: velox.OpCreate, field: "user_id", value: uint64(42), hasField: true}
	assert.True(t, errors.Is(rule.EvalMutation(ctx, m2), privacy.Allow))

	// uint mismatch
	m3 := &mockMutation{op: velox.OpCreate, field: "user_id", value: uint(99), hasField: true}
	assert.True(t, errors.Is(rule.EvalMutation(ctx, m3), privacy.Skip))
}

// TestIsOwnerOnCreate_Stringer verifies fmt.Stringer values are compared correctly.
func TestIsOwnerOnCreate_Stringer(t *testing.T) {
	rule := privacy.IsOwnerOnCreate("user_id")
	ctx := privacy.WithViewer(context.Background(), &privacy.SimpleViewer{UserID: "custom-id"})

	m := &mockMutation{op: velox.OpCreate, field: "user_id", value: stringerVal("custom-id"), hasField: true}
	assert.True(t, errors.Is(rule.EvalMutation(ctx, m), privacy.Allow))
}

// stringerVal is a test type that implements fmt.Stringer.
type stringerVal string

func (s stringerVal) String() string { return string(s) }

// TestTenantRule_UnsupportedType verifies that TenantRule denies on unsupported field value types.
func TestTenantRule_UnsupportedType(t *testing.T) {
	rule := privacy.TenantRule("tenant_id")
	ctx := privacy.WithViewer(context.Background(), &privacy.SimpleViewer{
		UserID: "u1", UserTenant: "tenant-abc",
	})

	m := &mockMutation{op: velox.OpCreate, field: "tenant_id", value: [16]byte{}, hasField: true}
	result := rule.EvalMutation(ctx, m)
	assert.True(t, errors.Is(result, privacy.Deny), "unsupported type should deny, got: %v", result)
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
			viewer:     &privacy.SimpleViewer{UserID: "u1", UserTenant: "tenant-abc"},
			wantResult: privacy.Allow,
		},
		{
			name:       "denies_with_non_matching_tenant",
			field:      "tenant_id",
			fieldValue: "tenant-xyz",
			hasField:   true,
			viewer:     &privacy.SimpleViewer{UserID: "u1", UserTenant: "tenant-abc"},
			wantResult: privacy.Deny,
		},
		{
			name:       "skips_without_field",
			field:      "tenant_id",
			fieldValue: nil,
			hasField:   false,
			viewer:     &privacy.SimpleViewer{UserID: "u1", UserTenant: "tenant-abc"},
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
			viewer:     &privacy.SimpleViewer{UserID: "u1", UserTenant: ""},
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
		viewer := &privacy.SimpleViewer{UserID: "user-123", UserTenant: ""}
		ctx := privacy.WithViewer(context.Background(), viewer)

		err := rule.EvalQuery(ctx, &mockQuery{})
		assert.True(t, errors.Is(err, privacy.Deny))
	})

	t.Run("skips_with_viewer_and_tenant", func(t *testing.T) {
		viewer := &privacy.SimpleViewer{UserID: "user-123", UserTenant: "tenant-abc"}
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
			UserID:    "admin-1",
			UserRoles: []string{"admin"},
		}
		ctx := privacy.WithViewer(context.Background(), viewer)

		err := policy.EvalMutation(ctx, &mockMutation{})
		assert.NoError(t, err)
	})

	t.Run("user_denied_without_admin_role", func(t *testing.T) {
		policy := privacy.MutationPolicy{
			privacy.DenyIfNoViewer(),
			privacy.HasRole("admin"),
			privacy.AlwaysDenyRule(),
		}

		viewer := &privacy.SimpleViewer{
			UserID:    "user-1",
			UserRoles: []string{"user"},
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
			UserID:    "user-123",
			UserRoles: []string{"user"},
		}
		ctx := privacy.WithViewer(context.Background(), viewer)

		mutation := &mockMutation{
			field:    "user_id",
			value:    "user-123",
			hasField: true,
		}

		err := policy.EvalMutation(ctx, mutation)
		assert.NoError(t, err)
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
			UserID:     "user-1",
			UserTenant: "tenant-a",
		}
		ctx := privacy.WithViewer(context.Background(), viewer)

		// Same tenant - allowed
		mutation := &mockMutation{
			field:    "tenant_id",
			value:    "tenant-a",
			hasField: true,
		}
		err := policy.EvalMutation(ctx, mutation)
		assert.NoError(t, err)

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

// nonTenantViewer is a viewer that does NOT implement TenantIDer.
type nonTenantViewer struct {
	id    string
	roles []string
}

func (v *nonTenantViewer) ID() string      { return v.id }
func (v *nonTenantViewer) Roles() []string { return v.roles }

// TestHasAnyRole_EmptyRoles verifies that HasAnyRole with no roles always skips.
func TestHasAnyRole_EmptyRoles(t *testing.T) {
	rule := privacy.HasAnyRole() // no roles specified
	ctx := privacy.WithViewer(context.Background(), &privacy.SimpleViewer{
		UserID: "u1", UserRoles: []string{"admin"},
	})
	err := rule.EvalQuery(ctx, &mockQuery{})
	assert.True(t, errors.Is(err, privacy.Skip), "empty roles list should skip")
}

// TestTenantRule_ViewerWithoutTenantIDer verifies that TenantRule skips when
// the viewer does not implement TenantIDer.
func TestTenantRule_ViewerWithoutTenantIDer(t *testing.T) {
	rule := privacy.TenantRule("tenant_id")
	ctx := privacy.WithViewer(context.Background(), &nonTenantViewer{id: "u1"})
	m := &mockMutation{field: "tenant_id", value: "t1", hasField: true}
	err := rule.EvalMutation(ctx, m)
	assert.True(t, errors.Is(err, privacy.Skip), "should skip when viewer doesn't implement TenantIDer")
}

// TestTenantQueryRule_ViewerWithoutTenantIDer verifies that TenantQueryRule denies when
// the viewer does not implement TenantIDer.
func TestTenantQueryRule_ViewerWithoutTenantIDer(t *testing.T) {
	rule := privacy.TenantQueryRule()
	ctx := privacy.WithViewer(context.Background(), &nonTenantViewer{id: "u1"})
	err := rule.EvalQuery(ctx, &mockQuery{})
	assert.True(t, errors.Is(err, privacy.Deny), "should deny when viewer doesn't implement TenantIDer")
}

// TestTenantRule_SkipsOnUpdate verifies that TenantRule skips on update/delete operations
// because ownership cannot be verified without a database query.
func TestTenantRule_SkipsOnUpdate(t *testing.T) {
	rule := privacy.TenantRule("tenant_id")
	ctx := privacy.WithViewer(context.Background(), &privacy.SimpleViewer{
		UserID: "u1", UserTenant: "tenant-a",
	})
	for _, op := range []velox.Op{velox.OpUpdate, velox.OpUpdateOne, velox.OpDelete, velox.OpDeleteOne} {
		t.Run(op.String(), func(t *testing.T) {
			m := &mockMutation{op: op, field: "tenant_id", value: "tenant-a", hasField: true}
			err := rule.EvalMutation(ctx, m)
			assert.True(t, errors.Is(err, privacy.Skip),
				"TenantRule should skip on %s (cannot verify without DB query)", op)
		})
	}
}

// TestIntegratedPolicyChain_WithCombinators tests And/Or/Not combinators in policy chains.
func TestIntegratedPolicyChain_WithCombinators(t *testing.T) {
	t.Run("admin_or_moderator", func(t *testing.T) {
		// Or: allow if viewer has "admin" OR "moderator" role.
		policy := privacy.MutationPolicy{
			privacy.DenyIfNoViewer(),
			privacy.Or(
				privacy.HasRole("admin"),
				privacy.HasRole("moderator"),
			),
			privacy.AlwaysDenyRule(),
		}

		// Admin is allowed via first Or branch.
		adminCtx := privacy.WithViewer(context.Background(), &privacy.SimpleViewer{
			UserID: "admin-1", UserRoles: []string{"admin"},
		})
		m := &mockMutation{op: velox.OpCreate, field: "user_id", value: "other-user", hasField: true}
		err := policy.EvalMutation(adminCtx, m)
		assert.NoError(t, err, "admin should be allowed")

		// Moderator is allowed via second Or branch.
		modCtx := privacy.WithViewer(context.Background(), &privacy.SimpleViewer{
			UserID: "mod-1", UserRoles: []string{"moderator"},
		})
		m2 := &mockMutation{op: velox.OpCreate, field: "user_id", value: "mod-1", hasField: true}
		err = policy.EvalMutation(modCtx, m2)
		assert.NoError(t, err, "moderator should be allowed")

		// Regular user is denied.
		userCtx := privacy.WithViewer(context.Background(), &privacy.SimpleViewer{
			UserID: "user-1", UserRoles: []string{"user"},
		})
		m3 := &mockMutation{op: velox.OpCreate, field: "user_id", value: "user-1", hasField: true}
		err = policy.EvalMutation(userCtx, m3)
		assert.True(t, errors.Is(err, privacy.Deny), "regular user should be denied")
	})

	t.Run("owner_create_in_policy_chain", func(t *testing.T) {
		// IsOwnerOnCreate is a MutationRule (not QueryMutationRule), so it goes
		// directly in MutationPolicy — not wrapped in Or.
		policy := privacy.MutationPolicy{
			privacy.DenyIfNoViewer(),
			privacy.HasRole("admin"),
			privacy.IsOwnerOnCreate("user_id"),
			privacy.AlwaysDenyRule(),
		}

		// Owner is allowed.
		ownerCtx := privacy.WithViewer(context.Background(), &privacy.SimpleViewer{
			UserID: "user-1", UserRoles: []string{"user"},
		})
		m := &mockMutation{op: velox.OpCreate, field: "user_id", value: "user-1", hasField: true}
		err := policy.EvalMutation(ownerCtx, m)
		assert.NoError(t, err, "owner should be allowed")

		// Non-owner is denied by AlwaysDenyRule after all others skip.
		nonOwnerCtx := privacy.WithViewer(context.Background(), &privacy.SimpleViewer{
			UserID: "user-2", UserRoles: []string{"user"},
		})
		m2 := &mockMutation{op: velox.OpCreate, field: "user_id", value: "user-1", hasField: true}
		err = policy.EvalMutation(nonOwnerCtx, m2)
		assert.True(t, errors.Is(err, privacy.Deny), "non-owner should be denied")
	})

	t.Run("not_deny_equals_allow", func(t *testing.T) {
		policy := privacy.QueryPolicy{
			privacy.Not(privacy.AlwaysDenyRule()),
		}
		err := policy.EvalQuery(context.Background(), &mockQuery{})
		assert.NoError(t, err, "Not(Deny) should Allow")
	})
}

// BenchmarkRules benchmarks privacy rule evaluation.
func BenchmarkRules(b *testing.B) {
	viewer := &privacy.SimpleViewer{
		UserID:     "user-123",
		UserRoles:  []string{"admin", "user"},
		UserTenant: "tenant-abc",
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
		for b.Loop() {
			_ = rule.EvalQuery(ctx, query)
		}
	})

	b.Run("DenyIfNoViewer_without_viewer", func(b *testing.B) {
		rule := privacy.DenyIfNoViewer()
		for b.Loop() {
			_ = rule.EvalQuery(ctxNoViewer, query)
		}
	})

	b.Run("HasRole", func(b *testing.B) {
		rule := privacy.HasRole("admin")
		for b.Loop() {
			_ = rule.EvalQuery(ctx, query)
		}
	})

	b.Run("HasAnyRole_3_roles", func(b *testing.B) {
		rule := privacy.HasAnyRole("admin", "moderator", "editor")
		for b.Loop() {
			_ = rule.EvalQuery(ctx, query)
		}
	})

	b.Run("IsOwner", func(b *testing.B) {
		rule := privacy.IsOwner("user_id")
		for b.Loop() {
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
		for b.Loop() {
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
		for b.Loop() {
			_ = policy.EvalMutation(ctx, mutation)
		}
	})
}

// Note: mockMutation and mockQuery are defined in privacy_test.go
