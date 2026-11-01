package engine

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
)

// WithTxAround adds a transaction hook. The first one given is the
// outermost. A nil hook panics.
func WithTxAround(h TxAround) Option {
	if h == nil {
		panic("velox: WithTxAround(nil)")
	}
	return Option{func(c *config) { c.TxArounds = append(c.TxArounds, h) }}
}

// txIDs numbers transactions; the first is 1.
var txIDs atomic.Uint64

// end records how the transaction's attempt ended; info may be nil.
func endTx(info *TxInfo, o TxOutcome) {
	if info != nil {
		info.Outcome = o
	}
}

// newTxInfo returns the TxInfo of a transaction with options o on shard sh
// (nil on a DB without a router), or nil when db has no hooks to read it.
func (db *DB) newTxInfo(o txOptions, sh *shardConn) *TxInfo {
	if !db.Hooked() && len(db.Cfg.TxArounds) == 0 {
		return nil
	}
	info := &TxInfo{ID: txIDs.Add(1), Isolation: o.Isolation, ReadOnly: o.ReadOnly, Route: Route{Replica: -1}}
	if sh != nil {
		info.Route.Shard = sh.Name
	}
	return info
}

// txAround runs attempts through db.Cfg.txArounds, the first given outermost.
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
	for _, v := range slices.Backward(db.Cfg.TxArounds) {
		h, inner := v, next
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
