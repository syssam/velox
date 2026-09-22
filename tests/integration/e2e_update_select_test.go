package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/tests/integration/post"
)

// TestUpdateOne_SelectNarrowsReadBackNotWrites pins that UpdateOne.Select()
// narrows the columns read back after the UPDATE and nothing else.
//
// It used to gate every spec.SetField with a slices.Contains(selectFields)
// check, so a field set on the builder but absent from Select() was silently
// dropped from the UPDATE — the write just did not happen, with no error.
// Ent's Select() only builds _spec.Node.Columns.
func TestUpdateOne_SelectNarrowsReadBackNotWrites(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	author := createUser(t, client, "Alice", "alice@example.com")
	p := createPost(t, client, author, "Hello", "body")

	got, err := client.Post.UpdateOneID(p.ID).
		SetTitle("Changed").
		SetViewCount(7).
		Select(post.FieldViewCount).
		Save(ctx)
	require.NoError(t, err)

	// Every SET reaches the database, including the field left out of Select().
	reread, err := client.Post.Get(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, "Changed", reread.Title, "a SET field outside Select() must still be written")
	assert.Equal(t, 7, reread.ViewCount)

	// The returned entity carries the selected column.
	assert.Equal(t, 7, got.ViewCount)
}

// TestUpdateOne_SelectRejectsUnknownColumn pins that an unknown column is
// rejected with a ValidationError before the read-back, rather than escaping
// as a driver-level "no such column". Matches Ent's ValidColumn guard.
func TestUpdateOne_SelectRejectsUnknownColumn(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	author := createUser(t, client, "Alice", "alice@example.com")
	p := createPost(t, client, author, "Hello", "body")

	_, err := client.Post.UpdateOneID(p.ID).SetTitle("Changed").Select("not_a_column").Save(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not_a_column")
	assert.Contains(t, err.Error(), "invalid field")
}
