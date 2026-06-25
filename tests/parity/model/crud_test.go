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
		op.CreateAuthor{Name: "Alice", Age: 30, Role: "user"},                   // handle 0
		op.CreatePost{Title: "T1", Status: "draft", ViewCount: 5, AuthorRef: 0}, // handle 1
		op.UpdatePostViewCount{PostRef: 1, ViewCount: 9},                        // handle 2
		op.QueryPostsByStatus{Status: "draft"},                                  // handle 3
		op.DeletePost{PostRef: 1},                                               // handle 4
		op.CountPosts{},                                                         // handle 5
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

func TestModel_UpsertTag_InsertThenIncrement(t *testing.T) {
	prog := op.Program{
		op.UpsertTag{Name: "U1", AddUsage: 5}, // 0: insert    -> usage_count 5
		op.UpsertTag{Name: "U1", AddUsage: 3}, // 1: conflict  -> usage_count 8
		op.UpsertTag{Name: "U2", AddUsage: 7}, // 2: insert    -> usage_count 7
		op.UpsertTag{Name: "U1", AddUsage: 1}, // 3: conflict  -> usage_count 9
	}
	res, err := model.Run(prog)
	require.NoError(t, err)
	require.Len(t, res, 4)

	require.Equal(t, compare.ErrOK, res[0].Err)
	assert.Equal(t, 5, *res[0].Scalar, "fresh upsert seeds usage_count with AddUsage")
	assert.Equal(t, 8, *res[1].Scalar, "conflicting upsert adds onto the existing usage_count")
	assert.Equal(t, 7, *res[2].Scalar, "a different name is an independent insert")
	assert.Equal(t, 9, *res[3].Scalar, "second conflict accumulates again")
}

func TestModel_BulkCreateTags_SumUsage(t *testing.T) {
	prog := op.Program{
		op.BulkCreateTags{Specs: []op.TagSpec{ // 0: one batch INSERT of 3 rows
			{Name: "b0", UsageCount: 3},
			{Name: "b1", UsageCount: 5},
			{Name: "b2", UsageCount: 7},
		}},
		op.SumTagUsage{},                       // 1 -> 3+5+7 = 15
		op.UpsertTag{Name: "b0", AddUsage: 10}, // 2 -> b0 becomes 13
		op.SumTagUsage{},                       // 3 -> 25
		op.CreateTag{Name: "c"},                // 4 -> a 0-usage row
		op.SumTagUsage{},                       // 5 -> still 25
	}
	res, err := model.Run(prog)
	require.NoError(t, err)
	require.Len(t, res, 6)
	assert.Equal(t, 15, *res[1].Scalar, "sum of the bulk-created usage_counts")
	assert.Equal(t, 25, *res[3].Scalar, "an upsert increment on a bulk row is reflected in the sum")
	assert.Equal(t, 25, *res[5].Scalar, "a plain CreateTag adds a 0-usage row")
}

func TestModel_M2MEdge_AddRemoveCount(t *testing.T) {
	prog := op.Program{
		op.CreateAuthor{Name: "A", Role: "user"},                 // 0
		op.CreatePost{Title: "P", Status: "draft", AuthorRef: 0}, // 1
		op.CreateTag{Name: "t0"},                                 // 2
		op.CreateTag{Name: "t1"},                                 // 3
		op.CreateTag{Name: "t2"},                                 // 4
		op.AddTagToPost{PostRef: 1, TagRef: 2},                   // 5
		op.AddTagToPost{PostRef: 1, TagRef: 3},                   // 6
		op.AddTagToPost{PostRef: 1, TagRef: 4},                   // 7
		op.CountPostTags{PostRef: 1},                             // 8 -> 3
		op.RemoveTagFromPost{PostRef: 1, TagRef: 3},              // 9
		op.CountPostTags{PostRef: 1},                             // 10 -> 2
		op.RemoveTagFromPost{PostRef: 1, TagRef: 3},              // 11 -> no-op (already gone)
		op.CountPostTags{PostRef: 1},                             // 12 -> 2
	}
	res, err := model.Run(prog)
	require.NoError(t, err)
	require.Len(t, res, 13)
	assert.Equal(t, 3, *res[8].Scalar, "three tags attached")
	require.Equal(t, compare.ErrOK, res[9].Err)
	assert.Equal(t, 2, *res[10].Scalar, "one tag removed")
	require.Equal(t, compare.ErrOK, res[11].Err, "removing an already-detached tag is a no-op, not an error")
	assert.Equal(t, 2, *res[12].Scalar, "repeated remove leaves the degree unchanged")
}

