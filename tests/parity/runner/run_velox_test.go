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

func crudJSONProg() op.Program {
	return op.Program{
		op.CreateAuthor{Name: "Alice", Age: 30, Role: "user"},                                           // 0
		op.CreatePost{Title: "T1", Status: "draft", ViewCount: 5, AuthorRef: 0, Labels: []string{"go"}}, // 1
		op.AppendPostLabels{PostRef: 1, Labels: []string{"orm"}},                                        // 2
		op.UpdatePostViewCount{PostRef: 1, ViewCount: 9},                                                // 3
		op.QueryPostsByStatus{Status: "draft"},                                                          // 4
		op.SumViewCount{},                                                                               // 5
	}
}

func TestRunVelox_MatchesReference_CRUDJSON(t *testing.T) {
	prog := crudJSONProg()
	ref := runner.Reference(t, prog)
	vc := runner.NewVeloxSQLite(t)
	got, err := runner.RunVelox(context.Background(), vc, prog)
	require.NoError(t, err)
	d := compare.Diff(ref, got)
	assert.Empty(t, d, "velox must match the reference for CRUD+JSON: %v", d)
}
