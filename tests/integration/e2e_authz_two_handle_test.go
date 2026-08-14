package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect"
	"github.com/syssam/velox/dialect/sql"
	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/integration/intercept"
	"github.com/syssam/velox/tests/integration/post"
	ormquery "github.com/syssam/velox/tests/integration/query"
	"github.com/syssam/velox/tests/integration/user"
)

// Ambient row scoping — an interceptor or a Policy driven by ctx — cannot
// tell two kinds of reads apart:
//
//	(a) rows to be returned to the caller        -> must be scoped
//	(b) rows that guard an invariant             -> must NOT be scoped
//
// Only the call site knows which it is. Scoping (b) does not leak data,
// it destroys it: an Exist() dependency check that returns false because
// the blocking row sat outside the caller's scope lets the delete through.
//
// The structural fix is to make the distinction a property of the HANDLE
// rather than of the context: one client carries the scope, one does not.
// A handle cannot be inherited invisibly down a call stack the way a ctx
// value can, and the choice is visible at every call site in review.
//
// These tests pin that velox supports the pattern, and pin the constraint
// that decides which mechanism can implement it:
//
//	interceptor -> per-client  (c.interStore)      -> two handles possible
//	Policy()    -> process-global (RuntimePolicy)  -> two handles IMPOSSIBLE
//
// That constraint is why Policy() is NOT the right mechanism for row-level
// tenancy despite covering more surfaces than interceptors: its only escape
// hatch is an ambient context flag, which fails open on the destructive side.

// twoHandles builds a scoped and an unscoped client over the SAME driver,
// so both see identical data and differ only in registered interceptors.
func twoHandles(t *testing.T) (scoped, system *integration.Client) {
	t.Helper()

	drv, err := sql.Open(dialect.SQLite, ":memory:?_pragma=foreign_keys(1)")
	require.NoError(t, err)
	t.Cleanup(func() { drv.Close() })

	system = integration.NewClient(integration.Driver(drv))
	require.NoError(t, system.Schema.Create(context.Background()))

	scoped = integration.NewClient(integration.Driver(drv))
	scoped.User.Intercept(intercept.TraverseUser(func(_ context.Context, q *ormquery.UserQuery) error {
		q.Where(user.NameField.EQ("alice"))
		return nil
	}))
	scoped.Post.Intercept(intercept.TraversePost(func(_ context.Context, q *ormquery.PostQuery) error {
		q.Where(post.TitleField.EQ("visible"))
		return nil
	}))
	return scoped, system
}

// TestTwoHandles_ScopeIsPerClientNotGlobal pins that registering an
// interceptor on one client does not scope another client built over the
// same driver. Without this, the two-handle pattern is unavailable and
// every internal read must opt out by hand.
func TestTwoHandles_ScopeIsPerClientNotGlobal(t *testing.T) {
	ctx := context.Background()
	scoped, system := twoHandles(t)

	for _, name := range []string{"alice", "bob"} {
		_, err := system.User.Create().
			SetName(name).SetEmail(name + "@x").SetAge(30).
			SetCreatedAt(now).SetUpdatedAt(now).Save(ctx)
		require.NoError(t, err)
	}

	got, err := scoped.User.Query().All(ctx)
	require.NoError(t, err)
	assert.Len(t, got, 1, "the scoped handle must see only in-scope rows")

	all, err := system.User.Query().All(ctx)
	require.NoError(t, err)
	assert.Len(t, all, 2,
		"the system handle must be unaffected — interceptors live on the client "+
			"(c.interStore), not in a process-global. If this ever returns 1, every "+
			"internal invariant check in every velox app has silently narrowed.")
}

// TestTwoHandles_InvariantCheckSurvivesScoping is the failure this whole
// pattern exists to prevent, reproduced concretely.
//
// A "can I delete this user?" guard asks whether any Post depends on it.
// Run through a scoped handle the guard answers "no dependents" whenever
// the blocking row is out of scope, and the delete proceeds. Run through
// the system handle it answers correctly.
//
// Note the direction of failure. An under-scoped read discloses data and
// can be audited after the fact. An over-scoped invariant check deletes
// rows that should have been protected, and nothing detects it later.
func TestTwoHandles_InvariantCheckSurvivesScoping(t *testing.T) {
	ctx := context.Background()
	scoped, system := twoHandles(t)

	// bob is outside the scope and owns a post — he must not be deletable.
	bob, err := system.User.Create().
		SetName("bob").SetEmail("bob@x").SetAge(30).
		SetCreatedAt(now).SetUpdatedAt(now).Save(ctx)
	require.NoError(t, err)
	_, err = system.Post.Create().
		SetTitle("hidden").SetContent("c").SetStatus("published").SetViewCount(0).
		SetAuthorID(bob.ID).SetCreatedAt(now).SetUpdatedAt(now).Save(ctx)
	require.NoError(t, err)

	// The guard, asked through the SCOPED handle.
	scopedSaysHasPosts, err := scoped.Post.Query().
		Where(post.HasAuthorWith(user.IDField.EQ(bob.ID))).
		Exist(ctx)
	require.NoError(t, err)

	// The same guard, asked through the SYSTEM handle.
	systemSaysHasPosts, err := system.Post.Query().
		Where(post.HasAuthorWith(user.IDField.EQ(bob.ID))).
		Exist(ctx)
	require.NoError(t, err)

	assert.True(t, systemSaysHasPosts,
		"the system handle must see bob's post — this is the correct answer")
	assert.False(t, scopedSaysHasPosts,
		"DOCUMENTED HAZARD: the scoped handle reports no dependents for an "+
			"out-of-scope owner. A delete guarded by this check would destroy data. "+
			"Invariant checks must run on the system handle, never the scoped one.")
}
