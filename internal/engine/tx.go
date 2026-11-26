package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/syssam/velox/internal/dberr"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/syssam/velox/internal/conn"
)

// Rows and the rest of the driver-facing types are internal/conn's, so that velox's driver packages
// can implement them.
type (
	Rows       = conn.Rows
	execResult = conn.Result
	txControl  = conn.Tx
	executor   = conn.Executor
)

// txOptions are a transaction's options; TxOption sets them.
type txOptions struct {
	Isolation sql.IsolationLevel
	ReadOnly  bool
	Retries   int
	Timeout   time.Duration // TxTimeout; 0: the DB's Tx default
	// CancelOnEnd is TxCancelOnEnd: fn's context is canceled when the
	// attempt ends, and so is each savepoint's when it returns.
	CancelOnEnd bool
	// caller is the context Tx was given: a statement under it in fn, on a
	// pool of one connection, is refused (iterconn.go).
	caller context.Context
}

// TxOption configures one call of DB.Tx: TxIsolation, TxReadOnly,
// TxRetryRolledBack, TxTimeout and TxCancelOnEnd. The zero TxOption is not valid: TxRetries
// panics on it, and so does DB.Tx when it begins the transaction.
type TxOption struct{ apply func(*txOptions) }

// TxIsolation sets the transaction's isolation level; the default is the
// database's. SQLite's transactions are serializable: LevelSerializable is
// accepted, and any other level fails Tx as "velox: begin: …".
func TxIsolation(level sql.IsolationLevel) TxOption {
	return TxOption{func(o *txOptions) { o.Isolation = level }}
}

// TxReadOnly starts a read-only transaction. On SQLite it runs under PRAGMA
// query_only, on the transaction's connection, where the setting is as it was once
// the transaction has ended, however it ended; a write in it fails. Under a DSN's
// _txlock=immediate it holds SQLite's write lock all the same.
func TxReadOnly() TxOption { return TxOption{func(o *txOptions) { o.ReadOnly = true }} }

// TxRetryRolledBack runs the transaction again, up to n more times, when the
// database rolled it back before COMMIT, so that fn may run again: a
// serialization failure or a deadlock (PostgreSQL 40001 and 40P01, MySQL 1213
// and 1205, SQLite BUSY and LOCKED), or a BEGIN or statement of the
// transaction's own that fails on a lost or refused connection, which the
// server rolls back with it (a dropped connection, a server shutting down or
// restarting, too many connections). An error of fn's own, such as an io.EOF,
// is not a lost connection; a failed login and a blocked host are not retried.
// fn must be safe to run again, and what it did outside the transaction, or
// under WithoutTx, is done again. An error that matches ErrCommitUnknown is
// not retried, MySQL's 1205 at COMMIT and a connection lost at COMMIT
// included. Each retry waits first, a random time from 5 ms that grows to at
// most a second; the transaction's deadline or ctx's end stops the wait, and
// the retry.
func TxRetryRolledBack(n int) TxOption { return TxOption{func(o *txOptions) { o.Retries = n }} }

// TxCancelOnEnd cancels the context fn gets when the transaction's attempt
// ends, committed or rolled back, and the context a nested Tx's fn gets when
// that Tx returns, with the cause ErrTxEnded. A statement that a goroutine
// sends under such a context after the end then fails with an error that
// matches context.Canceled, and context.Cause(ctx) is ErrTxEnded, before
// anything is sent: it does not run on its own, as under an ended
// transaction's mark it otherwise does (see Tx). The commit and the
// rollback run on the context Tx was given, so they are not affected, and
// nor are the AfterCommit and AfterRollback functions. A retry gives its
// attempt a fresh fn context. The context is a child of the one Tx runs on,
// which costs a transaction one allocation. It is for WithTxDefaults, to set
// once; a nested Tx refuses it as it refuses every option, and takes the
// outermost Tx's choice.
func TxCancelOnEnd() TxOption { return TxOption{func(o *txOptions) { o.CancelOnEnd = true }} }

// TxRetries returns the n of the last TxRetryRolledBack in opts, or 0 when
// there is none. It reads options and is not one. It is for code that runs a
// transaction's fn on an application's behalf and cannot run it again.
func TxRetries(opts ...TxOption) int {
	var o txOptions
	for _, opt := range opts {
		applyTxOption(opt, &o)
	}
	return o.Retries
}

// ended reports whether err says the driver already ended the transaction,
// as it does after a canceled context; rolling back then has nothing to do.
func ended(err error) bool {
	return dberr.Is(err, sql.ErrTxDone) || dberr.DriverSentinel(err, dberr.PgTxClosed)
}

// rollback rolls the transaction ctl controls back on rollbackContext. Its
// deadline is the rollback timeout's alone: the transaction's ROLLBACK must
// run after the transaction's deadline has passed.
func (db *DB) rollback(ctx context.Context, ctl txControl) error {
	rctx, cancel := db.rollbackContext(ctx, time.Time{})
	defer cancel()
	return ctl.Rollback(rctx)
}

// DefaultRollbackTimeout bounds every rollback velox sends on a context it
// detached from the caller's cancellation: the transaction's ROLLBACK and a
// savepoint's ROLLBACK TO and its RELEASE. WithRollbackTimeout replaces it.
const DefaultRollbackTimeout = 5 * time.Second

