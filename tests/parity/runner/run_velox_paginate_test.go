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

func intp(i int) *int { return &i }

func paginateProg() op.Program {
	prog := op.Program{op.CreateAuthor{Name: "A", Role: "user"}}
	for i := 1; i <= 6; i++ {
		prog = append(prog, op.CreatePost{Title: "P", Status: "draft", ViewCount: i, AuthorRef: 0})
	}
	order := []op.OrderTerm{{Field: "view_count", Desc: false}}
	prog = append(prog, op.PaginatePosts{First: intp(4), OrderBy: order})                    // forward
	prog = append(prog, op.PaginatePosts{Last: intp(2), BeforeRef: intp(4), OrderBy: order}) // backward
	return prog
}

func TestRunVelox_MatchesReference_Pagination(t *testing.T) {
	prog := paginateProg()
	ref := runner.Reference(t, prog)
	vc := runner.NewVeloxSQLite(t)
	got, err := runner.RunVelox(context.Background(), vc, prog)
	require.NoError(t, err)
	assert.Empty(t, compare.Diff(ref, got), "velox pagination must match the reference")
}
