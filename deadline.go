package velox

import (
	"context"
	"time"

	"github.com/syssam/velox2/internal/engine"
)

// timed reports whether a statement on db may get a deadline from velox.
func (db *DB) timed() bool {
	d := db.cfg.Deadlines
	return d.Statement > 0 || d.MaxStatement > 0 || !db.txDeadline.IsZero()
}

// bound applies db's statement limits, and its transaction's deadline, to ctx.
func (db *DB) bound(ctx context.Context) (context.Context, context.CancelFunc) {
	return engine.Bound(ctx, db.cfg.Deadlines.Statement, db.cfg.Deadlines.MaxStatement, db.txDeadline, db.txCause)
}

// pastTxDeadline reports whether db is in a transaction whose deadline has
// passed.
func (db *DB) pastTxDeadline() bool {
	return !db.txDeadline.IsZero() && !time.Now().Before(db.txDeadline)
}
