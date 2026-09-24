package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestJSONAppend_RepeatedCallsAccumulate pins that AppendX called twice on
// one builder appends both values. The mutation stored each call's slice
// under the column key, overwriting the previous one, so
// AppendLabels(a).AppendLabels(b) wrote only b. Ent accumulates
// (mutation.tmpl). The first call's slice must not be written through: the
// accumulated slice is capacity-clamped before the second append.
func TestJSONAppend_RepeatedCallsAccumulate(t *testing.T) {
	ctx := context.Background()
	c := openTestClient(t)
	u := createUser(t, c, "alice", "alice@x")
	p := createPost(t, c, u, "t", "c")
	_, err := c.Post.UpdateOneID(p.ID).SetLabels([]string{"orig"}).Save(ctx)
	require.NoError(t, err)

	first := make([]string, 1, 8) // spare capacity a bare append would write into
	first[0] = "a"
	got, err := c.Post.UpdateOneID(p.ID).AppendLabels(first).AppendLabels([]string{"b"}).Save(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"orig", "a", "b"}, got.Labels)
	require.Equal(t, []string{"a"}, first, "the caller's slice was modified")
	require.Equal(t, "", first[:2][1], "the caller's spare capacity was written")

	reread, err := c.Post.Get(ctx, p.ID)
	require.NoError(t, err)
	require.Equal(t, []string{"orig", "a", "b"}, reread.Labels)
}
