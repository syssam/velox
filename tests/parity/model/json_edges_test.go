package model_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"velox.test/parity/model"
	"velox.test/parity/op"
)

func TestModel_JSONAppendConcatenates(t *testing.T) {
	prog := op.Program{
		op.CreateAuthor{Name: "A", Role: "user"},                                  // 0
		op.CreatePost{Title: "P", Status: "draft", AuthorRef: 0, Labels: []string{"go"}}, // 1
		op.AppendPostLabels{PostRef: 1, Labels: []string{"orm", "x"}},             // 2
		op.QueryPostsByStatus{Status: "draft"},                                    // 3
	}
	res, err := model.Run(prog)
	require.NoError(t, err)
	assert.Equal(t, model.Value([]string{"go", "orm", "x"}), res[3].Rows[0]["labels"])
}

func TestModel_SetLabelsOverwrites(t *testing.T) {
	prog := op.Program{
		op.CreateAuthor{Name: "A", Role: "user"},                                  // 0
		op.CreatePost{Title: "P", Status: "draft", AuthorRef: 0, Labels: []string{"go"}}, // 1
		op.AppendPostLabels{PostRef: 1, Labels: []string{"orm"}},                   // 2
		op.SetPostLabels{PostRef: 1, Labels: []string{"reset"}},                    // 3
		op.QueryPostsByStatus{Status: "draft"},                                    // 4
	}
	res, err := model.Run(prog)
	require.NoError(t, err)
	assert.Equal(t, model.Value([]string{"reset"}), res[4].Rows[0]["labels"])
}

func TestModel_SumViewCount(t *testing.T) {
	prog := op.Program{
		op.CreateAuthor{Name: "A", Role: "user"},                                   // 0
		op.CreatePost{Title: "P1", Status: "draft", ViewCount: 3, AuthorRef: 0},    // 1
		op.CreatePost{Title: "P2", Status: "draft", ViewCount: 4, AuthorRef: 0},    // 2
		op.SumViewCount{},                                                          // 3
	}
	res, err := model.Run(prog)
	require.NoError(t, err)
	assert.Equal(t, 7, *res[3].Scalar)
}

func TestModel_LoadAuthorPosts(t *testing.T) {
	prog := op.Program{
		op.CreateAuthor{Name: "A", Role: "user"},                                   // 0
		op.CreatePost{Title: "P1", Status: "draft", AuthorRef: 0},                  // 1
		op.CreatePost{Title: "P2", Status: "draft", AuthorRef: 0},                  // 2
		op.LoadAuthorPosts{AuthorRef: 0},                                           // 3
	}
	res, err := model.Run(prog)
	require.NoError(t, err)
	require.Len(t, res[3].Rows, 2)
	assert.Equal(t, model.Ref{Handle: 1}, refOf(res[3].Rows[0]))
	assert.Equal(t, model.Ref{Handle: 2}, refOf(res[3].Rows[1]))
}

// refOf returns a Ref to the row's own handle (the "id" normalized to a Ref).
func refOf(r model.Row) model.Value { return r["id"] }
