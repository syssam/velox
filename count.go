package velox

import (
	"context"

	"github.com/syssam/velox/internal/engine"
)

// StatementCounter counts the statements that run with a context
// CountStatements returned, on a DB opened with WithStatementCounting, by
// shape: the statement's text without its comment and arguments, the key
// of the render cache. It is safe for concurrent use. Total is the number
// of statements counted and Shapes each shape, in the order each first
// ran. It is for tests and development: Check finds a read repeated once
// per row of another, an N+1.
//
//	ctx, n := velox.CountStatements(ctx)
//	handler(ctx)
//	if err := n.Check(velox.StatementBudget{Repeats: 1}); err != nil {
//		t.Error(err)
//	}
type StatementCounter = engine.StatementCounter

// StatementShape is one shape of statement a StatementCounter saw: SQL, its
// text the first time; Entity, the entity it is about ("" for raw SQL); Op;
// and Count, how many times it ran.
type StatementShape = engine.StatementShape

// StatementBudget is what a context may run, checked by
// StatementCounter.Check: at most Statements statements, and at most Repeats
// runs of one shape of read. A zero field sets no limit.
type StatementBudget = engine.StatementBudget

// BudgetError is StatementCounter.Check's error: Total, the statements
// counted; Budget; and Repeats, each read that ran more than the budget's
// Repeats times.
type BudgetError = engine.BudgetError

// CountStatements returns ctx with a new StatementCounter, which counts the
// statements that run with the returned context, or one derived from it,
// on a DB opened with WithStatementCounting.
func CountStatements(ctx context.Context) (context.Context, *StatementCounter) {
	return engine.CountStatements(ctx)
}

// WithStatementCounting adds an Around hook that counts each statement in
// the StatementCounter of its context, in the place it is given among
// WithAround's. A DB without it counts nothing and pays nothing; with it, a
// statement whose context has no counter pays one context lookup.
// veloxtest.Open adds it.
func WithStatementCounting() Option { return engine.WithStatementCounting() }
