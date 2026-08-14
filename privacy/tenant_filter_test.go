package privacy

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect/sql"
)

// recordingFilter captures the predicates a rule applies.
type recordingFilter struct{ applied int }

func (f *recordingFilter) WhereP(ps ...func(*sql.Selector)) { f.applied += len(ps) }

// intTenantViewer carries a non-string tenant id.
type intTenantViewer struct{ tenant int }

func (v intTenantViewer) ID() string         { return "u1" }
func (v intTenantViewer) Roles() []string    { return nil }
func (v intTenantViewer) TenantIDValue() any { return v.tenant }

// TestTenantFilterRule_AppliesPredicate is the behavior TenantQueryRule
// never had: the rule must actually narrow the statement.
//
// TenantQueryRule only checks that a tenant is present and returns Skip —
// a policy built from it denies unauthenticated callers while returning
// every tenant's rows to authenticated ones, which reads as isolation and
// is not. docs/privacy.md documented that shape (with a signature that
// did not even compile) as "Multi-Tenant Isolation".
func TestTenantFilterRule_AppliesPredicate(t *testing.T) {
	ctx := WithViewer(context.Background(), &SimpleViewer{
		UserID:     "u1",
		UserTenant: "acme",
	})

	f := &recordingFilter{}
	err := TenantFilterRule("tenant_id").(FilterFunc)(ctx, f)

	assert.ErrorIs(t, err, Skip, "the rule defers to later rules after filtering")
	assert.Equal(t, 1, f.applied, "TenantFilterRule must append exactly one predicate")
}

// TestTenantFilterRule_DeniesWithoutTenant pins the direction of failure:
// a tenant-scoped entity reached without a tenant must fail, never fall
// through to an unfiltered read.
func TestTenantFilterRule_DeniesWithoutTenant(t *testing.T) {
	tests := []struct {
		name string
		ctx  func() context.Context
	}{
		{"no viewer at all", func() context.Context {
			return context.Background()
		}},
		{"viewer with empty tenant", func() context.Context {
			return WithViewer(context.Background(), &SimpleViewer{UserID: "u1"})
		}},
		{"viewer that is not a TenantIDer", func() context.Context {
			return WithViewer(context.Background(), tenantlessViewer{})
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := &recordingFilter{}
			err := TenantFilterRule("tenant_id").(FilterFunc)(tc.ctx(), f)

			require.Error(t, err)
			assert.NotErrorIs(t, err, Skip,
				"a missing tenant must deny, not skip into an unfiltered read")
			assert.Zero(t, f.applied, "no predicate is applied on the deny path")
		})
	}
}

// TestTenantFilterRule_PrefersTenantIDValuer pins the non-string path.
// TenantIDer returns a string; binding a string against an integer or
// uuid tenant column errors on Postgres (`operator does not exist:
// integer = text`) while silently working on SQLite and MySQL — a
// dialect-dependent failure that only shows up in production.
func TestTenantFilterRule_PrefersTenantIDValuer(t *testing.T) {
	value, err := viewerTenantValue(intTenantViewer{tenant: 42})
	require.NoError(t, err)
	assert.Equal(t, 42, value, "TenantIDValue wins over the string TenantID path")

	value, err = viewerTenantValue(&SimpleViewer{UserTenant: "acme"})
	require.NoError(t, err)
	assert.Equal(t, "acme", value, "viewers without TenantIDValuer fall back to TenantID")
}

// TestTenantFilterRule_IsQueryAndMutationRule pins that one registration
// covers reads and predicate-based writes. Interceptors cannot reach
// writes at all, so this is the property that makes Policy the right
// place for row-level authorization.
func TestTenantFilterRule_IsQueryAndMutationRule(t *testing.T) {
	var rule any = TenantFilterRule("tenant_id")

	_, isQuery := rule.(QueryRule)
	_, isMutation := rule.(MutationRule)
	assert.True(t, isQuery, "must be usable in a QueryPolicy")
	assert.True(t, isMutation, "must be usable in a MutationPolicy — bulk UPDATE/DELETE need it")
}

// tenantlessViewer implements Viewer but neither tenant interface.
type tenantlessViewer struct{}

func (tenantlessViewer) ID() string      { return "u1" }
func (tenantlessViewer) Roles() []string { return nil }
