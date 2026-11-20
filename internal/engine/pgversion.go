package engine

import (
	"context"
	"strconv"
	"strings"
	"sync/atomic"
)

// returningOld is the first PostgreSQL whose RETURNING names old and new
// (server_version_num): a recorded write there reads both in the write.
const returningOld = 180000

// versionLoading marks a version slot whose read is under way.
const versionLoading = -1

// WithoutReturningOld makes a recording handle on PostgreSQL 18 or later
// record as it does below 18, a locked read before the write, so that tests
// run that path on any server. The root does not offer it.
func WithoutReturningOld() Option { return Option{func(c *config) { c.noReturningOld = true }} }

// returnsOld reports whether a recorded write through db, under ctx, reads
// its old and new values in the write itself (RETURNING old.…, new.…):
// PostgreSQL 18 or later, without WithoutReturningOld. Only a recording
// handle about to record a write calls it, so a handle that does not record
// never asks the server its version.
func returnsOld(ctx context.Context, db *DB) bool {
	return db.D == Postgres && !db.Cfg.noReturningOld && serverVersion(ctx, db) >= returningOld
}

// serverVersion is the server_version_num of the PostgreSQL db runs on, read
// with SHOW server_version_num once per pool (per shard on a sharded DB) and
// kept: on its primary, which a replica of it matches, or in db's
// transaction. It is 0 when the version is not known: its read failed,
// which is not kept, so the next write reads it again; or another goroutine
// is reading it, whose result is not waited for (waiting could deadlock on
// a pool's last connection, which a transaction that waits behind it may
// hold). A write that gets 0 takes the locked-read path, right on every
// version.
func serverVersion(ctx context.Context, db *DB) int {
	slot := versionSlot(ctx, db)
	if slot == nil {
		return 0
	}
	if v := slot.Load(); v != 0 {
		return int(max(v, 0))
	}
	if !slot.CompareAndSwap(0, versionLoading) {
		return 0
	}
	v, err := readVersion(ctx, db)
	if err != nil || v <= 0 {
		slot.Store(0)
		return 0
	}
	slot.Store(int64(v))
	return v
}

// versionSlot is where db's server version is kept: on the shard of db's
// transaction or of ctx on a sharded DB, else in db's configuration, which
// the handles of one pool share. nil when ctx names no shard.
func versionSlot(ctx context.Context, db *DB) *atomic.Int64 {
	switch {
	case db.txShard != nil:
		return &db.txShard.version
	case !db.InTx && db.Rt != nil && db.Rt.One == nil:
		sh, err := db.ctxShard(ctx)
		if err != nil {
			return nil
		}
		return &sh.version
	}
	return &db.Cfg.version
}

// readVersion sends SHOW server_version_num through db's pipeline, on the
// primary, and returns it.
func readVersion(ctx context.Context, db *DB) (int, error) {
	st := &Statement{Op: OpRawQuery, Entity: &rowsInfo, SQL: "SHOW server_version_num", PolicyBypass: "velox: the server version a recorded write depends on", InTx: db.InTx}
	var s string
	err := db.Run(WithPrimaryReads(ctx), st, func(r Rows) error {
		s = ""
		if r.Next() {
			return r.Scan(&s)
		}
		return nil
	}, nil)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(s))
}
