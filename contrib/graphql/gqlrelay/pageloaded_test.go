package gqlrelay

import (
	"cmp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPageLoaded(t *testing.T) {
	loaded := []int{3, 1, 2} // an eager load comes back in no particular order
	ptr := func(n int) *int { return &n }

	all, err := PageLoaded(loaded, cmp.Compare[int], nil, nil)
	require.NoError(t, err)
	assert.Equal(t, LoadedPage[int]{Nodes: []int{1, 2, 3}, TotalCount: 3}, all)
	assert.Equal(t, []int{3, 1, 2}, loaded, "the loaded edge must not be reordered in place")

	first, err := PageLoaded(loaded, cmp.Compare[int], ptr(2), nil)
	require.NoError(t, err)
	assert.Equal(t, LoadedPage[int]{Nodes: []int{1, 2}, TotalCount: 3, HasNextPage: true}, first)

	// last keeps ascending order, like Paginate, and counts the whole edge.
	last, err := PageLoaded(loaded, cmp.Compare[int], nil, ptr(2))
	require.NoError(t, err)
	assert.Equal(t, LoadedPage[int]{Nodes: []int{2, 3}, TotalCount: 3, HasPreviousPage: true}, last)

	exact, err := PageLoaded(loaded, cmp.Compare[int], ptr(3), nil)
	require.NoError(t, err)
	assert.False(t, exact.HasNextPage)

	_, err = PageLoaded(loaded, cmp.Compare[int], ptr(1), ptr(1))
	require.ErrorIs(t, err, ErrInvalidPagination, "first and last together are rejected like Paginate does")
}
