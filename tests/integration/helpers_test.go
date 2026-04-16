package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect"
	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/integration/entity"
	"github.com/syssam/velox/tests/integration/post"
	"github.com/syssam/velox/tests/integration/user"

	// Query package registers query factories via init().
	_ "github.com/syssam/velox/tests/integration/query"
	// SQLite driver (pure Go, no CGO).
	_ "modernc.org/sqlite"
)

// now is a fixed timestamp used by test fixtures for deterministic output.
var now = time.Now().Truncate(time.Second)

// openTestClient creates a new in-memory SQLite client with schema created.
func openTestClient(t *testing.T) *integration.Client {
	t.Helper()
	client, err := integration.Open(dialect.SQLite, ":memory:?_pragma=foreign_keys(1)")
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	ctx := context.Background()
	require.NoError(t, client.Schema.Create(ctx))
	return client
}

// createUser creates a user with standard fixture defaults.
func createUser(t *testing.T, client *integration.Client, name, email string) *entity.User {
	t.Helper()
	u, err := client.User.Create().
		SetName(name).
		SetEmail(email).
		SetAge(30).
		SetRole(user.RoleUser).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(context.Background())
	require.NoError(t, err)
	return u
}

// createPost creates a post for a given author with standard fixture defaults.
func createPost(t *testing.T, client *integration.Client, author *entity.User, title, content string) *entity.Post {
	t.Helper()
	p, err := client.Post.Create().
		SetTitle(title).
		SetContent(content).
		SetStatus(post.StatusPublished).
		SetViewCount(0).
		SetAuthorID(author.ID).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(context.Background())
	require.NoError(t, err)
	return p
}

// createTag creates a tag.
func createTag(t *testing.T, client *integration.Client, name string) *entity.Tag {
	t.Helper()
	tg, err := client.Tag.Create().
		SetName(name).
		Save(context.Background())
	require.NoError(t, err)
	return tg
}

// createComment creates a comment from a user on a post.
func createComment(t *testing.T, client *integration.Client, author *entity.User, p *entity.Post, text string) *entity.Comment {
	t.Helper()
	c, err := client.Comment.Create().
		SetContent(text).
		SetAuthorID(author.ID).
		SetPostID(p.ID).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(context.Background())
	require.NoError(t, err)
	return c
}
