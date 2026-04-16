package dataloader

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockEntity is a test entity.
type mockEntity struct {
	ID   int
	Name string
}

func (e *mockEntity) GetID() int {
	return e.ID
}

// =============================================================================
// OrderByKeys Tests
// =============================================================================

func TestOrderByKeys(t *testing.T) {
	t.Parallel()

	keyFn := func(e *mockEntity) int { return e.ID }

	t.Run("all keys found", func(t *testing.T) {
		t.Parallel()
		keys := []int{1, 2, 3}
		values := []*mockEntity{
			{ID: 3, Name: "third"},
			{ID: 1, Name: "first"},
			{ID: 2, Name: "second"},
		}

		result, errs := OrderByKeys(keys, values, keyFn)

		require.Len(t, result, 3)
		require.Len(t, errs, 3)
		assert.Equal(t, "first", result[0].Name)
		assert.Equal(t, "second", result[1].Name)
		assert.Equal(t, "third", result[2].Name)
		for _, err := range errs {
			assert.NoError(t, err)
		}
	})

	t.Run("some keys missing", func(t *testing.T) {
		t.Parallel()
		keys := []int{1, 2, 3, 4}
		values := []*mockEntity{
			{ID: 1, Name: "first"},
			{ID: 3, Name: "third"},
		}

		result, errs := OrderByKeys(keys, values, keyFn)

		require.Len(t, result, 4)
		require.Len(t, errs, 4)
		assert.Equal(t, "first", result[0].Name)
		assert.Nil(t, result[1])
		assert.Equal(t, "third", result[2].Name)
		assert.Nil(t, result[3])
		assert.NoError(t, errs[0])
		assert.ErrorIs(t, errs[1], ErrNotFound)
		assert.NoError(t, errs[2])
		assert.ErrorIs(t, errs[3], ErrNotFound)
	})

	t.Run("empty keys", func(t *testing.T) {
		t.Parallel()
		keys := []int{}
		values := []*mockEntity{}

		result, errs := OrderByKeys(keys, values, keyFn)

		assert.Empty(t, result)
		assert.Empty(t, errs)
	})

	t.Run("empty values", func(t *testing.T) {
		t.Parallel()
		keys := []int{1, 2, 3}
		values := []*mockEntity{}

		result, errs := OrderByKeys(keys, values, keyFn)

		require.Len(t, result, 3)
		for i, err := range errs {
			assert.ErrorIs(t, err, ErrNotFound, "expected ErrNotFound at index %d", i)
		}
	})

	t.Run("duplicate keys", func(t *testing.T) {
		t.Parallel()
		keys := []int{1, 1, 2}
		values := []*mockEntity{
			{ID: 1, Name: "first"},
			{ID: 2, Name: "second"},
		}

		result, errs := OrderByKeys(keys, values, keyFn)

		require.Len(t, result, 3)
		assert.Equal(t, "first", result[0].Name)
		assert.Equal(t, "first", result[1].Name)
		assert.Equal(t, "second", result[2].Name)
		for _, err := range errs {
			assert.NoError(t, err)
		}
	})
}

func TestOrderByKeysNoError(t *testing.T) {
	t.Parallel()

	keyFn := func(e *mockEntity) int { return e.ID }

	t.Run("returns result without errors", func(t *testing.T) {
		keys := []int{1, 2, 3}
		values := []*mockEntity{
			{ID: 1, Name: "first"},
			{ID: 3, Name: "third"},
		}

		result := OrderByKeysNoError(keys, values, keyFn)

		require.Len(t, result, 3)
		assert.Equal(t, "first", result[0].Name)
		assert.Nil(t, result[1])
		assert.Equal(t, "third", result[2].Name)
	})
}

// =============================================================================
// GroupByKey Tests
// =============================================================================

func TestGroupByKey(t *testing.T) {
	t.Parallel()

	type post struct {
		ID     int
		UserID int
		Title  string
	}

	keyFn := func(p *post) int { return p.UserID }

	t.Run("groups by key", func(t *testing.T) {
		t.Parallel()
		posts := []*post{
			{ID: 1, UserID: 10, Title: "Post 1"},
			{ID: 2, UserID: 10, Title: "Post 2"},
			{ID: 3, UserID: 20, Title: "Post 3"},
			{ID: 4, UserID: 10, Title: "Post 4"},
		}

		grouped := GroupByKey(posts, keyFn)

		require.Len(t, grouped[10], 3)
		require.Len(t, grouped[20], 1)
		assert.Equal(t, "Post 1", grouped[10][0].Title)
		assert.Equal(t, "Post 2", grouped[10][1].Title)
		assert.Equal(t, "Post 4", grouped[10][2].Title)
		assert.Equal(t, "Post 3", grouped[20][0].Title)
	})

	t.Run("empty input", func(t *testing.T) {
		t.Parallel()
		grouped := GroupByKey([]*post{}, keyFn)
		assert.Empty(t, grouped)
	})
}

