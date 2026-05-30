package model_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"velox.test/parity/compare"
	"velox.test/parity/model"
	"velox.test/parity/op"
)

func TestModel_CRUDRoundTrip(t *testing.T) {
	prog := op.Program{
		op.CreateAuthor{Name: "Alice", Age: 30, Role: "user"},                  // handle 0
		op.CreatePost{Title: "T1", Status: "draft", ViewCount: 5, AuthorRef: 0}, // handle 1
		op.UpdatePostViewCount{PostRef: 1, ViewCount: 9},                       // handle 2
		op.QueryPostsByStatus{Status: "draft"},                                 // handle 3
		op.DeletePost{PostRef: 1},                                              // handle 4
		op.CountPosts{},                                                        // handle 5
	}
	res, err := model.Run(prog)
	require.NoError(t, err)
	require.Len(t, res, 6)

	// Query at index 3 returns the one draft post (handle 1) with updated view_count.
	q := res[3]
	require.Len(t, q.Rows, 1)
	assert.Equal(t, model.Value(9), q.Rows[0]["view_count"])
	assert.Equal(t, model.Value("T1"), q.Rows[0]["title"])
	assert.Equal(t, model.Ref{Handle: 0}, q.Rows[0]["author"])

	// Count after delete is 0.
	assert.Equal(t, 0, *res[5].Scalar)
	assert.Equal(t, compare.ErrOK, res[3].Err)
}

func TestModel_DeleteThenUpdateIsNotFound(t *testing.T) {
	prog := op.Program{
		op.CreateAuthor{Name: "A", Role: "user"},                  // 0
		op.CreatePost{Title: "P", Status: "draft", AuthorRef: 0},  // 1
		op.DeletePost{PostRef: 1},                                 // 2
		op.UpdatePostViewCount{PostRef: 1, ViewCount: 1},          // 3 -> not found
	}
	res, err := model.Run(prog)
	require.NoError(t, err)
	assert.Equal(t, compare.ErrNotFound, res[3].Err)
}
