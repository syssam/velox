package engine

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// WithDeadlines sets the DB's time limits. A statement's limit covers its
// Around hooks and, with Iter, the caller's loop body. A transaction's covers
// fn, every TxRetryRolledBack attempt and the commit.
//
// A transaction's effective deadline is velox's or the one on the context
// given to Tx, whichever is earlier. It bounds every statement inside the
// transaction, nested Tx included, whatever context that statement is given,
// and a transaction whose context has ended by commit time is rolled back.
// The error matches ErrTimeout only when velox's deadline was the one that
// passed; the caller's matches context.DeadlineExceeded alone.
func WithDeadlines(d Deadlines) Option { return Option{func(c *config) { c.Deadlines = d }} }

// TxTimeout bounds the transaction to d, retries and commit included, in place
// of the DB's Tx default and within its MaxTx. An earlier deadline on the
// context still applies. TxTimeout(d) with d <= 0 sets no bound of its own:
// the DB's Tx default applies, in place of a TxTimeout in WithTxDefaults. A nested Tx
// refuses it, as it refuses every option: a savepoint runs within the
// enclosing transaction's deadline.
func TxTimeout(d time.Duration) TxOption { return TxOption{func(o *txOptions) { o.Timeout = d }} }

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

// endedErr is err, the error of who run on ctx, saying why ctx ended when it
// has and err does not: ErrTimeout when velox's deadline ended it, else the
// context's own error. The driver's error is kept.
//
// A driver may report a context that ended as an error of its own. pgx's
// database/sql driver reports a statement whose context ended before it was
// sent as driver.ErrBadConn, which database/sql retries outside a
// transaction but returns inside one. pgx also gives the deadline to the
// socket, whose i/o timeout can come before ctx's Done closes, so endedErr
// waits for it as over does.
func endedErr(ctx context.Context, who string, err error) error {
	if err == nil || !over(ctx) {
		return err
	}
	if timedOut(ctx) {
		if errors.Is(err, ErrTimeout) {
			return err
		}
		return errors.Join(timeoutError(who), err)
	}
	why := ctx.Err()
	switch {
	case why == nil:
		why = context.DeadlineExceeded // past its deadline, Done not closed within the grace
	case errors.Is(err, why):
		return err
	default:
		why = ctxErr(ctx)
	}
	return errors.Join(fmt.Errorf("velox: %s: %w", who, why), err)
}

// timeoutError is the error of a statement or transaction (who) that ran past
// velox's deadline.
func timeoutError(who string) error {
	return fmt.Errorf("velox: %s: %w: %w", who, ErrTimeout, context.DeadlineExceeded)
}

// timed reports whether a statement on db may get a deadline from velox.
func (db *DB) timed() bool {
	d := db.Cfg.Deadlines
	return d.Statement > 0 || d.MaxStatement > 0 || !db.txDeadline.IsZero()
}

// bound applies db's statement limits, and its transaction's deadline, to ctx.
func (db *DB) bound(ctx context.Context) (context.Context, context.CancelFunc) {
	return bound(ctx, db.Cfg.Deadlines.Statement, db.Cfg.Deadlines.MaxStatement, db.txDeadline, db.txCause)
}

// pastTxDeadline reports whether db is in a transaction whose deadline has
// passed.
func (db *DB) pastTxDeadline() bool {
	return !db.txDeadline.IsZero() && !time.Now().Before(db.txDeadline)
}
