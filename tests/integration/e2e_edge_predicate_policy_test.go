package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/integration/entity"
	"github.com/syssam/velox/tests/integration/post"
	"github.com/syssam/velox/tests/integration/user"
	testschema "github.com/syssam/velox/testschema"
)

// TestMultiDialect_EdgePredicateAppliesTargetPolicy pins that an edge
// predicate reads its target through the target's privacy policy. HasXxx /
// HasXxxWith compile to a subquery with no query object behind it, so the
// target's Policy never ran: under a User policy that scopes users to
// "alice", Post.Query().Where(HasAuthorWith(...)) still matched bob's posts
// — an existence oracle a GraphQL client drives through WhereInput
// (hasAuthorWith). Now a filtering policy narrows the subquery, and a
// denying one fails the whole query, as eager-loading a denied edge does.
func TestMultiDialect_EdgePredicateAppliesTargetPolicy(t *testing.T) {
	forEachDialect(t, func(t *testing.T, c *integration.Client) {
		base := context.Background()
		mk := func(name string) int {
			u, err := c.User.Create().SetName(name).SetEmail(name + "@ep").SetAge(30).
				SetRole(user.RoleUser).SetCreatedAt(now).SetUpdatedAt(now).Save(base)
			require.NoError(t, err)
			_, err = c.Post.Create().SetTitle(name + "-post").SetContent("c").SetStatus(post.StatusPublished).
				SetViewCount(0).SetAuthorID(u.ID).SetCreatedAt(now).SetUpdatedAt(now).Save(base)
			require.NoError(t, err)
			return u.ID
		}
		mk("alice")
		bob := mk("bob")

		scoped := testschema.FilterUserQueryToNameContext(base, "alice")
		for name, pred := range map[string]func() []string{
			"HasAuthorWith": func() []string {
				ps, err := c.Post.Query().Where(post.HasAuthorWith(user.IDField.GT(0))).All(scoped)
				require.NoError(t, err)
				return titles(ps)
			},
			"HasAuthor": func() []string {
				ps, err := c.Post.Query().Where(post.HasAuthor()).All(scoped)
				require.NoError(t, err)
				return titles(ps)
			},
		} {
			require.Equal(t, []string{"alice-post"}, pred(), "%s: bob is outside the User policy's scope", name)
		}

		// Probing a specific out-of-scope author must not reveal the post.
		n, err := c.Post.Query().Where(post.HasAuthorWith(user.IDField.EQ(bob))).Count(scoped)
		require.NoError(t, err)
		require.Zero(t, n, "existence oracle through the edge")

		// A policy that denies fails the query rather than matching nothing.
		denied := testschema.EnforceUserPrivacyContext(base)
		_, err = c.User.Query().All(denied)
		require.Error(t, err, "fixture: the User policy denies")
		_, err = c.Post.Query().Where(post.HasAuthorWith(user.IDField.GT(0))).All(denied)
		require.Error(t, err, "a denied target policy fails the query")
		_, err = c.Post.Query().Where(post.HasAuthor()).Count(denied)
		require.Error(t, err, "HasAuthor under a denying policy")

		// Without a policy context the predicate is unaffected.
		all, err := c.Post.Query().Where(post.HasAuthor()).Count(base)
		require.NoError(t, err)
		require.Equal(t, 2, all)
	})
}

func titles(ps []*entity.Post) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Title
	}
	return out
}

// TestMultiDialect_EdgePredicatePolicyOnWrites pins the same rule for bulk
// UPDATE and DELETE filtered through an edge. A denying target policy leaves
// the edge subquery unfiltered and records its error; the write must fail
// on that error rather than run with the subquery unscoped — which would
// update or delete rows the policy excludes.
func TestMultiDialect_EdgePredicatePolicyOnWrites(t *testing.T) {
	forEachDialect(t, func(t *testing.T, c *integration.Client) {
		base := context.Background()
		for _, name := range []string{"alice", "bob"} {
			u, err := c.User.Create().SetName(name).SetEmail(name + "@ew").SetAge(30).
				SetRole(user.RoleUser).SetCreatedAt(now).SetUpdatedAt(now).Save(base)
			require.NoError(t, err)
			_, err = c.Post.Create().SetTitle(name + "-post").SetContent("c").SetStatus(post.StatusPublished).
				SetViewCount(0).SetAuthorID(u.ID).SetCreatedAt(now).SetUpdatedAt(now).Save(base)
			require.NoError(t, err)
		}

		scoped := testschema.FilterUserQueryToNameContext(base, "alice")
		n, err := c.Post.Update().Where(post.HasAuthorWith(user.IDField.GT(0))).SetViewCount(7).Save(scoped)
		require.NoError(t, err)
		require.Equal(t, 1, n, "only alice's post is in scope")

		denied := testschema.EnforceUserPrivacyContext(base)
		_, err = c.Post.Update().Where(post.HasAuthor()).SetViewCount(9).Save(denied)
		require.Error(t, err, "update through a denied edge")
		_, err = c.Post.Delete().Where(post.HasAuthorWith(user.IDField.GT(0))).Exec(denied)
		require.Error(t, err, "delete through a denied edge")

		ps, err := c.Post.Query().Order(post.ByTitle()).All(base)
		require.NoError(t, err)
		require.Len(t, ps, 2, "nothing was deleted")
		require.Equal(t, 7, ps[0].ViewCount, "alice-post")
		require.Equal(t, 0, ps[1].ViewCount, "bob-post was never in scope, and the denied update did not run")
	})
}