// rollbackContext is ctx without its cancellation, with db's rollback
// timeout from now; for a savepoint also no later than the transaction's
// deadline (also).
func (db *DB) rollbackContext(ctx context.Context, also time.Time) (context.Context, context.CancelFunc) {
	d := db.Cfg.RollbackTimeout
	if d == 0 {
		d = DefaultRollbackTimeout
	}
	dl := time.Now().Add(d)
	if !also.IsZero() && also.Before(dl) {
		dl = also
	}
	return context.WithDeadline(context.WithoutCancel(ctx), dl)
}

// past reports whether deadline is set and has passed.
func past(deadline time.Time) bool {
	return !deadline.IsZero() && !time.Now().Before(deadline)
}

// overGrace is how long over waits for the Done of a context whose deadline
// has passed.
const overGrace = 50 * time.Millisecond

// over reports whether ctx has ended. Once its deadline has passed, it is
// over: a statement in the transaction carries the same deadline on a timer
// of its own, which may fire first, and the statement's timeout must not find
// the transaction still live. over then waits for ctx to end, so that its
// error says why, but at most overGrace: a context whose Done never closes
// must not hang the transaction.
func over(ctx context.Context) bool {
	if dl, ok := ctx.Deadline(); ok && !time.Now().Before(dl) {
		t := time.NewTimer(overGrace)
		defer t.Stop()
		select {
		case <-ctx.Done():
		case <-t.C:
		}
		return true
	}
	return ctx.Err() != nil
}

// savepointErr is the error of a SAVEPOINT or RELEASE (what) that failed
// with err on sctx; it matches ErrTimeout when velox's deadline ended sctx,
// and the context's error when the caller's ended it (endedErr).
func savepointErr(sctx context.Context, what string, err error) error {
	return endedErr(sctx, what, fmt.Errorf("velox: %s: %w", what, err))
}

// WithoutTx returns ctx without the mark that Tx puts on fn's ctx, keeping
// everything else: its values (the scope values, tags, spans), its deadline and its
// cancellation. It is for work inside fn that must not join the transaction,
// such as an audit row that must survive a rollback: under it, a statement on
// db runs outside the transaction, on its own connection, and a Tx on db
// begins a transaction of its own. A statement on tx under it still runs in
// the transaction. On a context without the mark it returns ctx.
//
// A statement on db under it needs a second connection while the transaction
// holds one. On a pool of one connection, as SQLite often has, it fails, from
// any goroutine, rather than wait for the transaction that waits for fn.
func WithoutTx(ctx context.Context) context.Context {
	if ctx.Value(txKey{}) == nil {
		return ctx
	}
	return noTxCtx{ctx}
}

// noTxCtx is WithoutTx's context: its parent without the transaction's mark.
type noTxCtx struct{ context.Context }

// Value returns nil for txKey, and the parent context's value otherwise.
func (c noTxCtx) Value(key any) any {
	if _, ok := key.(txKey); ok {
		return nil
	}
	return c.Context.Value(key)
}

// String names the context as the context package's do.
func (c noTxCtx) String() string { return fmt.Sprint(c.Context) + ".WithoutTx" }

// txKey is the context key of the mark a transaction's fn's context carries.
type txKey struct{}

// txSelfKey is the context key of the frame, the transaction's or a
// savepoint's, whose fn's context it is (txCtx.self).
type txSelfKey struct{}

func applyTxOption(opt TxOption, o *txOptions) { opt.apply(o) }

