package velox

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/syssam/velox2/internal/engine"
)

// Tx runs fn in a transaction. fn's error rolls it back and is returned; nil
// commits it; a panic in fn rolls it back and then panics again with the same
// value, and runtime.Goexit rolls it back and carries on. tx is a *DB like any
// other, so a function that takes a *DB works inside a transaction unchanged.
//
// fn's ctx is the context the transaction runs on: ctx, as the TxAround
// hooks passed it on, with the values they added. It carries the
// transaction's deadline, velox's or ctx's, whichever is earlier, unless a
// TxAround hook passed on a context without it; the deadline bounds every
// statement on tx either way.
//
// fn's ctx, and every context derived from it, is marked as the
// transaction's. Under the mark, a statement on any *DB other than tx and its
// savepoints fails before anything is sent, with ErrOutsideTx, as
// "velox: <Entity>: statement outside its transaction". That includes db
// itself, a DB from another Open, and another transaction's tx: such a
// statement was most likely meant for tx. A Tx on such a *DB fails the same
// way, as "velox: transaction: statement outside its transaction"; a nested
// transaction is tx.Tx.
//
// The mark refuses nothing else. A statement under a context without the
// mark runs as usual: one in a goroutine that fn starts with the ctx given to
// Tx, for example. So does a statement under WithoutTx(ctx), which removes
// the mark for work that must not join the transaction. Once the transaction
// has ended, committed or rolled back, its mark counts as none.
//
// Under a dry-run context (IsDryRun), such as the one SQL(ctx) gives a hook,
// Tx fails with ErrDryRun before BEGIN, nested or not: it is a write.
//
// WithDeadlines' Tx and MaxTx, and TxTimeout, bound the whole call. The
// transaction's deadline, velox's or ctx's, whichever is earlier, bounds every
// statement inside it, nested Tx included, whatever context the statement is
// given. A transaction whose ctx has ended (past either deadline, or canceled)
// is rolled back, never committed; its error matches ErrTimeout when velox's
// deadline passed, and context.DeadlineExceeded or context.Canceled otherwise.
//
// A rollback velox sends after fn fails, or once ctx has ended, is bounded to
// 5 seconds; database/sql's Tx.Rollback takes no context, so on a DB from Open
// that bound covers a savepoint's ROLLBACK TO only.
//
// WithTxAround's hooks wrap the whole call: every attempt and the commit.
//
// On a DB with replicas the transaction runs on the primary. On a DB opened
// with OpenShards it runs on the primary of the shard ctx names, as a
// statement's context names one; when ctx names none, Tx fails with
// ErrNoShard, as "velox: transaction: no shard for the statement: …", before
// anything is sent or any TxAround hook runs. Its statements run on that
// shard too, and one whose context names another fails.
//
// Inside a transaction, Tx makes a savepoint instead, so an error in a nested
// fn rolls back only that fn's work. When releasing the savepoint fails, the
// nested Tx returns that error and does not roll back to the savepoint.
// Whether the enclosing transaction can still commit is the database's call:
// PostgreSQL aborts it, so the outer COMMIT fails. A nested Tx (a savepoint)
// takes no options and returns an error when given any, and passes fn the ctx
// it is given, marked as the transaction's.
//
// tx is valid only inside fn: after Tx returns, statements on it fail (or, for
// a nested tx, run in the enclosing transaction). tx is not safe for
// concurrent use: run its statements from one goroutine at a time.
func (db *DB) Tx(ctx context.Context, fn func(ctx context.Context, tx *DB) error, opts ...TxOption) error {
	if db.foreign(ctx) {
		return fmt.Errorf("velox: transaction: %w", ErrOutsideTx)
	}
	if IsDryRun(ctx) {
		return fmt.Errorf("velox: transaction: %w", ErrDryRun)
	}
	if db.inTx {
		if len(opts) > 0 {
			return errors.New("velox: transaction: a nested Tx (a savepoint) takes no options; give them to the outermost Tx")
		}
		return db.savepoint(ctx, fn)
	}
	sh, err := db.shardForTx(ctx)
	if err != nil {
		return err
	}
	var o engine.TxOptions
	for _, opt := range opts {
		engine.ApplyTxOption(opt, &o)
	}
	def, maxd := db.cfg.Deadlines.Tx, db.cfg.Deadlines.MaxTx
	if o.Timeout > 0 {
		def = 0
		if maxd <= 0 || o.Timeout < maxd {
			maxd = o.Timeout
		}
	}
	tctx, cancel := engine.Bound(ctx, def, maxd, time.Time{}, nil)
	// The deadline, velox's or the caller's, bounds the statements in fn
	// whatever context they are given; it is velox's when bound set one.
	deadline, _ := tctx.Deadline()
	var cause error
	if cancel != nil {
		defer cancel()
		cause = ErrTimeout
	}
	info := db.newTxInfo(o, sh)
	if len(db.cfg.TxArounds) == 0 {
		// No closure here: one passed to the hooks escapes, and would cost
		// a DB with nothing configured an allocation per transaction.
		return db.attempts(tctx, tctx, fn, o, deadline, cause, info, sh)
	}
	return db.txAround(tctx, info, func(ctx context.Context) error {
		return db.attempts(ctx, tctx, fn, o, deadline, cause, info, sh)
	})
}

