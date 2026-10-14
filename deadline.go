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
// Around hooks and, with Iter, the caller's loop body; a transaction's covers
// fn, every RetrySerializable attempt and the commit, and bounds every
// statement inside it whatever context that statement is given.
func WithDeadlines(d Deadlines) Option { return func(c *config) { c.deadlines = d } }

// Timeout bounds the transaction to d, retries and commit included, in place
// of the DB's Tx default and within its MaxTx. An earlier deadline on the
// context still applies.
func Timeout(d time.Duration) TxOption { return func(o *txOptions) { o.timeout = d } }

// bound returns ctx with velox's limits applied: def when ctx has no
// deadline, at most maxd from now, and no later than also unless it is zero.
// cancel is nil when ctx is returned unchanged. A deadline velox sets has the
// cause ErrTimeout.
func bound(ctx context.Context, def, maxd time.Duration, also time.Time) (context.Context, context.CancelFunc) {
	now := time.Now()
	dl, has := ctx.Deadline()
	var want time.Time
	if !has && def > 0 {
		want = now.Add(def)
	}
	if maxd > 0 {
		if m := now.Add(maxd); want.IsZero() || m.Before(want) {
			want = m
		}
	}
	if !also.IsZero() && (want.IsZero() || also.Before(want)) {
		want = also
	}
	if want.IsZero() || has && !want.Before(dl) {
		return ctx, nil
	}
	return context.WithDeadlineCause(ctx, want, ErrTimeout)
}

// timedOut reports whether ctx ended because a deadline velox set passed.
func timedOut(ctx context.Context) bool {
	return ctx.Err() != nil && errors.Is(context.Cause(ctx), ErrTimeout)
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
	return bound(ctx, db.cfg.deadlines.Statement, db.cfg.deadlines.MaxStatement, db.txDeadline)
}