// WithTxDefaults sets the options every Tx of the DB, and of its shards,
// applies before the call's own, so that the call's override them. A nested
// Tx, a savepoint, applies none. A zero TxOption panics.
func WithTxDefaults(opts []TxOption) Option {
	for _, opt := range opts {
		if opt.apply == nil {
			panic("velox: WithTxDefaults: the zero TxOption")
		}
	}
	opts = slices.Clone(opts)
	return Option{func(c *config) { c.TxDefaults = append(c.TxDefaults, opts...) }}
}

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
// transaction's. Under the mark, a statement on db itself, the DB that began
// the transaction, runs in it: on tx, or on the savepoint whose fn got the
// context; and db.Tx there makes a savepoint, as tx.Tx does. So code that
// holds only db, such as a GraphQL resolver, runs in the caller's
// transaction (ADR 0171). Only that DB joins: not a copy of it with another
// WithoutPolicy reason or recording. A statement on any other *DB fails
// before anything is sent, with ErrOutsideTx, as "velox: <Entity>:
// statement outside its transaction": a DB from another Open, another
// transaction's tx, or such a copy. A Tx on such a *DB fails the same way,
// as "velox: transaction: statement outside its transaction".
//
// The mark refuses nothing else. A statement under a context without the
// mark runs as usual: one in a goroutine that fn starts with the ctx given to
// Tx, for example. So does a statement under WithoutTx(ctx), which removes
// the mark for work that must not join the transaction. Once the transaction
// has ended, committed or rolled back, its mark counts as none.
//
// On a pool of one connection, as Connect makes on SQLite, the transaction holds
// the connection, so a statement on db in fn would wait for itself. velox tells
// it by its context: one under the context Tx was given, or under WithoutTx of
// fn's, fails, from any goroutine. One under another context waits for the
// transaction to end, as do one on another DB over the same *sql.DB and one on
// velox.Pool(db). Once the context of the statement or of the Tx has ended, the
// statement's error says so and matches it.
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
// On MySQL, a deadlock (1213) rolls the whole transaction back, as does a lock
// wait timeout (1205) under innodb_rollback_on_timeout, which velox asks the
// server after one. On SQLite, an interrupted statement (one whose context ended)
// or INSERT OR ROLLBACK can do the same, so velox asks after any failed statement.
// Should fn swallow the error, its later statements fail unsent and its nil
// returns the error: nothing runs outside the transaction.
//
// Raw SQL on tx that would end the transaction is refused, with ErrTxControl, unsent:
// a statement whose first keyword, after comments, is COMMIT, END, ROLLBACK (but not
// ROLLBACK TO), ABORT, BEGIN, START or PREPARE TRANSACTION. On MySQL so is CREATE,
// ALTER, DROP, TRUNCATE or RENAME, unless of a TEMPORARY table, which commit. That is
// all velox reads. MySQL also commits on GRANT, REVOKE, LOCK TABLES, SET autocommit
// and others, and a driver that runs multi-statement text (MySQL's multiStatements)
// runs what follows the first statement: do not end the transaction that way, as
// velox cannot see it, and later statements commit alone. SQLite runs such text too,
// so there velox asks after each raw statement, and breaks the transaction if it is
// gone. On PostgreSQL pgx and lib/pq run all of a text sent by the simple protocol,
// as one without arguments is, so velox asks after a raw text that holds a semicolon
// before its trailing ones, with a savepoint. Each probe costs a statement or two,
// and text that ends the transaction and begins another still cannot be told.
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
// Under the context of a transaction db began, Tx makes a savepoint too, as
// db joins the transaction: a function given db and its caller's ctx runs
// in the caller's transaction, takes no options, and is retried only with
// the outermost Tx.
//
// Inside a transaction, Tx makes a savepoint instead, so an error in a nested
// fn rolls back only that fn's work. When releasing the savepoint fails, the
// nested Tx rolls back to the savepoint, on a context without ctx's
// cancellation, and returns the error, so an error always means the nested
// work is undone. When that rollback fails too, or a rollback after fn's error
// does, the transaction is broken: its statements fail, and the outer fn's nil
// rolls it back with an error. A nested Tx (a savepoint) takes no options and returns an error when given any, and passes fn the ctx
// it is given, marked as the transaction's.
//
// tx is valid only inside fn: after Tx returns, statements on it fail (or, for
// a nested tx, run in the enclosing transaction). tx is not safe for
// concurrent use: run its statements from one goroutine at a time. A statement
// sent while another goroutine's is being sent fails, and the transaction then
// rolls back with an error that says so. Overlaps outside the send, and an
// Iter's loop, are not seen.
func (db *DB) Tx(ctx context.Context, fn func(ctx context.Context, tx *DB) error, opts ...TxOption) error {
	db = db.In(ctx)
	if db.misuse != nil {
		return fmt.Errorf("velox: transaction: %w", db.misuse)
	}
	if db.Foreign(ctx) {
		return fmt.Errorf("velox: transaction: %w", ErrOutsideTx)
	}
	if IsDryRun(ctx) {
		return fmt.Errorf("velox: transaction: %w", ErrDryRun)
	}
	if db.InTx {
		if len(opts) > 0 {
			return errors.New("velox: transaction: a nested Tx (a savepoint) takes no options; give them to the outermost Tx")
		}
		return db.savepoint(ctx, fn)
	}
	sh, err := db.shardForTx(ctx)
	if err != nil {
		return err
	}
	if err := waitsForConn(ctx, db); err != nil {
		return fmt.Errorf("velox: transaction: %w", err)
	}
	o := txOptions{caller: ctx}
	for _, opt := range db.Cfg.TxDefaults {
		applyTxOption(opt, &o)
	}
	for _, opt := range opts {
		applyTxOption(opt, &o)
	}
	def, maxd := db.Cfg.Deadlines.Tx, db.Cfg.Deadlines.MaxTx
	if o.Timeout > 0 {
		def = 0
		if maxd <= 0 || o.Timeout < maxd {
			maxd = o.Timeout
		}
	}
	tctx, cancel := bound(ctx, def, maxd, time.Time{}, nil)
	// The deadline, velox's or the caller's, bounds the statements in fn
	// whatever context they are given; it is velox's when bound set one.
	deadline, _ := tctx.Deadline()
	var cause error
	if cancel != nil {
		defer cancel()
		cause = ErrTimeout
	}
	info := db.newTxInfo(o, sh)
	// after is the AfterRollback functions of the attempts that rolled back,
	// then the committed attempt's due AfterRollback and AfterCommit
	// functions, which run once Tx's work is done: the TxAround hooks have
	// returned and the connection is back in the pool.
	if len(db.Cfg.TxArounds) == 0 {
		// No closure here: one passed to the hooks escapes, and would cost
		// a DB with nothing configured an allocation per transaction.
		var after []func(context.Context)
		err = db.attempts(tctx, tctx, fn, o, deadline, cause, info, sh, &after)
		runAfter(ctx, after)
		return err
	}
	after, err := db.txAround(ctx, tctx, info, func(ctx context.Context, after *[]func(context.Context)) error {
		return db.attempts(ctx, tctx, fn, o, deadline, cause, info, sh, after)
	})
	runAfter(ctx, after)
	return err
}

