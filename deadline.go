package velox

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Deadlines are the time limits velox gives statements and transactions
// (policy design §5). A zero field is off.
type Deadlines struct {
	Statement    time.Duration // a statement whose context has no deadline gets this one
	MaxStatement time.Duration // no statement runs longer: a later deadline is brought forward
	Tx           time.Duration // a transaction whose context has no deadline gets this one
	MaxTx        time.Duration // no transaction runs longer, retries and commit included
}

// WithDeadlines sets the DB's time limits. A statement's limit covers its
// Around hooks and, with Iter, the caller's loop body. A transaction's covers
// fn, every RetrySerializable attempt and the commit.
//
// A transaction's effective deadline is velox's or the one on the context
// given to Tx, whichever is earlier. It bounds every statement inside the
// transaction, nested Tx included, whatever context that statement is given,
// and a transaction whose context has ended by commit time is rolled back.
// The error matches ErrTimeout only when velox's deadline was the one that
// passed; the caller's matches context.DeadlineExceeded alone.
func WithDeadlines(d Deadlines) Option { return func(c *config) { c.deadlines = d } }

// Timeout bounds the transaction to d, retries and commit included, in place
// of the DB's Tx default and within its MaxTx. An earlier deadline on the
// context still applies. Timeout(d) with d <= 0 is ignored, and so is Timeout
// on a nested Tx, which ignores every option: a savepoint runs within the
// enclosing transaction's deadline.
func Timeout(d time.Duration) TxOption { return func(o *txOptions) { o.timeout = d } }

// bound returns ctx with velox's limits applied: def when ctx has no
// deadline, at most maxd from now, and no later than also unless it is zero.
// cancel is nil when ctx is returned unchanged. A deadline from def or maxd
// has the cause ErrTimeout; one from also has alsoCause, and no cause of its
// own when that is nil (also is then the caller's deadline, carried into a
// transaction's statements).
func bound(ctx context.Context, def, maxd time.Duration, also time.Time, alsoCause error) (context.Context, context.CancelFunc) {
	now := time.Now()
	dl, has := ctx.Deadline()
	var want time.Time
	cause := ErrTimeout
	if !has && def > 0 {
		want = now.Add(def)
	}
	if maxd > 0 {
		if m := now.Add(maxd); want.IsZero() || m.Before(want) {
			want = m
		}
	}
	if !also.IsZero() && (want.IsZero() || also.Before(want)) {
		want, cause = also, alsoCause
	}
	if want.IsZero() || has && !want.Before(dl) {
		return ctx, nil
	}
	if cause == nil {
		return context.WithDeadline(ctx, want)
	}
	return context.WithDeadlineCause(ctx, want, cause)
}

// timedOut reports whether ctx ended because a deadline velox set passed.
func timedOut(ctx context.Context) bool {
	return ctx.Err() != nil && errors.Is(context.Cause(ctx), ErrTimeout)
}

// ctxErr is why ctx ended: its cause, which matches ctx.Err() unless the
// caller gave a cause of its own, and then both.
func ctxErr(ctx context.Context) error {
	err, cause := ctx.Err(), context.Cause(ctx)
	if errors.Is(cause, err) {
		return cause
	}
	return fmt.Errorf("%w: %w", err, cause)
}

// errTimeout is the error of a statement or transaction (who) that ran past
// velox's deadline.
func errTimeout(who string) error {
	return fmt.Errorf("velox: %s: %w: %w", who, ErrTimeout, context.DeadlineExceeded)
}

// timed reports whether a statement on db may get a deadline from velox.
func (db *DB) timed() bool {
	d := db.cfg.deadlines
	return d.Statement > 0 || d.MaxStatement > 0 || !db.txDeadline.IsZero()
}

// bound applies db's statement limits, and its transaction's deadline, to ctx.
func (db *DB) bound(ctx context.Context) (context.Context, context.CancelFunc) {
	return bound(ctx, db.cfg.deadlines.Statement, db.cfg.deadlines.MaxStatement, db.txDeadline, db.txCause)
}

// pastTxDeadline reports whether db is in a transaction whose deadline has
// passed.
func (db *DB) pastTxDeadline() bool {
	return !db.txDeadline.IsZero() && !time.Now().Before(db.txDeadline)
}
