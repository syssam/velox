package op_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"velox.test/parity/op"
)

func TestProgram_FormatIsReplayable(t *testing.T) {
	prog := op.Program{
		op.CreateAuthor{Name: "Alice", Age: 30, Role: "user"},
		op.CreatePost{Title: "Hello", Status: "draft", AuthorRef: 0},
		op.AppendPostLabels{PostRef: 1, Labels: []string{"go"}},
	}
	got := op.Format(prog)
	assert.Contains(t, got, "0: CreateAuthor")
	assert.Contains(t, got, "1: CreatePost")
	assert.Contains(t, got, "AuthorRef:0")
	assert.Contains(t, got, "2: AppendPostLabels")
	assert.Len(t, prog, 3)
}
