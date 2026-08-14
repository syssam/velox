package integration_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/privacy"
	"github.com/syssam/velox/runtime"
	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/integration/user"
)

// End-to-end proof that privacy.TenantFilterRule reaches the generated
// SQL, on reads and on predicate-based writes.
//
// The unit tests in privacy/ assert that the rule appends one predicate
// to a Filter. That is not the same claim: the predicate still has to
// survive prepareQuery, the mutation spec builder and the SQL builder,
// and it has to qualify the column against the right table. This test
// executes real statements against SQLite and asserts on rows.
//
// The entity policy is a package-level global (user.RuntimePolicy) read
// by the entity client constructor, so the policy is swapped in before
// the client is built and restored afterwards. These tests must not run
// in parallel with anything else touching that global.
func withTenantPolicy(t *testing.T, column string) *integration.Client {
	t.Helper()

	prev := user.RuntimePolicy
	t.Cleanup(func() { user.RuntimePolicy = prev })

	user.RuntimePolicy = privacy.Policy{
		Query: privacy.QueryPolicy{
			privacy.TenantFilterRule(column),
		},
		Mutation: privacy.MutationPolicy{
			privacy.TenantFilterRule(column),
		},
	}
	return openTestClient(t)
}

// tenantCtx returns a context carrying a viewer scoped to the tenant.
// testschema's User has no tenant column, so an existing column stands in
// for one per test ("name" where the column is always set, "nickname"
// where the point is that the caller can omit it). TenantFilterRule is
// column-agnostic, which keeps these tests independent of a schema change.
func tenantCtx(tenant string) context.Context {
	return privacy.WithViewer(context.Background(), &privacy.SimpleViewer{
		UserID:     "u1",
		UserTenant: tenant,
	})
}

func TestTenantFilterRule_E2E_ScopesReads(t *testing.T) {
	client := withTenantPolicy(t, user.FieldName)
	ctx := tenantCtx("alice")

	// Seed through a context that is allowed to write both rows.
	for _, name := range []string{"alice", "bob"} {
		_, err := client.User.Create().
			SetName(name).SetEmail(name + "@x").SetAge(30).
			SetCreatedAt(now).SetUpdatedAt(now).
			Save(tenantCtx(name))
		require.NoError(t, err)
	}

	got, err := client.User.Query().All(ctx)
	require.NoError(t, err)
	require.Len(t, got, 1, "the tenant predicate must reach the generated SELECT")
	assert.Equal(t, "alice", got[0].Name)

	n, err := client.User.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "Count goes through the same prepareQuery path")
}

func TestTenantFilterRule_E2E_ScopesBulkWrites(t *testing.T) {
	ctx := tenantCtx("alice")

	t.Run("bulk_update", func(t *testing.T) {
		client := withTenantPolicy(t, user.FieldName)
		for _, name := range []string{"alice", "bob"} {
			_, err := client.User.Create().
				SetName(name).SetEmail(name + "@x").SetAge(30).
				SetCreatedAt(now).SetUpdatedAt(now).
				Save(tenantCtx(name))
			require.NoError(t, err)
		}

		// No predicate on the builder: without the policy this rewrites
		// every row in the table.
		affected, err := client.User.Update().SetNickname("touched").Save(ctx)
		require.NoError(t, err)
		assert.Equal(t, 1, affected, "the tenant predicate must reach the UPDATE WHERE clause")

		bob, err := client.User.Query().Where(user.NameField.EQ("bob")).All(tenantCtx("bob"))
		require.NoError(t, err)
		require.Len(t, bob, 1)
		assert.Nil(t, bob[0].Nickname, "an out-of-tenant row must be untouched")
	})

	t.Run("bulk_delete", func(t *testing.T) {
		client := withTenantPolicy(t, user.FieldName)
		for _, name := range []string{"alice", "bob"} {
			_, err := client.User.Create().
				SetName(name).SetEmail(name + "@x").SetAge(30).
				SetCreatedAt(now).SetUpdatedAt(now).
				Save(tenantCtx(name))
			require.NoError(t, err)
		}

		affected, err := client.User.Delete().Exec(ctx)
		require.NoError(t, err)
		assert.Equal(t, 1, affected, "the tenant predicate must reach the DELETE WHERE clause")

		bob, err := client.User.Query().All(tenantCtx("bob"))
		require.NoError(t, err)
		require.Len(t, bob, 1)
		assert.Equal(t, "bob", bob[0].Name, "an out-of-tenant row must survive")
	})
}

// A read with no viewer must fail, not fall through to every tenant's rows.
func TestTenantFilterRule_E2E_DeniesWithoutViewer(t *testing.T) {
	client := withTenantPolicy(t, user.FieldName)
	_, err := client.User.Create().
		SetName("alice").SetEmail("a@x").SetAge(30).
		SetCreatedAt(now).SetUpdatedAt(now).
		Save(tenantCtx("alice"))
	require.NoError(t, err)

	_, err = client.User.Query().All(context.Background())
	require.Error(t, err, "a tenant-scoped read without a viewer must deny")
	assert.Contains(t, err.Error(), "viewer required")
}