func TestOrderGroupsByKeys(t *testing.T) {
	t.Parallel()

	t.Run("orders groups by keys", func(t *testing.T) {
		keys := []int{10, 20, 30}
		groups := map[int][]string{
			10: {"a", "b"},
			20: {"c"},
			// 30 is missing
		}

		result := OrderGroupsByKeys(keys, groups)

		require.Len(t, result, 3)
		assert.Equal(t, []string{"a", "b"}, result[0])
		assert.Equal(t, []string{"c"}, result[1])
		assert.Nil(t, result[2])
	})

	t.Run("empty keys", func(t *testing.T) {
		result := OrderGroupsByKeys([]int{}, map[int][]string{})
		assert.Empty(t, result)
	})
}

// =============================================================================
// Cache Tests
// =============================================================================

type mockCache[K comparable, V any] struct {
	data    map[K]V
	cleared []K
}

func newMockCache[K comparable, V any]() *mockCache[K, V] {
	return &mockCache[K, V]{data: make(map[K]V)}
}

func (c *mockCache[K, V]) Prime(key K, value V) {
	c.data[key] = value
}

func (c *mockCache[K, V]) Clear(key K) {
	c.cleared = append(c.cleared, key)
	delete(c.data, key)
}

func TestPrimeMany(t *testing.T) {
	t.Parallel()

	cache := newMockCache[int, *mockEntity]()
	entities := []*mockEntity{
		{ID: 1, Name: "first"},
		{ID: 2, Name: "second"},
	}

	PrimeMany(cache, entities, func(e *mockEntity) int { return e.ID })

	assert.Equal(t, "first", cache.data[1].Name)
	assert.Equal(t, "second", cache.data[2].Name)
}

func TestClearMany(t *testing.T) {
	t.Parallel()

	cache := newMockCache[int, *mockEntity]()
	cache.data[1] = &mockEntity{ID: 1}
	cache.data[2] = &mockEntity{ID: 2}
	cache.data[3] = &mockEntity{ID: 3}

	ClearMany(cache, []int{1, 3})

	assert.Contains(t, cache.cleared, 1)
	assert.Contains(t, cache.cleared, 3)
	assert.NotContains(t, cache.cleared, 2)
}

// =============================================================================
// Context Tests
// =============================================================================

type testLoaders struct {
	UserLoader string
}

func TestWithLoaders(t *testing.T) {
	t.Parallel()

	loaders := &testLoaders{UserLoader: "test"}
	ctx := WithLoaders(context.Background(), loaders)

	retrieved := For[*testLoaders](ctx)
	require.NotNil(t, retrieved)
	assert.Equal(t, "test", retrieved.UserLoader)
}

func TestFor_NotFound(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	retrieved := For[*testLoaders](ctx)
	assert.Nil(t, retrieved)
}

func TestForOK(t *testing.T) {
	t.Parallel()

	t.Run("returns true when loaders present", func(t *testing.T) {
		t.Parallel()
		loaders := &testLoaders{UserLoader: "test"}
		ctx := WithLoaders(context.Background(), loaders)

		retrieved, ok := ForOK[*testLoaders](ctx)
		assert.True(t, ok)
		require.NotNil(t, retrieved)
		assert.Equal(t, "test", retrieved.UserLoader)
	})

	t.Run("returns false when loaders absent", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()

		retrieved, ok := ForOK[*testLoaders](ctx)
		assert.False(t, ok)
		assert.Nil(t, retrieved)
	})
}

// =============================================================================
// BatchResult Tests
// =============================================================================

func TestNewBatchResult(t *testing.T) {
	t.Parallel()

	t.Run("with value", func(t *testing.T) {
		result := NewBatchResult(&mockEntity{ID: 1}, nil)
		assert.Equal(t, 1, result.Value.ID)
		assert.NoError(t, result.Error)
	})

	t.Run("with error", func(t *testing.T) {
		result := NewBatchResult[*mockEntity](nil, ErrNotFound)
		assert.Nil(t, result.Value)
		assert.ErrorIs(t, result.Error, ErrNotFound)
	})
}

