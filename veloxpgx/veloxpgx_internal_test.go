package veloxpgx

import (
	"database/sql"
	"testing"

	"github.com/jackc/pgx/v5"
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
