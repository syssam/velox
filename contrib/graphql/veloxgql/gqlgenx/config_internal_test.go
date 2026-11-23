package gqlgenx

import (
	"context"
	"testing"

	"github.com/vektah/gqlparser/v2/ast"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
)

// TestConfig: newConfig starts from veloxgql.DefaultLimits() and applies
// each option, the last of two that set one thing winning, fills a zero
// limit from the defaults, and panics on a zero Option.
func TestConfig(t *testing.T) {
	filter := func(context.Context, *ast.OperationDefinition) bool { return false }
	scoped := func(context.Context) any { return 1 }
	c := newConfig(WithLimits(veloxgql.Limits{MaxPage: 3}), WithTxOptions(velox.TxReadOnly()),
		WithTxFilter(filter), WithBatchScope(scoped), WithLimits(veloxgql.Limits{MaxPage: 4}))
	if c.limits.MaxPage != 4 || c.limits.MaxCost != veloxgql.DefaultLimits().MaxCost || len(c.txOptions) != 1 || c.txFilter == nil || c.batchScope == nil {
		t.Errorf("newConfig = %+v", c)
	}
	if d := newConfig(); d.limits != veloxgql.DefaultLimits() || d.txOptions != nil || d.txFilter != nil || d.batchScope != nil {
		t.Errorf("newConfig() = %+v", d)
	}
	defer func() {
		if recover() == nil {
			t.Error("newConfig(Option{}) did not panic")
		}
	}()
	newConfig(Option{})
}

// TestRunOptionsEndWithoutRetry: a mutation's transaction gets the options
// WithTxOptions gave, then TxRetrySerializable(0), which overrides a retry
// in the DB's velox.WithTxDefaults: gqlgen makes the response once.
func TestRunOptionsEndWithoutRetry(t *testing.T) {
	e := NewExtension(nil, WithTxOptions(velox.TxReadOnly()))
	if len(e.runOpts) != 2 || velox.TxRetries(e.runOpts...) != 0 {
		t.Errorf("run options %d, retries %d", len(e.runOpts), velox.TxRetries(e.runOpts...))
	}
	if len(e.txOpts) != 1 {
		t.Errorf("WithTxOptions' options changed: %d", len(e.txOpts))
	}
}