func TestResults(t *testing.T) {
	t.Parallel()

	t.Run("converts values and errors", func(t *testing.T) {
		values := []*mockEntity{{ID: 1}, nil, {ID: 3}}
		errs := []error{nil, ErrNotFound, nil}

		results := Results(values, errs)

		require.Len(t, results, 3)
		assert.Equal(t, 1, results[0].Value.ID)
		assert.NoError(t, results[0].Error)
		assert.Nil(t, results[1].Value)
		assert.ErrorIs(t, results[1].Error, ErrNotFound)
		assert.Equal(t, 3, results[2].Value.ID)
		assert.NoError(t, results[2].Error)
	})

	t.Run("handles fewer errors than values", func(t *testing.T) {
		values := []*mockEntity{{ID: 1}, {ID: 2}, {ID: 3}}
		errs := []error{nil}

		results := Results(values, errs)

		require.Len(t, results, 3)
		assert.NoError(t, results[0].Error)
		assert.NoError(t, results[1].Error)
		assert.NoError(t, results[2].Error)
	})

	t.Run("handles more errors than values", func(t *testing.T) {
		values := []*mockEntity{{ID: 1}}
		errs := []error{nil, ErrNotFound, ErrNotFound}

		results := Results(values, errs)

		require.Len(t, results, 3)
		assert.Equal(t, 1, results[0].Value.ID)
		assert.NoError(t, results[0].Error)
		assert.Nil(t, results[1].Value)
		assert.ErrorIs(t, results[1].Error, ErrNotFound)
		assert.Nil(t, results[2].Value)
		assert.ErrorIs(t, results[2].Error, ErrNotFound)
	})

	t.Run("empty input", func(t *testing.T) {
		results := Results([]*mockEntity{}, []error{})
		assert.Empty(t, results)
	})
}

// =============================================================================
// Edge Case Tests
// =============================================================================

func TestOrderByKeys_NilValues(t *testing.T) {
	t.Parallel()

	keyFn := func(e *mockEntity) int { return e.ID }

	t.Run("nil keys slice", func(t *testing.T) {
		t.Parallel()
		var nilKeys []int
		result, errs := OrderByKeys(nilKeys, nil, keyFn)
		assert.Empty(t, result)
		assert.Empty(t, errs)
	})

	t.Run("nil values with non-nil keys", func(t *testing.T) {
		t.Parallel()
		keys := []int{1, 2}
		result, errs := OrderByKeys(keys, nil, keyFn)
		require.Len(t, result, 2)
		require.Len(t, errs, 2)
		assert.Nil(t, result[0])
		assert.Nil(t, result[1])
		assert.ErrorIs(t, errs[0], ErrNotFound)
		assert.ErrorIs(t, errs[1], ErrNotFound)
	})
}

func TestOrderByKeys_StringKeys(t *testing.T) {
	t.Parallel()

	type item struct {
		Slug string
		Name string
	}

	keyFn := func(i *item) string { return i.Slug }
	keys := []string{"beta", "alpha", "gamma"}
	values := []*item{
		{Slug: "gamma", Name: "Gamma"},
		{Slug: "alpha", Name: "Alpha"},
		{Slug: "beta", Name: "Beta"},
	}

	result, errs := OrderByKeys(keys, values, keyFn)

	require.Len(t, result, 3)
	assert.Equal(t, "Beta", result[0].Name)
	assert.Equal(t, "Alpha", result[1].Name)
	assert.Equal(t, "Gamma", result[2].Name)
	for _, err := range errs {
		assert.NoError(t, err)
	}
}

func TestOrderByKeys_ValueTypes(t *testing.T) {
	t.Parallel()

	// Test with non-pointer value types (zero value instead of nil for missing)
	type record struct {
		ID   int
		Data string
	}

	keyFn := func(r record) int { return r.ID }
	keys := []int{1, 2, 3}
	values := []record{
		{ID: 1, Data: "one"},
		{ID: 3, Data: "three"},
	}

	result, errs := OrderByKeys(keys, values, keyFn)

	require.Len(t, result, 3)
	assert.Equal(t, "one", result[0].Data)
	assert.Equal(t, "", result[1].Data) // zero value, not nil
	assert.Equal(t, "three", result[2].Data)
	assert.ErrorIs(t, errs[1], ErrNotFound)
}