// attempts runs fn in a transaction on ctx, and again after a retryable
// failure as many times as o allows. tctx is the context Tx bounded, ctx the
// one the TxAround hooks passed on (tctx when there are none); cause is
// ErrTimeout when the deadline is velox's. info, when not nil, is updated
// before each attempt and each retry. sh is the shard the transaction runs
// on; nil on a DB without a router.
func (db *DB) attempts(ctx, tctx context.Context, fn func(context.Context, *DB) error, o engine.TxOptions, deadline time.Time, cause error, info *TxInfo, sh *engine.ShardConn) error {
	for attempt := 1; ; attempt++ {
		if info != nil {
			info.Attempt = attempt
		}
		var wrote *atomic.Bool
		if db.rt != nil {
			wrote = new(atomic.Bool) // one per attempt: a retry forgets a rolled back write
		}
		committed, err := db.transaction(ctx, fn, o, deadline, cause, info, wrote, sh)
		if committed && wrote != nil && wrote.Load() {
			// A COMMIT that failed has an unknown outcome, as a write's that
			// reached the database: it pins too.
			db.pinSession(tctx) // the context given to Tx, not a TxAround hook's
		}
		if err != nil && cause != nil && engine.TimedOut(tctx) && !engine.Is(err, ErrTimeout) {
			err = errors.Join(engine.TimeoutError("transaction"), err)
		}
		if err == nil || attempt > o.Retries || !engine.Retryable(err) || ctx.Err() != nil || tctx.Err() != nil {
			return err
		}
		if info != nil {
			info.Retries = append(info.Retries, TxRetry{Err: err, At: time.Now()})
		}
	}
}

// transaction runs fn once in a new transaction, and records how it ended in
// info, which may be nil. A transaction that did not finish, because fn
// panicked or called runtime.Goexit, is rolled back by the deferred function.
// Rollback uses RollbackContext, without ctx's cancellation, so a canceled ctx
// still rolls back, and bounded, so a dead connection cannot hang it.
//
// Once ctx has ended, a rollback's error is not reported: the driver may have
// closed the connection that the deadline cut a statement off on (pgx does),
// and the server then aborts the transaction itself.
//
// A savepoint whose rollback failed breaks the transaction (txFrame.broken):
// fn's nil then rolls it back, with an error that wraps the rollback's.
//
// deadline, Tx's, ends the transaction as ctx's would, even when a TxAround
// hook passed on a context without it: past it, nothing commits.
//
// wrote, nil on a DB without a router, is the flag the transaction's
// statements set on a successful write. sh, nil on a DB without a router, is
// the shard whose primary the transaction runs on.
//
// fn gets ctx, marked as the transaction's; the mark ends when transaction
// returns, after the commit or the rollback.
//
// committed reports whether COMMIT was sent, whether or not it then failed.
func (db *DB) transaction(ctx context.Context, fn func(context.Context, *DB) error, o engine.TxOptions, deadline time.Time, cause error, info *TxInfo, wrote *atomic.Bool, sh *engine.ShardConn) (committed bool, _ error) {
	primary := db.ex
	var txShard *engine.ShardConn // its statements check their shard against it
	if sh != nil {
		primary = sh.Primary // a transaction runs on the primary
		if db.rt.One == nil {
			txShard = sh
		}
	}
	ex, ctl, err := primary.Begin(ctx, sql.TxOptions{Isolation: o.Isolation, ReadOnly: o.ReadOnly})
	if err != nil {
		engine.EndTx(info, TxError)
		return false, fmt.Errorf("velox: begin: %w", err)
	}
	f := newTxFrame(ctx, DB{d: db.d, ex: ex, rt: db.rt, cfg: db.cfg, inTx: true, txDeadline: deadline, txCause: cause, tx: info, txWrote: wrote, txShard: txShard}, nil)
	defer f.ended.Store(true) // deferred first, so it runs last: after the rollback
	finished := false
	defer func() {
		if !finished {
			engine.EndTx(info, TxRollback)
			_ = engine.Rollback(ctx, ctl)
		}
	}()
	if err := fn(&f.ctx, &f.db); err != nil {
		finished = true
		if ctx.Err() != nil || engine.Past(deadline) {
			engine.EndTx(info, TxError)
		} else {
			engine.EndTx(info, TxRollback)
		}
		if rerr := engine.Rollback(ctx, ctl); rerr != nil && !engine.Ended(rerr) && !engine.Over(ctx) && !engine.Past(deadline) {
			return false, errors.Join(err, fmt.Errorf("velox: rollback: %w", rerr))
		}
		if cause != nil && engine.Past(deadline) && !engine.Is(err, ErrTimeout) {
			// velox's deadline passed, whatever context fn's statements ran
			// on: say so without waiting for the deadline's timer.
			return false, errors.Join(engine.TimeoutError("transaction"), err)
		}
		return false, err
	}
	finished = true
	if engine.Over(ctx) || engine.Past(deadline) {
		engine.EndTx(info, TxError)
		// Past the deadline, or canceled: roll back on every driver rather
		// than leave it to the driver whether a late commit succeeds.
		_ = engine.Rollback(ctx, ctl)
		// velox's deadline is the cause even when a TxAround hook passed on
		// a context without it.
		if engine.TimedOut(ctx) || cause != nil && engine.Past(deadline) {
			return false, engine.TimeoutError("transaction")
		}
		err := engine.CtxErr(ctx)
		if err == nil {
			// Past its deadline, its Done not closed within the grace.
			err = context.DeadlineExceeded
		}
		return false, fmt.Errorf("velox: transaction: %w", err)
	}
	if f.broken != nil {
		// A savepoint did not roll back: its fn's work may still be in the
		// transaction, which must not commit it.
		engine.EndTx(info, TxError)
		err := fmt.Errorf("velox: transaction: %w", f.broken)
		if rerr := engine.Rollback(ctx, ctl); rerr != nil && !engine.Ended(rerr) {
			return false, errors.Join(err, fmt.Errorf("velox: rollback: %w", rerr))
		}
		return false, err
	}
	if err := ctl.Commit(ctx); err != nil {
		engine.EndTx(info, TxError)
		return true, fmt.Errorf("velox: commit: %w", err)
	}
	engine.EndTx(info, TxCommit)
	return true, nil
}

