package gqlrelay

import (
	"cmp"
	"fmt"
	"slices"
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

	// An edge already in ID order skips the sort but is still copied: the
	// page must never alias the loaded edge.
	inOrder := []int{1, 2, 3}
	page, err := PageLoaded(inOrder, cmp.Compare[int], nil, nil)
	require.NoError(t, err)
	page.Nodes[0] = 99
	assert.Equal(t, []int{1, 2, 3}, inOrder, "the page must not share the loaded edge's backing array")
}

type pageLoadedNode struct{ ID int }

func cmpPageLoadedNode(a, b *pageLoadedNode) int { return cmp.Compare(a.ID, b.ID) }

// BenchmarkPageLoaded measures the fast-path page cut over an eager-loaded
// edge. An eager load usually comes back in ID order already ("Sorted"), so
// that case must cost exactly one allocation: the copy.
func BenchmarkPageLoaded(b *testing.B) {
	for _, n := range []int{10, 100} {
		sorted := make([]*pageLoadedNode, n)
		for i := range sorted {
			sorted[i] = &pageLoadedNode{ID: i + 1}
		}
		reversed := slices.Clone(sorted)
		slices.Reverse(reversed)
		first := 5
		for _, c := range []struct {
			name  string
			nodes []*pageLoadedNode
		}{{"Sorted", sorted}, {"Reversed", reversed}} {
			b.Run(fmt.Sprintf("%s/%d", c.name, n), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, err := PageLoaded(c.nodes, cmpPageLoadedNode, &first, nil); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