func TestGroupByKey_NilInput(t *testing.T) {
	t.Parallel()

	type post struct {
		ID     int
		UserID int
	}
	keyFn := func(p *post) int { return p.UserID }

	var nilPosts []*post
	grouped := GroupByKey(nilPosts, keyFn)
	assert.Empty(t, grouped)
}

func TestGroupByKey_DuplicateValues(t *testing.T) {
	t.Parallel()

	// When multiple values have the same key, all should be in the group
	type item struct {
		Name   string
		Bucket string
	}
	keyFn := func(i *item) string { return i.Bucket }

	values := []*item{
		{Name: "a", Bucket: "x"},
		{Name: "b", Bucket: "x"},
		{Name: "c", Bucket: "y"},
		{Name: "d", Bucket: "x"},
	}

	grouped := GroupByKey(values, keyFn)

	require.Len(t, grouped["x"], 3)
	require.Len(t, grouped["y"], 1)
	assert.Equal(t, "a", grouped["x"][0].Name)
	assert.Equal(t, "b", grouped["x"][1].Name)
	assert.Equal(t, "d", grouped["x"][2].Name)
}

func TestOrderGroupsByKeys_NilGroups(t *testing.T) {
	t.Parallel()

	keys := []int{1, 2, 3}
	var nilGroups map[int][]string
	result := OrderGroupsByKeys(keys, nilGroups)

	require.Len(t, result, 3)
	for _, group := range result {
		assert.Nil(t, group)
	}
}

func TestPrimeMany_EmptyValues(t *testing.T) {
	t.Parallel()

	cache := newMockCache[int, *mockEntity]()
	PrimeMany(cache, []*mockEntity{}, func(e *mockEntity) int { return e.ID })
	assert.Empty(t, cache.data)
}

func TestPrimeMany_NilValues(t *testing.T) {
	t.Parallel()

	cache := newMockCache[int, *mockEntity]()
	PrimeMany(cache, nil, func(e *mockEntity) int { return e.ID })
	assert.Empty(t, cache.data)
}

func TestClearMany_EmptyKeys(t *testing.T) {
	t.Parallel()

	cache := newMockCache[int, *mockEntity]()
	cache.data[1] = &mockEntity{ID: 1}

	ClearMany(cache, []int{})
	assert.Len(t, cache.data, 1) // unchanged
	assert.Empty(t, cache.cleared)
}

func TestClearMany_NilKeys(t *testing.T) {
	t.Parallel()

	cache := newMockCache[int, *mockEntity]()
	cache.data[1] = &mockEntity{ID: 1}

	ClearMany(cache, nil)
	assert.Len(t, cache.data, 1) // unchanged
}

func TestResults_NilInputs(t *testing.T) {
	t.Parallel()

	var nilValues []*mockEntity
	results := Results(nilValues, nil)
	assert.Empty(t, results)
}

func TestWithLoaders_OverwritesPrevious(t *testing.T) {
	t.Parallel()

	first := &testLoaders{UserLoader: "first"}
	second := &testLoaders{UserLoader: "second"}

	ctx := WithLoaders(context.Background(), first)
	ctx = WithLoaders(ctx, second)

	retrieved := For[*testLoaders](ctx)
	require.NotNil(t, retrieved)
	assert.Equal(t, "second", retrieved.UserLoader)
}

func TestFor_WrongType(t *testing.T) {
	t.Parallel()

	// Store one type, retrieve another
	ctx := WithLoaders(context.Background(), &testLoaders{UserLoader: "test"})

	type otherLoaders struct{ Name string }
	retrieved := For[*otherLoaders](ctx)
	assert.Nil(t, retrieved)
}

// =============================================================================
// Benchmarks
// =============================================================================

func BenchmarkOrderByKeys(b *testing.B) {
	keyFn := func(e *mockEntity) int { return e.ID }

	// Create test data
	keys := make([]int, 100)
	values := make([]*mockEntity, 100)
	for i := range 100 {
		keys[i] = i
		values[i] = &mockEntity{ID: i, Name: "entity"}
	}

	b.ResetTimer()
	for b.Loop() {
		OrderByKeys(keys, values, keyFn)
	}
}

func BenchmarkGroupByKey(b *testing.B) {
	type post struct {
		ID     int
		UserID int
	}
	keyFn := func(p *post) int { return p.UserID }

	// Create test data with 100 posts across 10 users
	posts := make([]*post, 100)
	for i := range 100 {
		posts[i] = &post{ID: i, UserID: i % 10}
	}

	b.ResetTimer()
	for b.Loop() {
		GroupByKey(posts, keyFn)
	}
}