func TestModel_NillableField_SetClearCount(t *testing.T) {
	bio := "hello"
	prog := op.Program{
		op.CreateAuthor{Name: "A", Role: "user", Bio: &bio}, // 0: bio set
		op.CreateAuthor{Name: "B", Role: "user"},            // 1: bio NULL
		op.CountAuthorsWithBio{},                            // 2 -> 1
		op.SetAuthorBio{AuthorRef: 1, Bio: &bio},            // 3: set B's bio
		op.CountAuthorsWithBio{},                            // 4 -> 2
		op.SetAuthorBio{AuthorRef: 0, Bio: nil},             // 5: CLEAR A's bio -> NULL
		op.CountAuthorsWithBio{},                            // 6 -> 1
	}
	res, err := model.Run(prog)
	require.NoError(t, err)
	require.Len(t, res, 7)
	assert.Equal(t, 1, *res[2].Scalar, "one author created with a bio")
	assert.Equal(t, 2, *res[4].Scalar, "setting a NULL bio makes it non-NULL")
	assert.Equal(t, 1, *res[6].Scalar, "clearing a bio returns it to NULL")
}

func TestModel_MultiRow_UpdateDeleteByPredicate(t *testing.T) {
	prog := op.Program{
		op.CreateAuthor{Name: "A", Role: "user"},                                     // 0
		op.CreatePost{Title: "d0", Status: "draft", ViewCount: 1, AuthorRef: 0},      // 1
		op.CreatePost{Title: "p0", Status: "published", ViewCount: 10, AuthorRef: 0}, // 2
		op.CreatePost{Title: "d1", Status: "draft", ViewCount: 2, AuthorRef: 0},      // 3
		op.BulkAddViewCountByStatus{Status: "draft", Delta: 100},                     // 4 -> 2 affected
		op.SumViewCount{}, // 5 -> 101+102+10 = 213
		op.BulkDeletePostsByStatus{Status: "draft"}, // 6 -> 2 deleted
		op.CountPosts{},   // 7 -> 1
		op.SumViewCount{}, // 8 -> 10
	}
	res, err := model.Run(prog)
	require.NoError(t, err)
	require.Len(t, res, 9)
	assert.Equal(t, 2, *res[4].Scalar, "two draft posts updated")
	assert.Equal(t, 213, *res[5].Scalar, "view_counts incremented on matched rows only")
	assert.Equal(t, 2, *res[6].Scalar, "two draft posts deleted")
	assert.Equal(t, 1, *res[7].Scalar, "one published post remains")
	assert.Equal(t, 10, *res[8].Scalar, "remaining post's view_count untouched")
}

func TestModel_DeleteThenUpdateIsNotFound(t *testing.T) {
	prog := op.Program{
		op.CreateAuthor{Name: "A", Role: "user"},                 // 0
		op.CreatePost{Title: "P", Status: "draft", AuthorRef: 0}, // 1
		op.DeletePost{PostRef: 1},                                // 2
		op.UpdatePostViewCount{PostRef: 1, ViewCount: 1},         // 3 -> not found
	}
	res, err := model.Run(prog)
	require.NoError(t, err)
	assert.Equal(t, compare.ErrNotFound, res[3].Err)
}
