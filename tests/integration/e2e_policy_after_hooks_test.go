package integration_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/syssam/velox"
	"github.com/syssam/velox/privacy"
	integration "github.com/syssam/velox/tests/integration"
	userclient "github.com/syssam/velox/tests/integration/client/user"
	"github.com/syssam/velox/tests/integration/user"
)

var errForbiddenName = errors.New("name is forbidden")

// withForbiddenNamePolicy swaps in a mutation policy that denies writing the
// name "forbidden". user.RuntimePolicy is a package global read at client
// construction, so this must not run in parallel with other users of it.
func withForbiddenNamePolicy(t *testing.T) *integration.Client {
	t.Helper()
	prev := user.RuntimePolicy
	t.Cleanup(func() { user.RuntimePolicy = prev })
	user.RuntimePolicy = privacy.Policy{
		Mutation: privacy.MutationPolicy{
			privacy.MutationRuleFunc(func(_ context.Context, m velox.Mutation) error {
				if v, ok := m.Field(user.FieldName); ok && v == "forbidden" {
					return privacy.Denyf("%w", errForbiddenName)
				}
				return privacy.Skip
			}),
		},
	}
	return openTestClient(t)
}

// TestPolicy_SeesValuesWrittenByHooks pins that a mutation policy judges the
// values that will actually be written. The policy ran once, before every
// hook, so a hook that changed a field after it — a client Use() hook that
// rewrites the name, a role, an owner — wrote a value the policy never saw.
// It now also runs after the hooks, just before the statement; the check
// before them stays, so a denied write never reaches hooks with side effects.
func TestPolicy_SeesValuesWrittenByHooks(t *testing.T) {
	ctx := context.Background()
	c := withForbiddenNamePolicy(t)
	u, err := c.User.Create().SetName("alice").SetEmail("a@ph").SetAge(30).
		SetCreatedAt(now).SetUpdatedAt(now).Save(ctx)
	require.NoError(t, err, "fixture: a permitted write")

	hookRuns := 0
	c.User.Use(func(next integration.Mutator) integration.Mutator {
		return integration.MutateFunc(func(ctx context.Context, m integration.Mutation) (integration.Value, error) {
			hookRuns++
			return next.Mutate(ctx, m)
		})
	})
	_, err = c.User.UpdateOneID(u.ID).SetName("forbidden").Save(ctx)
	require.ErrorIs(t, err, errForbiddenName, "the caller's own value is checked")
	require.Zero(t, hookRuns, "a request the policy denies up front never reaches a hook")

	c.User.Use(func(next integration.Mutator) integration.Mutator {
		return integration.MutateFunc(func(ctx context.Context, m integration.Mutation) (integration.Value, error) {
			if um, ok := m.(*userclient.UserMutation); ok {
				if _, set := um.Name(); set {
					um.SetName("forbidden")
				}
			}
			return next.Mutate(ctx, m)
		})
	})

	_, err = c.User.Create().SetName("bob").SetEmail("b@ph").SetAge(30).
		SetCreatedAt(now).SetUpdatedAt(now).Save(ctx)
	require.ErrorIs(t, err, errForbiddenName, "create")

	_, err = c.User.UpdateOneID(u.ID).SetName("alice2").Save(ctx)
	require.ErrorIs(t, err, errForbiddenName, "update one")

	_, err = c.User.Update().Where(user.IDField.EQ(u.ID)).SetName("alice3").Save(ctx)
	require.ErrorIs(t, err, errForbiddenName, "bulk update")

	_, err = c.User.CreateBulk(
		c.User.Create().SetName("carol").SetEmail("c@ph").SetAge(30).SetCreatedAt(now).SetUpdatedAt(now),
	).Save(ctx)
	require.ErrorIs(t, err, errForbiddenName, "bulk create")

	names, err := c.User.Query().Select(user.FieldName).Strings(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"alice"}, names, "no hook-written forbidden name reached the table")
}

// TestPolicy_RunsOnceWithoutHooks pins the cost of the post-hook check: it
// runs only when hooks are registered. Without them nothing can change the
// mutation between the two points, so every write evaluates its rules once —
// the same count as before the post-hook check existed.
func TestPolicy_RunsOnceWithoutHooks(t *testing.T) {
	ctx := context.Background()
	var calls int
	var roles []any
	prev := user.RuntimePolicy
	t.Cleanup(func() { user.RuntimePolicy = prev })
	user.RuntimePolicy = privacy.Policy{
		Mutation: privacy.MutationPolicy{
			privacy.MutationRuleFunc(func(_ context.Context, m velox.Mutation) error {
				calls++
				if m.Op().Is(velox.OpCreate) {
					v, _ := m.Field(user.FieldRole)
					roles = append(roles, v)
				}
				return privacy.Skip
			}),
		},
	}
	c := openTestClient(t)
	newUser := func(name string) *userclient.UserCreate {
		return c.User.Create().SetName(name).SetEmail(name + "@ph").SetAge(30).SetCreatedAt(now).SetUpdatedAt(now)
	}

	writes := func(round string) {
		t.Helper()
		u, err := newUser("a" + round).Save(ctx)
		require.NoError(t, err)
		_, err = c.User.CreateBulk(newUser("b"+round), newUser("c"+round)).Save(ctx)
		require.NoError(t, err)
		_, err = c.User.UpdateOneID(u.ID).SetAge(31).Save(ctx)
		require.NoError(t, err)
		_, err = c.User.Update().Where(user.IDField.EQ(u.ID)).SetAge(32).Save(ctx)
		require.NoError(t, err)
		_, err = c.User.Delete().Where(user.IDField.EQ(u.ID)).Exec(ctx)
		require.NoError(t, err)
	}
	const perRound = 6 // create + 2 bulk rows + update one + update + delete

	writes("1")
	require.Equal(t, perRound, calls, "without hooks every write evaluates its rules once")
	// Bulk create applies defaults before the policy, as single-row Save
	// does, so a rule sees the defaulted role on every row.
	require.Equal(t, []any{user.RoleUser, user.RoleUser, user.RoleUser}, roles)

	c.User.Use(func(next integration.Mutator) integration.Mutator {
		return integration.MutateFunc(func(ctx context.Context, m integration.Mutation) (integration.Value, error) {
			return next.Mutate(ctx, m)
		})
	})
	calls = 0
	writes("2")
	require.Equal(t, 2*perRound, calls, "with hooks each write is checked before and after them")
}
