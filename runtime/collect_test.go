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

		err := CollectFields(ctx, &testQuery{Ctx: &QueryContext{}}, &CollectMeta{})
		assert.NoError(t, err)
	})

	t.Run("delegates_to_collector", func(t *testing.T) {
		defer fieldCollector.Store(nil)

		called := false
		fn := func(_ context.Context, _ FieldCollectable, _ *CollectMeta, _ []string) error {
			called = true
			return nil
		}
		SetFieldCollector(fn)

		err := CollectFields(ctx, &testQuery{Ctx: &QueryContext{}}, &CollectMeta{})
		require.NoError(t, err)
		assert.True(t, called)
	})

	t.Run("returns_error_from_collector", func(t *testing.T) {
		defer fieldCollector.Store(nil)

		fn := func(_ context.Context, _ FieldCollectable, _ *CollectMeta, _ []string) error {
			return fmt.Errorf("collection failed")
		}
		SetFieldCollector(fn)

		err := CollectFields(ctx, &testQuery{Ctx: &QueryContext{}}, &CollectMeta{})
		assert.EqualError(t, err, "collection failed")
	})

	t.Run("forwards_satisfies", func(t *testing.T) {
		defer fieldCollector.Store(nil)

		var got []string
		fn := func(_ context.Context, _ FieldCollectable, _ *CollectMeta, satisfies []string) error {
			got = satisfies
			return nil
		}
		SetFieldCollector(fn)

		err := CollectFields(ctx, &testQuery{Ctx: &QueryContext{}}, &CollectMeta{}, "Node", "User")
		require.NoError(t, err)
		assert.Equal(t, []string{"Node", "User"}, got)
	})
}

// TestCollectFields_PassesMetaThrough pins that the collector receives the
// caller's CollectMeta pointer as given (so CollectedFor reaches it) and
// that a nil meta is a no-op.
func TestCollectFields_PassesMetaThrough(t *testing.T) {
	ctx := context.Background()
	defer fieldCollector.Store(nil)

	var got *CollectMeta
	SetFieldCollector(func(_ context.Context, _ FieldCollectable, meta *CollectMeta, _ []string) error {
		got = meta
		return nil
	})

	meta := &CollectMeta{CollectedFor: map[string][]string{"fullName": {"first_name", "last_name"}}}
	require.NoError(t, CollectFields(ctx, &testQuery{Ctx: &QueryContext{}}, meta))
	assert.Same(t, meta, got, "collector must receive the caller's CollectMeta")

	got = nil
	require.NoError(t, CollectFields(ctx, &testQuery{Ctx: &QueryContext{}}, nil))
	assert.Nil(t, got, "nil meta must not reach the collector")
}
