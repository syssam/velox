package compare_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"velox.test/parity/compare"
	"velox.test/parity/model"
)

func row(id int, vc int) model.Row {
	return model.Row{"id": model.Ref{Handle: id}, "view_count": vc}
}

func TestDiff_EqualResultsNoDiff(t *testing.T) {
	a := []model.Result{{Rows: []model.Row{row(1, 5)}}}
	b := []model.Result{{Rows: []model.Row{row(1, 5)}}}
	assert.Empty(t, compare.Diff(a, b))
}

func TestDiff_FieldMismatchReported(t *testing.T) {
	a := []model.Result{{Rows: []model.Row{row(1, 5)}}}
	b := []model.Result{{Rows: []model.Row{row(1, 9)}}}
	d := compare.Diff(a, b)
	require.Len(t, d, 1)
	assert.Equal(t, 0, d[0].OpIndex)
	assert.Equal(t, "view_count", d[0].Field)
	assert.Equal(t, 5, d[0].A)
	assert.Equal(t, 9, d[0].B)
}

func TestDiff_ErrCategoryMismatchReported(t *testing.T) {
	a := []model.Result{{Err: compare.ErrOK}}
	b := []model.Result{{Err: compare.ErrNotFound}}
	d := compare.Diff(a, b)
	require.Len(t, d, 1)
	assert.Equal(t, "<error>", d[0].Field)
}