// savepoint runs fn under a savepoint of the current transaction, and gives
// it ctx, marked as the transaction's. It runs no TxAround hook; its
// statements see the transaction's TxInfo.
//
// SAVEPOINT and the RELEASE after fn succeeds run within the transaction's
// deadline, as its statements do, whatever ctx is. On a sharded DB, a ctx that
// names a shard other than the transaction's, or one that is not a shard, is
// refused before SAVEPOINT, as a statement's is; one that names none passes.
//
// A rollback of the savepoint that fails, after fn's error or its panic,
// breaks the transaction: see txFrame.broken.
//
// The savepoints of a transaction are named velox_1, velox_2, and so on, in
// the order they are made, and no name is used twice: MySQL replaces a
// savepoint of the same name, so a later ROLLBACK TO would reach the wrong
// one, or none. The count is the transaction's, not the nesting depth, because
// savepoints made from DBs of one depth can be nested.
func (db *DB) savepoint(ctx context.Context, fn func(context.Context, *DB) error) error {
	if err := db.txRoot.broken; err != nil {
		return fmt.Errorf("velox: savepoint: %w", err)
	}
	if db.txShard != nil {
		rc, _ := ctx.Value(engine.RouteKey{}).(engine.RouteCtx)
		sh, err := db.rt.ShardOf(ctx, rc)
		if err != nil {
			return fmt.Errorf("velox: savepoint: %w", err)
		}
		if sh != nil && sh != db.txShard {
			return fmt.Errorf("velox: savepoint: for shard %q in a transaction on shard %q", sh.Name, db.txShard.Name)
		}
	}
	sctx, cancel := engine.Bound(ctx, 0, 0, db.txDeadline, db.txCause)
	if cancel != nil {
		defer cancel()
	}
	name := "velox_" + strconv.FormatUint(db.txRoot.savepoints.Add(1), 10)
	if _, err := db.ex.Exec(sctx, "SAVEPOINT "+name, nil); err != nil {
		return engine.SavepointErr(sctx, "savepoint", err)
	}
	finished := false
	defer func() {
		if !finished {
			db.breakOn(db.undoSavepoint(ctx, name))
		}
	}()
	f := newTxFrame(ctx, DB{d: db.d, ex: db.ex, rt: db.rt, cfg: db.cfg, inTx: true, txDeadline: db.txDeadline, txCause: db.txCause, tx: db.tx, txWrote: db.txWrote, txShard: db.txShard}, db.txRoot)
	if err := fn(&f.ctx, &f.db); err != nil {
		finished = true
		// As in transaction: once the transaction's deadline has passed, the
		// connection may be gone and the rollback's error is noise.
		rerr := db.undoSavepoint(ctx, name)
		db.breakOn(rerr)
		if rerr != nil && !engine.Ended(rerr) && ctx.Err() == nil && !db.pastTxDeadline() {
			return errors.Join(err, fmt.Errorf("velox: rollback to savepoint: %w", rerr))
		}
		return err
	}
	finished = true
	if _, err := db.ex.Exec(sctx, "RELEASE SAVEPOINT "+name, nil); err != nil {
		return engine.SavepointErr(sctx, "release savepoint", err)
	}
	return nil
}

