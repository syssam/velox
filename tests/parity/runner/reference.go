// Package runner holds the executors that turn an op.Program into observed
// []model.Result. This file is the reference executor: it runs the ground-truth
// in-memory model. It imports NO ORM — the reference oracle must be independent
// of the implementations it judges. A3 adds run_velox.go / run_ent.go in this
// package (the only files allowed to import velox / ent, per the architecture
// guard test).
package runner

import (
	"testing"

	"github.com/stretchr/testify/require"
	"velox.test/parity/model"
	"velox.test/parity/op"
)

// Reference runs prog through the reference model and returns its results,
// failing the test on any harness-internal error.
func Reference(t testing.TB, prog op.Program) []model.Result {
	t.Helper()
	res, err := model.Run(prog)
	require.NoError(t, err)
	return res
}

// CloneResults deep-copies a result slice so that mutating the clone (rows,
// scalar, page) cannot touch the original. This is what lets the self-test
// perturb a copy of the reference results and prove the comparator reports the
// injected divergence.
func CloneResults(in []model.Result) []model.Result {
	if in == nil {
		return nil
	}
	out := make([]model.Result, len(in))
	for i, r := range in {
		out[i] = model.Result{
			Rows:   cloneRows(r.Rows),
			Scalar: cloneIntPtr(r.Scalar),
			Page:   clonePage(r.Page),
			Err:    r.Err,
		}
	}
	return out
}

func cloneRows(rows []model.Row) []model.Row {
	if rows == nil {
		return nil
	}
	out := make([]model.Row, len(rows))
	for i, row := range rows {
		nr := make(model.Row, len(row))
		for k, v := range row {
			nr[k] = cloneValue(v)
		}
		out[i] = nr
	}
	return out
}

// cloneValue deep-copies a Value. Scalars and Ref are value types (copied by
// assignment); []string is copied into a fresh backing array so mutations to
// the clone's slice do not alias the original.
func cloneValue(v model.Value) model.Value {
	if s, ok := v.([]string); ok {
		cp := make([]string, len(s))
		copy(cp, s)
		return cp
	}
	return v
}

func clonePage(p *model.PageInfo) *model.PageInfo {
	if p == nil {
		return nil
	}
	return &model.PageInfo{
		HasNext:     p.HasNext,
		HasPrev:     p.HasPrev,
		StartHandle: cloneIntPtr(p.StartHandle),
		EndHandle:   cloneIntPtr(p.EndHandle),
	}
}

func cloneIntPtr(p *int) *int {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
