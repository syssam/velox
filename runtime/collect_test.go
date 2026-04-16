package runtime

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCollectFields(t *testing.T) {
	ctx := context.Background()

	t.Run("nil_collector_is_noop", func(t *testing.T) {
		fieldCollector.Store(nil)
		defer fieldCollector.Store(nil)

		err := CollectFields(ctx, &QueryBase{Ctx: &QueryContext{}}, nil, nil)
		assert.NoError(t, err)
	})

	t.Run("delegates_to_collector", func(t *testing.T) {
		defer fieldCollector.Store(nil)

		called := false
		fn := func(_ context.Context, _ FieldCollectable, _ map[string]string, _ map[string]EdgeMeta, _ []string) error {
			called = true
			return nil
		}
		SetFieldCollector(fn)

		err := CollectFields(ctx, &QueryBase{Ctx: &QueryContext{}}, nil, nil)
		require.NoError(t, err)
		assert.True(t, called)
	})

	t.Run("returns_error_from_collector", func(t *testing.T) {
		defer fieldCollector.Store(nil)

		fn := func(_ context.Context, _ FieldCollectable, _ map[string]string, _ map[string]EdgeMeta, _ []string) error {
			return fmt.Errorf("collection failed")
		}
		SetFieldCollector(fn)

		err := CollectFields(ctx, &QueryBase{Ctx: &QueryContext{}}, nil, nil)
		assert.EqualError(t, err, "collection failed")
	})

	t.Run("forwards_satisfies", func(t *testing.T) {
		defer fieldCollector.Store(nil)

		var got []string
		fn := func(_ context.Context, _ FieldCollectable, _ map[string]string, _ map[string]EdgeMeta, satisfies []string) error {
			got = satisfies
			return nil
		}
		SetFieldCollector(fn)

		err := CollectFields(ctx, &QueryBase{Ctx: &QueryContext{}}, nil, nil, "Node", "User")
		require.NoError(t, err)
		assert.Equal(t, []string{"Node", "User"}, got)
	})
}