// breakOn breaks db's transaction when err, a savepoint's rollback's, is not
// nil; the first such error is kept. Past the transaction's deadline it does
// not: the transaction can no longer commit, and its later statements fail
// as timed out (ErrTimeout), as they would without the savepoint, rather
// than with a broken transaction's error that wraps the deadline's.
func (db *DB) breakOn(err error) {
	if err != nil && db.txRoot.broken == nil && !db.pastTxDeadline() {
		db.txRoot.broken = fmt.Errorf("a savepoint did not roll back, so the transaction can only roll back: %w", err)
	}
}

// undoSavepoint is rollbackSavepoint on RollbackContext, bounded by the
// transaction's deadline too.
func (db *DB) undoSavepoint(ctx context.Context, name string) error {
	rctx, cancel := engine.RollbackContext(ctx, db.txDeadline)
	defer cancel()
	return db.rollbackSavepoint(rctx, name)
}

// rollbackSavepoint undoes the work since the savepoint and releases it,
// because PostgreSQL keeps a savepoint after ROLLBACK TO and a loop of failing
// nested Tx would pile up subtransactions.
func (db *DB) rollbackSavepoint(ctx context.Context, name string) error {
	if _, err := db.ex.Exec(ctx, "ROLLBACK TO SAVEPOINT "+name, nil); err != nil {
		return err
	}
	_, err := db.ex.Exec(ctx, "RELEASE SAVEPOINT "+name, nil)
	return err
}

// txCtx is the context a transaction's or a savepoint's fn gets: the context
// it runs on, marked with the transaction's root frame, which Value returns
// for TxKey. The root frame is the transaction's identity, which its
// savepoints share. Under a mark whose transaction has not ended, a
// statement on a DB of another transaction, or of none, fails with
// ErrOutsideTx (foreign). It is a context type of its own, rather than
// context.WithValue's, so that it can live in the DB's allocation (txFrame):
// marking costs a transaction no allocation.
type txCtx struct {
	context.Context
	root *txFrame
}

// Value returns the transaction's root frame for TxKey, and the parent
// context's value otherwise.
func (c *txCtx) Value(key any) any {
	if _, ok := key.(engine.TxKey); ok {
		return c.root
	}
	return c.Context.Value(key)
}

// String names the context as the context package's do.
func (c *txCtx) String() string { return fmt.Sprint(c.Context) + ".WithTx" }

// txFrame is a transaction's or a savepoint's DB and the context its fn
// gets, in one allocation. ended is set, on the transaction's own frame (its
// root), once the transaction has committed or rolled back.
//
// broken is set, on the root, once a savepoint's ROLLBACK TO or its RELEASE
// has failed: the savepoint's work may still be in the transaction. From then
// on the transaction does not commit, and its statements and nested Tx fail
// with broken before anything is sent. A savepoint that encloses the broken
// one and whose fn then returns nil still sends its RELEASE, which the
// transaction's rollback undoes with the rest. Like the rest of tx, it is
// read and set by one goroutine at a time.
//
// savepoints counts the savepoints made in the transaction, on the root, and
// names the next one. It is atomic so that misuse (statements on one
// transaction from several goroutines) races only on the connection, not on
// velox's own state.
type txFrame struct {
	db         DB
	ctx        txCtx
	ended      atomic.Bool
	savepoints atomic.Uint64
	broken     error
}

// newTxFrame returns the frame of db, with ctx marked as the transaction's.
// root is the transaction's frame, for a savepoint; nil for the transaction
// itself, whose frame is its own root.
func newTxFrame(ctx context.Context, db DB, root *txFrame) *txFrame {
	f := &txFrame{db: db}
	if root == nil {
		root = f
	}
	f.db.txRoot = root
	f.ctx = txCtx{Context: ctx, root: root}
	return f
}

// foreign reports whether ctx carries the mark of a transaction that has not
// ended and that db is not in: db is in no transaction, or in another.
func (db *DB) foreign(ctx context.Context) bool {
	m, _ := ctx.Value(engine.TxKey{}).(*txFrame)
	return m != nil && m != db.txRoot && !m.ended.Load()
}