// attempts runs fn in a transaction on ctx, and again after a retryable
// failure as many times as o allows. tctx is the context Tx bounded, ctx the
// one the TxAround hooks passed on (tctx when there are none); cause is
// ErrTimeout when the deadline is velox's. info, when not nil, is updated
// before each attempt and each retry. sh is the shard the transaction runs
// on; nil on a DB without a router. after gets the AfterCommit functions of
// the attempt that committed.
func (db *DB) attempts(ctx, tctx context.Context, fn func(context.Context, *DB) error, o txOptions, deadline time.Time, cause error, info *TxInfo, sh *shardConn, after *[]func(context.Context)) error {
	for attempt := 1; ; attempt++ {
		if info != nil {
			info.Attempt = attempt
		}
		var wrote *atomic.Bool
		if db.Rt != nil {
			wrote = new(atomic.Bool) // one per attempt: a retry forgets a rolled back write
		}
		var lost *atomic.Bool
		if o.Retries > 0 {
			lost = new(atomic.Bool) // one per attempt, as wrote is
		}
		committed, err := db.transaction(ctx, fn, o, deadline, cause, info, wrote, lost, sh, after)
		if committed && wrote != nil && wrote.Load() {
			// A COMMIT that failed has an unknown outcome, as a write's that
			// reached the database: it pins too.
			db.pinSession(tctx) // the context given to Tx, not a TxAround hook's
		}
		if err != nil && cause != nil && timedOut(tctx) && !dberr.Is(err, ErrTimeout) {
			err = errors.Join(timeoutError("transaction"), err)
		}
		// A COMMIT whose outcome is unknown is never run again, though its
		// error be retryable before COMMIT (MySQL's 1205). A statement that
		// lost its connection before COMMIT rolled the transaction back, as a
		// deadlock does: the server rolls back what a dropped connection held.
		if err == nil || attempt > o.Retries || dberr.Is(err, ErrCommitUnknown) ||
			!dberr.Retryable(err) && (lost == nil || !lost.Load()) {
			return err
		}
		if ctx.Err() != nil || tctx.Err() != nil || !retryWait(ctx, tctx, retryDelay(attempt)) {
			return retryEnded(ctx, tctx, cause, err)
		}
		if info != nil {
			info.Retries = append(info.Retries, TxRetry{Err: err, At: time.Now()})
		}
	}
}

// retryEnded is the error of a transaction whose ctx or tctx ended before the
// retry of err, the last attempt's failure, which it shows: as Tx documents, it
// matches why they ended, ErrTimeout when that is velox's deadline.
func retryEnded(ctx, tctx context.Context, cause, err error) error {
	switch {
	case cause != nil && timedOut(tctx):
		if dberr.Is(err, ErrTimeout) {
			return err
		}
		return errors.Join(timeoutError("transaction"), err)
	case ctx.Err() != nil:
		return errors.Join(fmt.Errorf("velox: transaction: %w", ctxErr(ctx)), err)
	}
	return errors.Join(fmt.Errorf("velox: transaction: %w", ctxErr(tctx)), err)
}

// retryBase and retryMax bound the wait before a transaction's retry: the
// first is at most retryBase, each later at most twice the one before, none
// more than retryMax. Vars, so that a test can change them.
var (
	retryBase = 10 * time.Millisecond
	retryMax  = time.Second
)

// retryWait waits before a retry. A var, so that a test can record it.
var retryWait = waitRetry

// retryDelay is the wait after the given failed attempt, from 1: half its
// ceiling and a random part, so that transactions that deadlocked together do
// not retry together.
func retryDelay(attempt int) time.Duration {
	ceiling := retryMax
	if c := retryBase << min(attempt-1, 30); c > 0 && c < ceiling {
		ceiling = c
	}
	return ceiling/2 + rand.N(ceiling/2+1)
}

// waitRetry waits d, and reports false when ctx or tctx ends first.
func waitRetry(ctx, tctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
	case <-tctx.Done():
	}
	return false
}

