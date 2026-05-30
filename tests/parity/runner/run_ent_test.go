package runner_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"velox.test/parity/compare"
	"velox.test/parity/op"
	"velox.test/parity/runner"
)

// crudEntProg mirrors crudJSONProg but uses SetPostLabels (JSON replace) instead
// of AppendPostLabels. Ent's generated JSON-array APPEND is broken on SQLite: it
// runs JSON_INSERT(labels, '$[#]', ?), which SQLite rejects as "malformed JSON"
// on the blob-stored JSON value (velox CASTs to TEXT first, then json_each, and
// works). The labels column is declared json in both schemas; the value is
// blob-stored because of the []byte bind param, not a column-type difference.
// That genuine EntDivergent is surfaced by the curated three-way suite
// (TestCuratedSuite_SQLite::json_append), not silenced. This executor sanity
// test exercises the rest of ent's CRUD+JSON-replace path, which ent handles
// correctly, so it validates the ent executor wiring without tripping over
// ent's own SQLite bug.
func crudEntProg() op.Program {
	return op.Program{
		op.CreateAuthor{Name: "Alice", Age: 30, Role: "user"},                                           // 0
		op.CreatePost{Title: "T1", Status: "draft", ViewCount: 5, AuthorRef: 0, Labels: []string{"go"}}, // 1
		op.SetPostLabels{PostRef: 1, Labels: []string{"go", "orm"}},                                     // 2 (replace, not append)
		op.UpdatePostViewCount{PostRef: 1, ViewCount: 9},                                                // 3
		op.QueryPostsByStatus{Status: "draft"},                                                          // 4
		op.SumViewCount{},                                                                               // 5
	}
}

func TestRunEnt_MatchesReference_CRUDJSON(t *testing.T) {
	prog := crudEntProg()
	ref := runner.Reference(t, prog)
	ec := runner.NewEntSQLite(t)
	got, err := runner.RunEnt(context.Background(), ec, prog)
	require.NoError(t, err)
	assert.Empty(t, compare.Diff(ref, got), "ent must match the reference for CRUD+JSON(replace)")
}

func TestRunEnt_MatchesReference_Pagination(t *testing.T) {
	prog := paginateProg()
	ref := runner.Reference(t, prog)
	ec := runner.NewEntSQLite(t)
	got, err := runner.RunEnt(context.Background(), ec, prog)
	require.NoError(t, err)
	assert.Empty(t, compare.Diff(ref, got), "ent pagination must match the reference")
}