// TestTenantFilterRule_E2E_CreateWithoutTenantIsAllowed pins a sharp edge
// of the documented multi-tenant policy so it cannot be mistaken for
// isolation.
//
// MutationPolicy.EvalMutation returns nil — allow — when every rule
// skips. On a CREATE that does not set the tenant column, TenantFilterRule
// appends a predicate the create ignores and skips, and TenantRule skips
// because the field is unset. The row is created with a zero tenant and is
// then invisible to every tenant, including the one that made it.
//
// This is not a cross-tenant leak, it is an orphaned row, and no read path
// will ever surface it. docs/privacy.md therefore tells users to stamp the
// tenant column from a hook rather than accept it from the caller; this
// test is the executable statement of why.
//
// "nickname" stands in for the tenant column here because it is the one
// optional field on testschema's User — the point is a column the caller
// can omit.
func TestTenantFilterRule_E2E_CreateWithoutTenantIsAllowed(t *testing.T) {
	client := withTenantPolicy(t, user.FieldNickname)
	ctx := tenantCtx("acme")

	created, err := client.User.Create().
		SetName("alice").SetEmail("a@x").SetAge(30).
		SetCreatedAt(now).SetUpdatedAt(now).
		Save(ctx) // nickname (the tenant column) deliberately unset
	require.NoError(t, err,
		"documented behavior: a create omitting the tenant column is allowed — "+
			"stamp it from a hook instead. If this starts denying, update "+
			"docs/privacy.md, the change is user-visible")
	require.Nil(t, created.Nickname)

	// And the row it produced is unreachable from that same tenant.
	got, err := client.User.Query().All(ctx)
	require.NoError(t, err)
	assert.Empty(t, got,
		"the orphaned row is invisible to the tenant that created it — this is "+
			"the concrete cost of accepting the tenant column from the caller")
}

// TestTenantFilterRule_E2E_HookStampedTenantClosesTheGap runs the fix
// docs/privacy.md prescribes for the orphaned-row hole above, end to end.
//
// The gap: a CREATE that omits the tenant column passes every policy rule
// and lands a row no tenant can see. The prescribed fix is to stamp the
// column from the viewer in a hook rather than accept it from the caller,
// which makes it both unomittable and unspoofable.
//
// This test exists because the snippet in docs/privacy.md was only ever
// compile-checked. A documented security pattern that has never been run
// is exactly the defect class this session was spent removing.
func TestTenantFilterRule_E2E_HookStampedTenantClosesTheGap(t *testing.T) {
	client := withTenantPolicy(t, user.FieldNickname)

	// The hook from docs/privacy.md, with "nickname" as the tenant column.
	client.User.Use(func(next runtime.Mutator) runtime.Mutator {
		return runtime.MutateFunc(func(ctx context.Context, m runtime.Mutation) (runtime.Value, error) {
			if m.Op().Is(runtime.OpCreate) {
				viewer, ok := privacy.ViewerFromContext(ctx).(privacy.TenantIDer)
				if !ok {
					return nil, errors.New("user: create requires a tenant viewer")
				}
				if err := m.SetField(user.FieldNickname, viewer.TenantID()); err != nil {
					return nil, err
				}
			}
			return next.Mutate(ctx, m)
		})
	})

	acme := tenantCtx("acme")

	t.Run("omitted tenant is stamped, so the row is not orphaned", func(t *testing.T) {
		created, err := client.User.Create().
			SetName("alice").SetEmail("a@x").SetAge(30).
			SetCreatedAt(now).SetUpdatedAt(now).
			Save(acme) // tenant column deliberately unset
		require.NoError(t, err)
		require.NotNil(t, created.Nickname)
		assert.Equal(t, "acme", *created.Nickname, "the hook must stamp the tenant")

		got, err := client.User.Query().All(acme)
		require.NoError(t, err)
		require.Len(t, got, 1,
			"the row is now reachable by its tenant — the orphan is gone")
	})

	t.Run("a spoofed tenant is overwritten", func(t *testing.T) {
		created, err := client.User.Create().
			SetName("mallory").SetEmail("m@x").SetAge(30).
			SetNickname("other-tenant"). // caller tries to plant the row elsewhere
			SetCreatedAt(now).SetUpdatedAt(now).
			Save(acme)
		require.NoError(t, err)
		require.NotNil(t, created.Nickname)
		assert.Equal(t, "acme", *created.Nickname,
			"the hook overwrites the caller's value — the column is unspoofable")
	})

	t.Run("create without a viewer fails loudly", func(t *testing.T) {
		_, err := client.User.Create().
			SetName("nobody").SetEmail("n@x").SetAge(30).
			SetCreatedAt(now).SetUpdatedAt(now).
			Save(context.Background())
		require.Error(t, err, "no viewer must fail, not create an untenanted row")
	})
}