// transaction runs fn once in a new transaction, and records how it ended in
// info, which may be nil. A transaction that did not finish, because fn
// panicked or called runtime.Goexit, is rolled back by the deferred function.
// rollback uses rollbackContext, without ctx's cancellation, so a canceled ctx
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
// fn's nil runs the BeforeCommit functions it queued (runBefore); an error
// of theirs rolls back as fn's does.
//
// committed reports whether COMMIT was sent, whether or not it then failed.
// after gets appended what Tx runs once it is done: the AfterRollback
// functions of an attempt that rolled back, and, once COMMIT has
// succeeded, those a rolled back savepoint made due, then the AfterCommit
// functions. A panic or Goexit in fn appends none.
func (db *DB) transaction(ctx context.Context, fn func(context.Context, *DB) error, o txOptions, deadline time.Time, cause error, info *TxInfo, wrote, lost *atomic.Bool, sh *shardConn, after *[]func(context.Context)) (committed bool, _ error) {
	primary := db.Ex
	var txShard *shardConn // its statements check their shard against it
	if sh != nil {
		primary = sh.Primary // a transaction runs on the primary
		if db.Rt.One == nil {
			txShard = sh
		}
	}
	stop := watchHeld(db, "transaction", nil)
	ex, ctl, err := primary.Begin(ctx, sql.TxOptions{Isolation: o.Isolation, ReadOnly: o.ReadOnly})
	stop()
	if err != nil {
		endTx(info, TxError)
		markLost(lost, err) // before any statement: nothing to roll back
		return false, endedErr(ctx, "transaction", fmt.Errorf("velox: begin: %w", err))
	}
	fctx := ctx // the context fn gets is built on it
	if o.CancelOnEnd {
		// Deferred before the rollback's defer below, so it runs after it.
		var cancel context.CancelCauseFunc
		fctx, cancel = context.WithCancelCause(ctx)
		defer cancel(ErrTxEnded)
	}
	f := newTxFrame(fctx, DB{D: db.D, Ex: ex, Rt: db.Rt, Cfg: db.Cfg, InTx: true, txDeadline: deadline, txCause: cause, tx: info, txWrote: wrote, txLost: lost, txShard: txShard, bypass: db.bypass, rec: db.rec}, nil)
	defer releaseConn(db, holdConn(o.caller, db, f)) // deferred first, so it runs last: after the rollback
	defer f.ended.Store(true)                        // before the note, and after the rollback
	// Its entities read on it while it is live, then on db (Source).
	defer txSource(&f.db, db.src).end()
	f.origin = db.src
	f.cancelEnd = o.CancelOnEnd
	finished, committedOK := false, false
	defer func() {
		if !finished {
			endTx(info, TxRollback)
			_ = db.rollback(ctx, ctl)
		}
		if !committedOK {
			f.unkeep(0) // rolled back, or a COMMIT that failed; and before a retry
			rolled := f.closeAfter()
			if finished {
				// Not on a panic or a Goexit: Tx does not return, and runs none.
				*after = append(*after, rolled...)
			}
		}
	}()
	err = fn(&f.ctx, &f.db)
	if err == nil {
		err = f.runBefore(&f.ctx)
	}
	if err != nil {
		finished = true
		return false, db.abort(ctx, ctl, info, deadline, cause, err)
	}
	finished = true
	if sent, err := db.commit(ctx, ctl, f, info, deadline, cause); err != nil {
		return sent, err
	}
	committedOK = true
	*after = append(*after, f.takeAfter()...)
	return true, nil
}

// abort rolls the transaction back after fn, or a Before function, failed
// with err, and returns the error Tx gives: err, joined with the rollback's
// error when that failed on a live connection, or with ErrTimeout once
// velox's deadline passed.
func (db *DB) abort(ctx context.Context, ctl txControl, info *TxInfo, deadline time.Time, cause, err error) error {
	if ctx.Err() != nil || past(deadline) {
		endTx(info, TxError)
	} else {
		endTx(info, TxRollback)
	}
	if rerr := db.rollback(ctx, ctl); rerr != nil && !ended(rerr) && !over(ctx) && !past(deadline) {
		return errors.Join(err, fmt.Errorf("velox: rollback: %w", rerr))
	}
	if cause != nil && past(deadline) && !dberr.Is(err, ErrTimeout) {
		// velox's deadline passed, whatever context fn's statements ran
		// on: say so without waiting for the deadline's timer.
		return errors.Join(timeoutError("transaction"), err)
	}
	return err
}

