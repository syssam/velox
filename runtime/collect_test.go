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
		fn := func(_ context.Context, _ FieldCollectable, _ *CollectMeta, _ []string) error {
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

		fn := func(_ context.Context, _ FieldCollectable, _ *CollectMeta, _ []string) error {
			return fmt.Errorf("collection failed")
		}
		SetFieldCollector(fn)

		err := CollectFields(ctx, &QueryBase{Ctx: &QueryContext{}}, nil, nil)
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

		err := CollectFields(ctx, &QueryBase{Ctx: &QueryContext{}}, nil, nil, "Node", "User")
		require.NoError(t, err)
		assert.Equal(t, []string{"Node", "User"}, got)
	})
}

// TestCollectFieldsMeta pins the full-metadata entry point generated code
// uses: the collector receives the CollectMeta pointer as given (so
// CollectedFor reaches it), a nil meta is a no-op, and the legacy
// CollectFields wrapper arrives as a CollectMeta with only columns and edges.
func TestCollectFieldsMeta(t *testing.T) {
	ctx := context.Background()
	defer fieldCollector.Store(nil)

	var got *CollectMeta
	SetFieldCollector(func(_ context.Context, _ FieldCollectable, meta *CollectMeta, _ []string) error {
		got = meta
		return nil
	})

	meta := &CollectMeta{CollectedFor: map[string][]string{"fullName": {"first_name", "last_name"}}}
	require.NoError(t, CollectFieldsMeta(ctx, &QueryBase{Ctx: &QueryContext{}}, meta))
	assert.Same(t, meta, got, "collector must receive the caller's CollectMeta")

	got = nil
	require.NoError(t, CollectFieldsMeta(ctx, &QueryBase{Ctx: &QueryContext{}}, nil))
	assert.Nil(t, got, "nil meta must not reach the collector")

	require.NoError(t, CollectFields(ctx, &QueryBase{Ctx: &QueryContext{}}, map[string]string{"name": "name"}, nil))
	require.NotNil(t, got)
	assert.Equal(t, "name", got.FieldColumns["name"])
	assert.Nil(t, got.CollectedFor, "the legacy wrapper carries no CollectedFor")
}
