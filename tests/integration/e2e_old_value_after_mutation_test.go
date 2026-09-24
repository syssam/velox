package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/runtime"
	integration "github.com/syssam/velox/tests/integration"
)

// TestOldValue_AfterMutationIsAnError pins that OldXxx called after the
// UPDATE ran returns an error instead of the new value. The old-value
// loader re-read the row lazily, so an audit hook that read OldName after
// next.Mutate recorded "after" as the previous name, with no error. Ent
// marks the mutation done and refuses the load; a value the hook loaded
// before the UPDATE is still returned.
func TestOldValue_AfterMutationIsAnError(t *testing.T) {
	ctx := context.Background()
	type oldNamer interface {
		OldName(context.Context) (string, error)
	}

	t.Run("not_loaded_before", func(t *testing.T) {
		c := openTestClient(t)
		u := createUser(t, c, "before", "a@x")
		var got string
		var gotErr error
		c.User.Use(func(next integration.Mutator) integration.Mutator {
			return integration.MutateFunc(func(ctx context.Context, m integration.Mutation) (integration.Value, error) {
				v, err := next.Mutate(ctx, m)
				if m.Op().Is(integration.OpUpdateOne) {
					got, gotErr = m.(oldNamer).OldName(ctx)
				}
				return v, err
			})
		})
		_, err := c.User.UpdateOneID(u.ID).SetName("after").Save(ctx)
		require.NoError(t, err)
		require.ErrorIs(t, gotErr, runtime.ErrOldValueAfterMutation, "OldName after the UPDATE returned %q", got)
		require.Contains(t, gotErr.Error(), "before next.Mutate", "the error must tell the hook author what to do")
	})

	t.Run("loaded_before", func(t *testing.T) {
		c := openTestClient(t)
		u := createUser(t, c, "before", "b@x")
		var pre, post string
		var postErr error
		c.User.Use(func(next integration.Mutator) integration.Mutator {
			return integration.MutateFunc(func(ctx context.Context, m integration.Mutation) (integration.Value, error) {
				if !m.Op().Is(integration.OpUpdateOne) {
					return next.Mutate(ctx, m)
				}
				var err error
				if pre, err = m.(oldNamer).OldName(ctx); err != nil {
					return nil, err
				}
				v, err := next.Mutate(ctx, m)
				post, postErr = m.(oldNamer).OldName(ctx)
				return v, err
			})
		})
		_, err := c.User.UpdateOneID(u.ID).SetName("after").Save(ctx)
		require.NoError(t, err)
		require.Equal(t, "before", pre)
		require.NoError(t, postErr, "a value loaded before the UPDATE stays readable")
		require.Equal(t, "before", post)
	})
}
