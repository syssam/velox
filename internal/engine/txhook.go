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

// txAround runs attempts through db.Cfg.txArounds, the first given outermost,
// and returns the AfterCommit functions of the attempt that committed, which
// attempts sets through its second argument; it returns them with an error
// too, as the transaction committed whatever a hook then returned. Its state
// is one struct, so that the closure costs one allocation for all of it.
func (db *DB) txAround(ctx context.Context, info *TxInfo, attempts func(context.Context, *[]func(context.Context)) error) ([]func(context.Context), error) {
	var s struct {
		called, returned bool
		last             error // what attempts returned
		after            []func(context.Context)
	}
	next := func(ctx context.Context) error {
		if s.called {
			return errors.New("velox: transaction: a TxAround hook called next twice")
		}
		s.called = true
		s.last = attempts(ctx, &s.after)
		s.returned = true
		return s.last
	}
	for _, v := range slices.Backward(db.Cfg.TxArounds) {
		h, inner := v, next
		next = func(ctx context.Context) error { return h(ctx, info, inner) }
	}
	if err := next(ctx); err != nil {
		return s.after, err
	}
	if !s.called {
		return nil, errors.New("velox: transaction: a TxAround hook returned without calling next")
	}
	if !s.returned {
		// attempts panicked and a hook recovered the panic: velox rolled the
		// transaction back, so Tx must not report success.
		return nil, errors.New("velox: transaction: a TxAround hook recovered a panic from next")
	}
	if s.last != nil {
		return nil, errors.New("velox: transaction: a TxAround hook discarded the error from next")
	}
	return s.after, nil
}