// commit ends a transaction whose fn succeeded: it rolls it back when ctx or
// the deadline ended or a savepoint broke it, and sends COMMIT otherwise. sent
// reports whether COMMIT was sent, which is when a failure leaves the outcome
// open; err is nil on a commit.
func (db *DB) commit(ctx context.Context, ctl txControl, f *txFrame, info *TxInfo, deadline time.Time, cause error) (sent bool, _ error) {
	if over(ctx) || past(deadline) {
		endTx(info, TxError)
		// past the deadline, or canceled: roll back on every driver rather
		// than leave it to the driver whether a late commit succeeds.
		_ = db.rollback(ctx, ctl)
		// velox's deadline is the cause even when a TxAround hook passed on
		// a context without it.
		if timedOut(ctx) || cause != nil && past(deadline) {
			return false, timeoutError("transaction")
		}
		err := ctxErr(ctx)
		if err == nil {
			// past its deadline, its Done not closed within the grace.
			err = context.DeadlineExceeded
		}
		return false, fmt.Errorf("velox: transaction: %w", err)
	}
	if err := f.uncommittable(); err != nil {
		endTx(info, TxError)
		if rerr := db.rollback(ctx, ctl); rerr != nil && !ended(rerr) {
			return false, errors.Join(err, fmt.Errorf("velox: rollback: %w", rerr))
		}
		return false, err
	}
	if err := ctl.Commit(ctx); err != nil {
		endTx(info, TxError)
		// A deferred constraint is checked here: it is a *ConstraintError,
		// with no entity, as the commit is no one statement's.
		// An error that says the transaction rolled back (dberr.RolledBack)
		// is one; anything else leaves the outcome unknown: ErrCommitUnknown.
		if ce := dberr.Constraint("", err); ce != nil {
			err = ce
		} else if !dberr.RolledBack(err) {
			err = commitUnknownError{err}
		}
		return true, endedErr(ctx, "transaction", fmt.Errorf("velox: commit: %w", err))
	}
	endTx(info, TxCommit)
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
// A RELEASE that fails rolls back to the savepoint, as fn's error does. A
// rollback of the savepoint that fails, after any of them or fn's panic, breaks
// the transaction: see txFrame.broken.
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
		rc, _ := ctx.Value(routeKey{}).(routeCtx)
		sh, err := db.Rt.ShardOf(ctx, rc)
		if err != nil {
			return fmt.Errorf("velox: savepoint: %w", err)
		}
		if sh != nil && sh != db.txShard {
			return fmt.Errorf("velox: savepoint: for shard %q in a transaction on shard %q", sh.Name, db.txShard.Name)
		}
	}
	sctx, cancel := bound(ctx, 0, 0, db.txDeadline, db.txCause)
	if cancel != nil {
		defer cancel()
	}
	name := "velox_" + strconv.FormatUint(db.txRoot.savepoints.Add(1), 10)
	if _, err := db.Ex.Exec(sctx, "SAVEPOINT "+name, nil); err != nil {
		return savepointErr(sctx, "savepoint", err)
	}
	mark, kmark, amark, poison := db.batch.len(), len(db.txRoot.kept), db.txRoot.queueMark(), db.batch.failed()
	finished := false
	defer func() {
		if !finished {
			db.batch.restore(poison)
			db.txRoot.unkeep(kmark)
			db.txRoot.unqueue(amark)
			db.breakOn(db.undoSavepoint(ctx, name))
		}
	}()
	fctx := ctx
	if db.txRoot.cancelEnd {
		var cancel context.CancelCauseFunc
		fctx, cancel = context.WithCancelCause(ctx)
		defer cancel(ErrTxEnded)
	}
	f := newTxFrame(fctx, DB{D: db.D, Ex: db.Ex, Rt: db.Rt, Cfg: db.Cfg, InTx: true, txDeadline: db.txDeadline, txCause: db.txCause, tx: db.tx, txWrote: db.txWrote, txLost: db.txLost, txShard: db.txShard, bypass: db.bypass, rec: db.rec, batch: db.batch}, db.txRoot)
	defer f.ended.Store(true) // its handles are stale once it has returned (AfterCommit)
	defer txSource(&f.db, db.src).end()
	if err := fn(&f.ctx, &f.db); err != nil {
		finished = true
		db.batch.truncate(mark)
		db.batch.restore(poison) // a write that failed in it took no effect
		db.txRoot.unkeep(kmark)
		db.txRoot.unqueue(amark)
		// As in transaction: once the transaction's deadline has passed, the
		// connection may be gone and the rollback's error is noise.
		rerr := db.undoSavepoint(ctx, name)
		db.breakOn(rerr)
		if rerr != nil && !ended(rerr) && ctx.Err() == nil && !db.pastTxDeadline() {
			return errors.Join(err, fmt.Errorf("velox: rollback to savepoint: %w", rerr))
		}
		return err
	}
	finished = true
	if _, err := db.Ex.Exec(sctx, "RELEASE SAVEPOINT "+name, nil); err != nil {
		// The nested Tx fails, so its work must not commit with the
		// transaction: undo it, or break the transaction when that fails too.
		err = savepointErr(sctx, "release savepoint", err)
		db.batch.truncate(mark)
		db.batch.restore(poison)
		db.txRoot.unkeep(kmark)
		db.txRoot.unqueue(amark)
		rerr := db.undoSavepoint(ctx, name)
		db.breakOn(rerr)
		if rerr != nil && !ended(rerr) && !db.pastTxDeadline() {
			return errors.Join(err, fmt.Errorf("velox: rollback to savepoint: %w", rerr))
		}
		return err
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

// noteServerRollback breaks db's transaction when err, a statement's, means the
// database rolled all of it back. The connection is then in autocommit: a
// statement after an fn that swallowed err would commit on its own. On MySQL a
// deadlock (1213) always does that, and a lock wait timeout (1205) under
// innodb_rollback_on_timeout. On SQLite an interrupt, a conflict clause of
// ROLLBACK and some errors do, which only asking can tell. PostgreSQL keeps the
// transaction, aborted, and refuses its COMMIT itself.
func (db *DB) noteServerRollback(ctx context.Context, err error) {
	if db.txRoot.broken != nil {
		return
	}
	gone := false
	switch db.D {
	case MySQL:
		n, _, ok := dberr.MySQLError(err)
		gone = ok && (n == 1213 || n == 1205 && db.rollsBackOnTimeout(ctx))
	case SQLite:
		gone = db.sqliteRolledBack(ctx)
	}
	if gone {
		db.txRoot.broken = fmt.Errorf("the database rolled the transaction back, so it can only roll back: %w", err)
	}
}

// sqliteRolledBack reports whether SQLite's transaction has gone, after a failed
// statement. BEGIN asks: ROLLBACK TO an unknown savepoint says the same with a
// transaction and without. In one, BEGIN fails and changes nothing. Without one it
// opens an empty transaction, for the ROLLBACK that a broken transaction ends in.
// No answer counts as gone, as in rollsBackOnTimeout. A transaction past its
// deadline, or one database/sql has ended, can send nothing more anyway.
func (db *DB) sqliteRolledBack(ctx context.Context) bool {
	if db.pastTxDeadline() {
		return false
	}
	pctx, cancel := db.rollbackContext(ctx, db.txDeadline)
	defer cancel()
	_, err := db.Ex.Exec(pctx, "BEGIN", nil)
	return err == nil || !ended(err) && !strings.Contains(err.Error(), "within a transaction")
}

// rollsBackOnTimeout asks the server, on the transaction's connection, whether
// a lock wait timeout rolls the transaction back. No answer counts as yes: a
// transaction wrongly refused fails, and one wrongly trusted loses its writes.
func (db *DB) rollsBackOnTimeout(ctx context.Context) bool {
	qctx, cancel := db.rollbackContext(ctx, db.txDeadline)
	defer cancel()
	r, err := db.Ex.Query(qctx, "SELECT @@innodb_rollback_on_timeout", nil)
	if err != nil {
		return true
	}
	defer r.Close()
	var on int64
	if !r.Next() || r.Scan(&on) != nil {
		return true
	}
	return on != 0
}

// undoSavepoint is rollbackSavepoint on rollbackContext, bounded by the
// transaction's deadline too.
func (db *DB) undoSavepoint(ctx context.Context, name string) error {
	rctx, cancel := db.rollbackContext(ctx, db.txDeadline)
	defer cancel()
	return db.rollbackSavepoint(rctx, name)
}

// rollbackSavepoint undoes the work since the savepoint and releases it,
// because PostgreSQL keeps a savepoint after ROLLBACK TO and a loop of failing
// nested Tx would pile up subtransactions.
func (db *DB) rollbackSavepoint(ctx context.Context, name string) error {
	if _, err := db.Ex.Exec(ctx, "ROLLBACK TO SAVEPOINT "+name, nil); err != nil {
		return err
	}
	_, err := db.Ex.Exec(ctx, "RELEASE SAVEPOINT "+name, nil)
	return err
}

// txCtx is the context a transaction's or a savepoint's fn gets: the context
// it runs on, marked with the transaction's root frame, which Value returns
// for txKey. The root frame is the transaction's identity, which its
// savepoints share. Under a mark whose transaction has not ended, a
// statement on a DB of another transaction, or of none, fails with
// ErrOutsideTx (foreign). It is a context type of its own, rather than
// context.WithValue's, so that it can live in the DB's allocation (txFrame):
// marking costs a transaction no allocation.
type txCtx struct {
	context.Context
	root *txFrame
	self *txFrame // the frame whose fn got the context: root, or a savepoint's
}

// Value returns the transaction's root frame for txKey, and the parent
// context's value otherwise.
func (c *txCtx) Value(key any) any {
	switch key.(type) {
	case txKey:
		return c.root
	case txSelfKey:
		return c.self
	}
	return c.Context.Value(key)
}

// String names the context as the context package's do.
func (c *txCtx) String() string { return fmt.Sprint(c.Context) + ".WithTx" }

// txFrame is a transaction's or a savepoint's DB and the context its fn
// gets, in one allocation. ended is set, on the transaction's own frame (its
// root), once the transaction has committed or rolled back, and on a
// savepoint's once its nested Tx has returned, released or rolled back.
// Foreign reads only the root's.
//
// broken is set, on the root, once a savepoint's ROLLBACK TO or its RELEASE
// has failed, as the savepoint's work may still be in the transaction, or once
// the server has rolled it back (noteServerRollback). From then on the
// transaction does not commit, and its statements and nested Tx fail
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
	// origin is, on the root of a transaction velox began, the Source of
	// the DB that began it (Joined); nil for an adopted one (UseTx).
	origin     *Source
	db         DB
	ctx        txCtx
	ended      atomic.Bool
	savepoints atomic.Uint64
	// sending is set, on the root, while a statement of the transaction is
	// being sent (exec), and shared is set once a statement found it set:
	// two goroutines used the Tx at once, which then cannot commit. An Iter's
	// statement does not set sending, as its loop body may run statements.
	sending, shared atomic.Bool
	broken          error
	// cancelEnd is, on the root, TxCancelOnEnd: its savepoints cancel their
	// fn's context when they return.
	cancelEnd bool
	// kept is, on the root, the rows Recorders kept in the transaction, in
	// order: a savepoint that rolls back drops those after its mark, and a
	// transaction that does not commit drops them all (unkeep).
	kept []keptRow
	// adopted is set, on the root, for a transaction the caller began
	// (UseTx): velox never sees it end, so a Recorder's rows go to it
	// through Sink, and nothing is kept to drop. AfterCommit refuses it.
	adopted bool
	// after is, on the root, the functions AfterCommit queued, in order: a
	// savepoint that rolls back drops those after its mark (unqueue), and
	// they run once the transaction has committed (runAfter).
	after []func(context.Context)
	// before is, on the root, the functions BeforeCommit queued, in order,
	// which run before COMMIT (runBefore); a savepoint that rolls back drops
	// those after its mark. beforeDone is set once they have run.
	before     []func(context.Context) error
	beforeDone bool
	// rolled is, on the root, the functions AfterRollback queued, in order.
	// A savepoint that rolls back marks those after its mark due: they run
	// whatever the transaction then does. The others run only when it does
	// not commit.
	rolled []rolledFn
	// afterMu guards the queues and afterClosed, which is set once the
	// transaction's end has taken them (takeAfter, closeAfter): a later
	// AfterCommit, BeforeCommit or AfterRollback fails instead of queuing
	// what nothing would run.
	afterMu     sync.Mutex
	afterClosed bool
}

