package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestJSONLabelsSetAndQuery verifies basic []string JSON field set + read back.
func TestJSONLabelsSetAndQuery(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	author := createUser(t, client, "Alice", "alice@example.com")

	p, err := client.Post.Create().
		SetTitle("hello").
		SetContent("body").
		SetAuthorID(author.ID).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		SetLabels([]string{"go", "orm"}).
		Save(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"go", "orm"}, p.Labels)

	got, err := client.Post.Get(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"go", "orm"}, got.Labels)
}

// TestJSONLabelsAppend verifies AppendLabels concatenates onto an existing array.
func TestJSONLabelsAppend(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	author := createUser(t, client, "Alice", "alice@example.com")

	p, err := client.Post.Create().
		SetTitle("hello").
		SetContent("body").
		SetAuthorID(author.ID).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		SetLabels([]string{"go"}).
		Save(ctx)
	require.NoError(t, err)

	_, err = client.Post.UpdateOneID(p.ID).
		AppendLabels([]string{"orm", "sqlite"}).
		Save(ctx)
	require.NoError(t, err)

	got, err := client.Post.Get(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"go", "orm", "sqlite"}, got.Labels)
}
