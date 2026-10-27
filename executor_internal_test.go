package velox

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestPgxTxOptions(t *testing.T) {
	// A transaction that is not read-only must leave the access mode to the
	// server, so a role's default_transaction_read_only is not overridden.
	if got := pgxTxOptions(txOptions{}); got != (pgx.TxOptions{}) {
		t.Errorf("default options %+v", got)
	}
	if got := pgxTxOptions(txOptions{readOnly: true, isolation: sql.LevelSerializable}); got.AccessMode != pgx.ReadOnly || got.IsoLevel != pgx.Serializable {
		t.Errorf("read-only serializable %+v", got)
	}
	if got := pgxTxOptions(txOptions{isolation: sql.LevelReadCommitted}); got.AccessMode != "" || got.IsoLevel != pgx.ReadCommitted {
		t.Errorf("read committed %+v", got)
	}
}

func TestPgxIsolation(t *testing.T) {
	for l, want := range map[sql.IsolationLevel]pgx.TxIsoLevel{
		sql.LevelDefault:         "",
		sql.LevelReadUncommitted: pgx.ReadUncommitted,
		sql.LevelReadCommitted:   pgx.ReadCommitted,
		sql.LevelRepeatableRead:  pgx.RepeatableRead,
		sql.LevelSnapshot:        pgx.RepeatableRead,
		sql.LevelSerializable:    pgx.Serializable,
		sql.LevelLinearizable:    pgx.Serializable,
		sql.LevelWriteCommitted:  "",
	} {
		if got := pgxIsolation(l); got != want {
			t.Errorf("%v: %q, want %q", l, got, want)
		}
	}
}

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
	r, err := sqlExecutor{c: countlessConn{}}.exec(context.Background(), "UPDATE t SET a = 1", nil)
	if err != nil || r.affected != -1 {
		t.Errorf("affected %d, error %v; want -1 and no error", r.affected, err)
	}
	st := &Statement{Op: OpInsert, Entity: invInfo, Rows: [][]any{{1}}, Conflict: &ConflictClause{}, upsertPolicy: true}
	var pe *PolicyError
	if err := checkUpserted(st, -1); !errors.As(err, &pe) || !errors.Is(err, errUnknownAffected) {
		t.Errorf("checkUpserted with -1: %v, want a *PolicyError wrapping errUnknownAffected", err)
	}
}
