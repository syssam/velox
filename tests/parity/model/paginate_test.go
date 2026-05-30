package model_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"velox.test/parity/model"
	"velox.test/parity/op"
)

func intp(i int) *int { return &i }

// seed makes 1 author + n draft posts (handles 1..n) with view_count = i.
func seed(n int) op.Program {
	prog := op.Program{op.CreateAuthor{Name: "A", Role: "user"}}
	for i := 1; i <= n; i++ {
		prog = append(prog, op.CreatePost{Title: "P", Status: "draft", ViewCount: i, AuthorRef: 0})
	}
	return prog
}

func handles(r model.Result) []int {
	out := make([]int, len(r.Rows))
	for i, row := range r.Rows {
		out[i] = row["id"].(model.Ref).Handle
	}
	return out
}

func TestModel_Paginate_ForwardDefault(t *testing.T) {
	prog := append(seed(6), op.PaginatePosts{First: intp(3)}) // index 7
	res, err := model.Run(prog)
	require.NoError(t, err)
	p := res[len(res)-1]
	assert.Equal(t, []int{1, 2, 3}, handles(p))
	require.NotNil(t, p.Page)
	assert.True(t, p.Page.HasNext)
	assert.False(t, p.Page.HasPrev)
}

// The real velox bug: multi-order BACKWARD (before-cursor) must return the rows
// BEFORE the cursor, mirrored, not the rows after it.
func TestModel_Paginate_MultiOrderBackward(t *testing.T) {
	// 6 posts, view_count 1..6. Order by view_count ASC (+ id tiebreak).
	prog := seed(6)
	order := []op.OrderTerm{{Field: "view_count", Desc: false}}
	// Forward first=4 -> handles 1,2,3,4; take the cursor at handle 4.
	prog = append(prog, op.PaginatePosts{First: intp(4), OrderBy: order}) // idx 7
	// Backward before=cursor(handle 4), last=2 -> handles 2,3.
	prog = append(prog, op.PaginatePosts{Last: intp(2), BeforeRef: intp(4), OrderBy: order}) // idx 8
	res, err := model.Run(prog)
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2, 3, 4}, handles(res[7]))
	assert.Equal(t, []int{2, 3}, handles(res[8]),
		"backward must return rows BEFORE the cursor (mirror), not after")
	assert.True(t, res[8].Page.HasPrev)
}
