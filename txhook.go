package velox

import (
	"context"
	"errors"

	"github.com/syssam/velox2/internal/engine"
)

// newTxInfo returns the TxInfo of a transaction with options o on shard sh
// (nil on a DB without a router), or nil when db has no hooks to read it.
func (db *DB) newTxInfo(o engine.TxOptions, sh *engine.ShardConn) *TxInfo {
	if !db.hooked() && len(db.cfg.TxArounds) == 0 {
		return nil
	}
	info := &TxInfo{ID: engine.TxIDs.Add(1), Isolation: o.Isolation, ReadOnly: o.ReadOnly, Route: Route{Replica: -1}}
	if sh != nil {
		info.Route.Shard = sh.Name
	}
	return info
}

// txAround runs attempts through db.cfg.txArounds, the first given outermost.
func (db *DB) txAround(ctx context.Context, info *TxInfo, attempts func(context.Context) error) error {
	called, returned := false, false
	var last error // what attempts returned
	next := func(ctx context.Context) error {
		if called {
			return errors.New("velox: transaction: a TxAround hook called next twice")
		}
		called = true
		last = attempts(ctx)
		returned = true
		return last
	}
	for i := len(db.cfg.TxArounds) - 1; i >= 0; i-- {
		h, inner := db.cfg.TxArounds[i], next
		next = func(ctx context.Context) error { return h(ctx, info, inner) }
	}
	if err := next(ctx); err != nil {
		return err
	}
	if !called {
		return errors.New("velox: transaction: a TxAround hook returned without calling next")
	}
	if !returned {
		// attempts panicked and a hook recovered the panic: velox rolled the
		// transaction back, so Tx must not report success.
		return errors.New("velox: transaction: a TxAround hook recovered a panic from next")
	}
	if last != nil {
		return errors.New("velox: transaction: a TxAround hook discarded the error from next")
	}
	return nil
}
