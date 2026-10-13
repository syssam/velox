// Package firstuse is a program whose first use of an entity is not a query
// or a mutation of it: the entity is the target of an edge of another, or
// the entity of a join table. An entity is filled in with the code of its
// schema (its policy, its defaults) when it is first used, and these are the
// first uses that are easy to miss; a miss is an entity without its policy.
//
// It is a package of its own because it needs a process in which nothing
// has used the entities yet, and one test, because the steps are in order.
package firstuse

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/orm"
	"github.com/syssam/velox/privacy"
	"github.com/syssam/velox/tests/orm/velox"
	"github.com/syssam/velox/tests/orm/velox/migrate"
	"github.com/syssam/velox/tests/orm/velox/post"
	schema "github.com/syssam/velox/testschema"

	_ "modernc.org/sqlite"
)

func TestEntityFirstUsedThroughAnother(t *testing.T) {
	ctx := context.Background()
	conn, err := velox.Open("sqlite", "file:firstuse?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	require.NoError(t, migrate.Create(ctx, conn))

	// The rows are written without the entities, which stay unused.
	now := time.Now()
	_, err = orm.ExecSQL(ctx, conn, `INSERT INTO users (id, created_at, updated_at, name, email, age, role) VALUES (1, ?, ?, 'ann', 'ann@example.com', 30, 'user')`, now, now)
	require.NoError(t, err)
	_, err = orm.ExecSQL(ctx, conn, `INSERT INTO posts (id, created_at, updated_at, title, content, status, view_count, user_posts) VALUES (1, ?, ?, 'first', '', 'draft', 0, 1)`, now, now)
	require.NoError(t, err)

	// Tag, first used as the target of a condition on an edge: its policy
	// decides, though the query is of Post.
	_, err = velox.Post.Query(conn).Where(post.Tags.Has()).Count(schema.DenyTagQueryContext(ctx))
	require.ErrorIs(t, err, privacy.Deny, "a condition on an edge to an entity that was never used ran without that entity's policy")

	// User, first used as the target of an eager load.
	_, err = velox.Post.Query(conn).WithAuthor().All(schema.EnforceUserPrivacyContext(ctx))
	require.ErrorIs(t, err, privacy.Deny, "an eager load of an entity that was never used ran without that entity's policy")
	posts, err := velox.Post.Query(conn).WithAuthor().All(ctx)
	require.NoError(t, err)
	require.Len(t, posts, 1)
	require.NotNil(t, posts[0].Edges.Author)
	assert.Equal(t, "ann", posts[0].Edges.Author.Name)

	// Like, first used as the entity of a join table: the row of the join
	// gets the defaults of its schema.
	_, err = velox.Post.UpdateByID(conn, 1).Set(post.Likers.Add(1)).SaveOne(ctx)
	require.NoError(t, err)
	rows, err := orm.QuerySQL(ctx, conn, `SELECT liked_at FROM likes WHERE post_id = 1 AND user_id = 1`)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	require.True(t, rows.Next(), "the join row was not written")
	var likedAt time.Time
	require.NoError(t, rows.Scan(&likedAt))
	assert.WithinDuration(t, time.Now(), likedAt, time.Minute, "the join row did not get the default of its schema")
}
