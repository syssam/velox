package integration_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	integration "github.com/syssam/velox/tests/integration"
	postclient "github.com/syssam/velox/tests/integration/client/post"
)

// TestUpdate_RunsFieldValidators pins that user-defined field validators run on
// UPDATE, not only on CREATE.
//
// They used to run only on CREATE: the generated update check() validated
// required unique edges and nothing else, so a field declared
// `field.String("title").NotEmpty().MaxLen(200)` was enforced by Create and
// silently ignored by Update — `UpdateOneID(id).SetTitle("")` wrote an empty
// title, and `SetViewCount(-5)` wrote a negative count past NonNegative().
// Ent emits the validator block in both check()s.
func TestUpdate_RunsFieldValidators(t *testing.T) {
	longTitle := strings.Repeat("a", 300) // MaxLen(200)

	for _, tc := range []struct {
		name string
		call func(*integration.Client, context.Context, int) error
	}{
		{"UpdateOne/NotEmpty", func(c *integration.Client, ctx context.Context, id int) error {
			_, err := c.Post.UpdateOneID(id).SetTitle("").Save(ctx)
			return err
		}},
		{"UpdateOne/MaxLen", func(c *integration.Client, ctx context.Context, id int) error {
			_, err := c.Post.UpdateOneID(id).SetTitle(longTitle).Save(ctx)
			return err
		}},
		{"UpdateOne/NonNegative", func(c *integration.Client, ctx context.Context, id int) error {
			_, err := c.Post.UpdateOneID(id).SetViewCount(-5).Save(ctx)
			return err
		}},
		{"BulkUpdate/NotEmpty", func(c *integration.Client, ctx context.Context, _ int) error {
			_, err := c.Post.Update().SetTitle("").Save(ctx)
			return err
		}},
		{"BulkUpdate/NonNegative", func(c *integration.Client, ctx context.Context, _ int) error {
			_, err := c.Post.Update().SetViewCount(-5).Save(ctx)
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := openTestClient(t)
			ctx := context.Background()
			author := createUser(t, client, "Alice", "alice@example.com")
			p := createPost(t, client, author, "Hello", "body")

			require.Error(t, tc.call(client, ctx, p.ID), "validator must reject the value")

			// The row must be untouched.
			reread, err := client.Post.Get(ctx, p.ID)
			require.NoError(t, err)
			assert.Equal(t, "Hello", reread.Title)
			assert.Equal(t, 0, reread.ViewCount)
		})
	}
}

// TestUpdate_ValidatesHookWrittenValues pins that check() runs INSIDE the hook
// chain (it is called from sqlSave), so a value a hook writes is validated too.
// A validator that only covered caller-supplied values would let a buggy hook
// put an invalid value straight into the UPDATE.
func TestUpdate_ValidatesHookWrittenValues(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	author := createUser(t, client, "Alice", "alice@example.com")
	p := createPost(t, client, author, "Hello", "body")

	client.Post.Use(func(next integration.Mutator) integration.Mutator {
		return integration.MutateFunc(func(ctx context.Context, m integration.Mutation) (integration.Value, error) {
			if mut, ok := m.(*postclient.PostMutation); ok && m.Op().Is(integration.OpUpdateOne) {
				mut.SetTitle("") // violates NotEmpty
			}
			return next.Mutate(ctx, m)
		})
	})

	_, err := client.Post.UpdateOneID(p.ID).SetContent("new body").Save(ctx)
	require.Error(t, err, "a hook-written value must be validated before the UPDATE")

	reread, err := client.Post.Get(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, "Hello", reread.Title)
}
