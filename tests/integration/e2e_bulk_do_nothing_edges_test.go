package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect"
	"github.com/syssam/velox/dialect/sql"
	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/integration/post"
	"github.com/syssam/velox/tests/integration/tag"
	"github.com/syssam/velox/tests/integration/user"
)

// TestMultiDialect_BulkConflictSkipNeverMislinks pins what a bulk create
// does when ON CONFLICT skips some rows. Inserted IDs come back in insert
// order with nothing saying which input they belong to, and they were
// assigned by position: CreateBulk(a+edge, b) with "a" existing returned
// "a" carrying b's ID and linked the post to b, with no error (Ent does the
// same). Now: rows with edges fail the whole create before anything is
// linked, and without edges no row reports another row's ID.
func TestMultiDialect_BulkConflictSkipNeverMislinks(t *testing.T) {
	forEachDialect(t, func(t *testing.T, c *integration.Client) {
		ctx := context.Background()
		u, err := c.User.Create().SetName("u").SetEmail("u@bd").SetAge(30).SetRole(user.RoleUser).
			SetCreatedAt(now).SetUpdatedAt(now).Save(ctx)
		require.NoError(t, err)
		p, err := c.Post.Create().SetTitle("p").SetContent("c").SetStatus(post.StatusPublished).
			SetViewCount(0).SetAuthorID(u.ID).SetCreatedAt(now).SetUpdatedAt(now).Save(ctx)
		require.NoError(t, err)
		existing, err := c.Tag.Create().SetName("a").Save(ctx)
		require.NoError(t, err)
		skip := sql.DoNothing()

		// With edges: refused, and nothing is written.
		_, err = c.Tag.CreateBulk(
			c.Tag.Create().SetName("a").AddPostIDs(p.ID),
			c.Tag.Create().SetName("b"),
		).OnConflict(sql.ConflictColumns(tag.FieldName), skip).Save(ctx)
		require.ErrorContains(t, err, "edges cannot be linked")
		linked, err := p.QueryTags().Count(ctx)
		require.NoError(t, err)
		require.Zero(t, linked, "no edge may be linked")
		n, err := c.Tag.Query().Where(tag.NameField.EQ("b")).Count(ctx)
		require.NoError(t, err)
		require.Zero(t, n, "the create is rolled back")

		// Without edges: the insert-or-skip succeeds; no returned row may
		// carry another row's ID.
		tags, err := c.Tag.CreateBulk(
			c.Tag.Create().SetName("a"),
			c.Tag.Create().SetName("c"),
		).OnConflict(sql.ConflictColumns(tag.FieldName), skip).Save(ctx)
		require.NoError(t, err)
		for _, tg := range tags {
			if tg.ID == 0 {
				continue
			}
			stored, err := c.Tag.Get(ctx, tg.ID)
			require.NoError(t, err)
			require.Equal(t, tg.Name, stored.Name, "returned %q with the ID of %q", tg.Name, stored.Name)
		}

		// The documented alternative links correctly where the database
		// reports per-row IDs. MySQL has no RETURNING, so it refuses instead.
		if c.RuntimeConfig().Driver.Dialect() == dialect.MySQL {
			_, err = c.Tag.CreateBulk(
				c.Tag.Create().SetName("a").AddPostIDs(p.ID),
				c.Tag.Create().SetName("d"),
			).OnConflict(sql.ConflictColumns(tag.FieldName), sql.ResolveWithIgnore()).Save(ctx)
			require.ErrorContains(t, err, "one at a time")
			return
		}
		tags, err = c.Tag.CreateBulk(
			c.Tag.Create().SetName("a").AddPostIDs(p.ID),
			c.Tag.Create().SetName("d"),
		).OnConflict(sql.ConflictColumns(tag.FieldName), sql.ResolveWithIgnore()).Save(ctx)
		require.NoError(t, err)
		require.Equal(t, existing.ID, tags[0].ID)
		got, err := p.QueryTags().Only(ctx)
		require.NoError(t, err)
		require.Equal(t, "a", got.Name)
	})
}
