package engine

import (
	"context"
	"errors"
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

// mayReturnOld reports whether a recorded write through db could read its
// old and new values in the write: PostgreSQL, without WithoutReturningOld.
func mayReturnOld(db *DB) bool { return db.D == Postgres && !db.Cfg.noReturningOld }

// returnsOld reports whether a recorded write through db, under ctx, reads
// its old and new values in the write itself (RETURNING old.…, new.…):
// PostgreSQL 18 or later, without WithoutReturningOld. Outside a
// transaction it asks the server its version when it is not known yet
// (serverVersion). In a transaction it only looks at what is known, and
// sends nothing: a failed statement there would abort the transaction. A
// transaction velox opens for a recorded write learns the version before its
// BEGIN (primeVersion); in the caller's transaction, trackedWrite asks
// inside the write's savepoint, and bidiTx in a savepoint of its own.
func returnsOld(ctx context.Context, db *DB) bool {
	if !mayReturnOld(db) {
		return false
	}
	if db.InTx {
		slot := versionSlot(ctx, db)
		return slot != nil && slot.Load() >= returningOld
	}
	return serverVersion(ctx, db) >= returningOld
}

// versionUnknown reports whether a recording handle on db would learn the
// server version by asking: PostgreSQL without WithoutReturningOld, its
// version not read yet, and no other goroutine reading it.
func versionUnknown(ctx context.Context, db *DB) bool {
	if db.rec == nil || !mayReturnOld(db) {
		return false
	}
	slot := versionSlot(ctx, db)
	return slot != nil && slot.Load() == 0
}

// primeVersion learns the server version for the recorded writes db is
// about to make, when it is not known yet, where a failure cannot abort a
// transaction: outside a transaction, before the BEGIN of one velox opens
// (ownTx); in the caller's transaction, in a savepoint of its own, which a
// failure rolls back. In a transaction velox opened it does nothing: that
// transaction primed it before its BEGIN. A failure is not kept; the writes
// then take the locked read.
func primeVersion(ctx context.Context, db *DB) {
	switch {
	case !versionUnknown(ctx, db):
	case !db.InTx:
		serverVersion(ctx, db)
	case !inOwnTx(db):
		_ = db.Tx(ctx, func(ctx context.Context, sp *DB) error {
			_, err := loadVersion(ctx, sp)
			return err
		})
	}
}

// serverVersion is the server_version_num of the PostgreSQL db, outside a
// transaction, runs on, read once per pool (per shard on a sharded DB) and
// kept (loadVersion). It is 0 when the version is not known: its read
// failed, or another goroutine is reading it. A write that gets 0 takes the
// locked-read path, right on every version.
func serverVersion(ctx context.Context, db *DB) int {
	v, _ := loadVersion(ctx, db)
	return v
}

// errVersionBusy is loadVersion's error when another goroutine is reading
// the version.
var errVersionBusy = errors.New("velox: the server version is being read")

// loadVersion returns db's server version: the one kept, or else one it
// reads with SHOW server_version_num on db (its transaction's connection
// when db is in one, else the primary) and keeps. A failed read is
// returned, and not kept, so the next write asks again. While another
// goroutine reads it, loadVersion does not wait (waiting could deadlock on a
// pool's last connection, which a transaction that waits behind it may
// hold): it returns errVersionBusy.
func loadVersion(ctx context.Context, db *DB) (int, error) {
	slot := versionSlot(ctx, db)
	if slot == nil {
		return 0, errVersionBusy
	}
	if v := slot.Load(); v != 0 {
		if v < 0 {
			return 0, errVersionBusy
		}
		return int(v), nil
	}
	if !slot.CompareAndSwap(0, versionLoading) {
		return 0, errVersionBusy
	}
	v, err := readVersion(ctx, db)
	if err != nil {
		slot.Store(0)
		return 0, err
	}
	slot.Store(int64(v))
	return v, nil
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

// readVersion sends SHOW server_version_num and returns it. It is velox's
// own statement, not the caller's: it goes to the executor directly, past
// the Rewrite and Around hooks, on db's transaction's connection, or else
// on the primary, under db's statement deadlines.
func readVersion(ctx context.Context, db *DB) (int, error) {
	ex := db.Ex
	if !db.InTx && db.Rt != nil {
		sh, err := db.ctxShard(ctx)
		if err != nil {
			return 0, err
		}
		ex = sh.Primary
	}
	if db.timed() {
		tctx, cancel := db.bound(ctx)
		if cancel != nil {
			defer cancel()
		}
		ctx = tctx
	}
	r, err := ex.Query(ctx, "SHOW server_version_num", nil)
	if err != nil {
		return 0, err
	}
	defer r.Close()
	var s string
	if r.Next() {
		if err := r.Scan(&s); err != nil {
			return 0, err
		}
	}
	if err := r.Err(); err != nil {
		return 0, err
	}
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || v <= 0 {
		return 0, errors.New("velox: SHOW server_version_num gave no version")
	}
	return v, nil
}
