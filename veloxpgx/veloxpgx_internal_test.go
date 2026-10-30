package veloxpgx

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/syssam/velox2/internal/conn"
)

func TestTxOptions(t *testing.T) {
	// A transaction that is not read-only must leave the access mode to the
	// server, so a role's default_transaction_read_only is not overridden.
	if got := txOptions(sql.TxOptions{}); got != (pgx.TxOptions{}) {
		t.Errorf("default options %+v", got)
	}
	if got := txOptions(sql.TxOptions{ReadOnly: true, Isolation: sql.LevelSerializable}); got.AccessMode != pgx.ReadOnly || got.IsoLevel != pgx.Serializable {
		t.Errorf("read-only serializable %+v", got)
	}
	if got := txOptions(sql.TxOptions{Isolation: sql.LevelReadCommitted}); got.AccessMode != "" || got.IsoLevel != pgx.ReadCommitted {
		t.Errorf("read committed %+v", got)
	}
}

func TestIsolation(t *testing.T) {
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
		if got := isolation(l); got != want {
			t.Errorf("%v: %q, want %q", l, got, want)
		}
	}
}

type fakeRows struct{ pgx.Rows }

// fakeConn is a pool and a transaction both: it records what it is asked and
// returns its err, or a command tag of 3 rows.
type fakeConn struct {
	pgx.Tx
	err  error
	last string
	opts pgx.TxOptions
	done []string
}

func (f *fakeConn) Query(_ context.Context, s string, _ ...any) (pgx.Rows, error) {
	f.last = s
	if f.err != nil {
		return nil, f.err
	}
	return fakeRows{}, nil
}

func (f *fakeConn) Exec(_ context.Context, s string, _ ...any) (pgconn.CommandTag, error) {
	f.last = s
	if f.err != nil {
		return pgconn.CommandTag{}, f.err
	}
	return pgconn.NewCommandTag("UPDATE 3"), nil
}

func (f *fakeConn) BeginTx(_ context.Context, o pgx.TxOptions) (pgx.Tx, error) {
	f.opts = o
	if f.err != nil {
		return nil, f.err
	}
	return f, nil
}

func (f *fakeConn) Commit(context.Context) error {
	f.done = append(f.done, "commit")
	return nil
}

func (f *fakeConn) Rollback(context.Context) error {
	f.done = append(f.done, "rollback")
	return nil
}

func TestExecutor(t *testing.T) {
	ctx := context.Background()
	f := &fakeConn{}
	var ex conn.Executor = executor{c: f, pool: f}
	if r, err := ex.Query(ctx, "SELECT 1", nil); err != nil || r == nil || f.last != "SELECT 1" {
		t.Errorf("Query: %v, %v; ran %q", r, err, f.last)
	}
	if res, err := ex.Exec(ctx, "UPDATE t", nil); err != nil || res != (conn.Result{Affected: 3}) {
		t.Errorf("Exec: %+v, %v", res, err)
	}
	in, tx, err := ex.Begin(ctx, sql.TxOptions{ReadOnly: true})
	if err != nil || f.opts.AccessMode != pgx.ReadOnly {
		t.Fatalf("Begin: %v, options %+v", err, f.opts)
	}
	if res, err := in.Exec(ctx, "DELETE", nil); err != nil || res.Affected != 3 || f.last != "DELETE" {
		t.Errorf("Exec in the transaction: %+v, %v", res, err)
	}
	if err := errors.Join(tx.Commit(ctx), tx.Rollback(ctx)); err != nil || !slices.Equal(f.done, []string{"commit", "rollback"}) {
		t.Errorf("ending the transaction: %v, ran %v", err, f.done)
	}
}

func TestExecutorErrors(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("boom")
	f := &fakeConn{err: boom}
	ex := executor{c: f, pool: f}
	if r, err := ex.Query(ctx, "SELECT 1", nil); !errors.Is(err, boom) || r != nil {
		t.Errorf("Query: %v, %v; a nil Rows must stay an untyped nil", r, err)
	}
	if _, err := ex.Exec(ctx, "UPDATE t", nil); !errors.Is(err, boom) {
		t.Errorf("Exec: %v", err)
	}
	if _, _, err := ex.Begin(ctx, sql.TxOptions{}); !errors.Is(err, boom) {
		t.Errorf("Begin: %v", err)
	}
}
