package velox

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

// countlessConn is a sqlConn whose statements run but whose results cannot
// report their count.
type countlessConn struct{ sqlConn }

func (countlessConn) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return countlessResult{}, nil
}

type countlessResult struct{}

func (countlessResult) LastInsertId() (int64, error) { return 0, errors.New("no id") }
func (countlessResult) RowsAffected() (int64, error) { return 0, errors.New("no count") }

// TestRowsAffectedErrorIsUnknown: a statement that ran but whose driver
// could not count its rows is not failed: its count is -1. A
// policy-conditioned upsert without the count fails closed.
func TestRowsAffectedErrorIsUnknown(t *testing.T) {
	r, err := sqlExecutor{c: countlessConn{}}.Exec(context.Background(), "UPDATE t SET a = 1", nil)
	if err != nil || r.Affected != -1 {
		t.Errorf("affected %d, error %v; want -1 and no error", r.Affected, err)
	}
	st := &Statement{Op: OpInsert, Entity: invInfo, Rows: [][]any{{1}}, Conflict: &ConflictClause{}, upsertPolicy: true}
	var pe *PolicyError
	if err := checkUpserted(st, -1); !errors.As(err, &pe) || !errors.Is(err, errUnknownAffected) {
		t.Errorf("checkUpserted with -1: %v, want a *PolicyError wrapping errUnknownAffected", err)
	}
}