// uncommittable is why f's transaction must roll back though fn returned
// nil: it was used from two goroutines at once, or a savepoint did not roll
// back, so its fn's work may still be in the transaction. nil if neither.
func (f *txFrame) uncommittable() error {
	switch {
	case f.shared.Load():
		return fmt.Errorf("velox: transaction: %w", errTxShared)
	case f.broken != nil:
		return fmt.Errorf("velox: transaction: %w", f.broken)
	}
	return nil
}

// newTxFrame returns the frame of db, with ctx marked as the transaction's.
// root is the transaction's frame, for a savepoint; nil for the transaction
// itself, whose frame is its own root.
func newTxFrame(ctx context.Context, db DB, root *txFrame) *txFrame {
	var f *txFrame
	if db.bypass == "" {
		f = &txFrame{db: db}
	} else {
		// The copy of the handle without the reason, which the Source
		// resolves to (txSource), is in the frame's allocation.
		b := &bypassFrame{db: db}
		f = &b.txFrame
	}
	if root == nil {
		root = f
	}
	f.db.txRoot, f.db.txSelf = root, f
	f.ctx = txCtx{Context: ctx, root: root, self: f}
	return f
}

// In returns the DB a statement or Tx on db under ctx runs on: when ctx is
// marked by a live transaction that db began (Joined), that transaction's
// handle, or its savepoint's, whose fn got ctx; db otherwise. So the DB an
// application holds runs in a transaction it began, under its context,
// where another DB fails with ErrOutsideTx.
func (db *DB) In(ctx context.Context) *DB {
	if j := db.joined(ctx); j != nil {
		return j
	}
	return db
}

