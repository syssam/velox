package runner_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"velox.test/parity/op"
	"velox.test/parity/runner"
)

// crudNoJSONProg is a pure-CRUD program (no JSON append) so it must all-pass on
// every backend — json_append is the only case where ent's verdict is allowed to
// differ by dialect, and it is deliberately excluded here.
func crudNoJSONProg() op.Program {
	return op.Program{
		op.CreateAuthor{Name: "Alice", Age: 30, Role: "user"},
		op.CreatePost{Title: "T", Status: "draft", ViewCount: 5, AuthorRef: 0},
		op.UpdatePostViewCount{PostRef: 1, ViewCount: 9},
		op.QueryPostsByStatus{Status: "draft"},
		op.SumViewCount{},
		op.CountPosts{},
	}
}

func TestDriver_Postgres_CRUDAllPass(t *testing.T) {
	if !runner.HasBackend(runner.Postgres) {
		t.Skip("postgres not configured")
	}
	rep := runner.RunParity(t, runner.Postgres, crudNoJSONProg())
	assert.True(t, rep.AllPass(), "%s", rep)
}

func TestDriver_MySQL_CRUDAllPass(t *testing.T) {
	if !runner.HasBackend(runner.MySQL) {
		t.Skip("mysql not configured")
	}
	rep := runner.RunParity(t, runner.MySQL, crudNoJSONProg())
	assert.True(t, rep.AllPass(), "%s", rep)
}