// joined is the handle In returns in a transaction db began; nil when ctx
// has no live transaction's mark, or db is in a transaction, or db did not
// begin the transaction: its Source or mode (sameMode) is not the
// beginner's, or its mode is not that of the savepoint whose fn got ctx.
func (db *DB) joined(ctx context.Context) *DB {
	if db.txRoot != nil || db.src == nil {
		return nil
	}
	root, _ := ctx.Value(txKey{}).(*txFrame)
	if root == nil || root.origin != db.src || root.ended.Load() || !sameMode(&root.db, db) {
		return nil
	}
	if self, _ := ctx.Value(txSelfKey{}).(*txFrame); self != nil && self.db.txRoot == root && !self.ended.Load() {
		// A savepoint begun on a WithoutPolicy or Record copy of the
		// transaction has that copy's reason or recording: db would
		// skip its policies, or write to the copy's sink. It does not
		// join; the root's handle would run in the savepoint too.
		if !sameMode(&self.db, db) {
			return nil
		}
		return &self.db
	}
	return &root.db
}

// sameMode reports whether a and b treat a statement alike: the same
// WithoutPolicy reason, misuse and recording. A DB joins a transaction only
// when its mode is the transaction's; the handle it gets back runs its
// statements, so a mode that differs would be lost. Every field of DB is
// either a mode compared here or handle state: TestDBFieldsClassified fails
// on a field that is neither.
func sameMode(a, b *DB) bool {
	return a.bypass == b.bypass && a.misuse == b.misuse && a.rec == b.rec //nolint:errorlint // the same misuse value, not a match in a tree
}

// bypassFrame is the frame of a transaction or savepoint begun with a
// WithoutPolicy reason, and the copy of its handle without the reason that
// its Source resolves to while it is live (txSource). The copy is not
// cleared when the transaction ends: a reader that loaded it from the
// Source just before keeps a whole DB, whose statements fail as an ended
// transaction's do.
type bypassFrame struct {
	txFrame
	plain DB
}

// plainCopy is the copy of f's handle without its WithoutPolicy reason, in
// f's allocation; f.db.bypass is not empty, so newTxFrame made f as the
// first field of a bypassFrame.
func (f *txFrame) plainCopy() *DB {
	return &(*bypassFrame)(unsafe.Pointer(f)).plain
}

// Foreign reports whether ctx carries the mark of a transaction that has not
// ended and that db is not in: db is in no transaction, or in another.
func (db *DB) Foreign(ctx context.Context) bool {
	m, _ := ctx.Value(txKey{}).(*txFrame)
	return m != nil && m != db.txRoot && !m.ended.Load()
}

// commitUnknownError wraps a COMMIT's error to match ErrCommitUnknown, leaving
// its message and the driver's error as they were.
type commitUnknownError struct{ err error }

func (e commitUnknownError) Error() string        { return e.err.Error() }
func (e commitUnknownError) Unwrap() error        { return e.err }
func (e commitUnknownError) Is(target error) bool { return target == ErrCommitUnknown } //nolint:errorlint // a sentinel compared by identity, as errors.Is does.
