package velox

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"

	"github.com/syssam/velox/internal/engine"
)

// ErrNotReady is returned by a statement on a DB opened with a Deferred
// whose Set has not been called: the filter and hooks that Set gives are not
// there to judge it, so it is refused. Nothing was sent.
var ErrNotReady = errors.New("velox: a statement ran before Deferred.Set gave the DB its filter and hooks")

// Deferred is a row filter and write hooks that a DB installs when it opens
// and that are given later. They cannot be given at Open when they need a
// service that takes the DB: the DB must exist first. Until Set, the DB
// fails closed: every statement on it is refused with ErrNotReady, where a
// WithFilter and WithHooksAll that were filled later would let everything
// through.
//
//	late := velox.NewDeferred()
//	db, err := velox.Connect("postgres", dsn, late.Option())
//	svc := newService(db)
//	err = late.Set(svc.RowScope, svc.CheckAccess, svc.SyncStatus)
//
// A DB opened without Option has no such wait. Set may be called once, and
// the filter and hooks it gives run as WithFilter and WithHooksAll's do, on
// the DB and every Tx and shard it opens. Statements that run before Set
// fail, a read the service makes at startup included, and WithoutPolicy does
// not lift a filter: call Set before them. A Deferred opens one DB at a time;
// give each DB its own.
//
// It is safe for concurrent use.
type Deferred struct{ set atomic.Pointer[deferredSet] }

// deferredSet is what Set gave.
type deferredSet struct {
	filter Filter
	hooks  []Hook
}

// NewDeferred returns a Deferred that is not set.
func NewDeferred() *Deferred { return new(Deferred) }

// Option is the Option that installs d on a DB: a filter and a hook for every
// entity, which refuse until Set and then call what Set gave. It is
// WithFilter and WithHooksAll over d.
func (d *Deferred) Option() Option {
	return engine.CombineOptions(WithFilter(d.filter), WithHooksAll(d.hook))
}

// Set gives the DB filter, which may be nil for none, and hs, the first
// outermost, and ends the refusals. It returns an error when it was called
// before, and then changes nothing. A nil hook panics, as WithHooksAll's does.
func (d *Deferred) Set(filter Filter, hs ...Hook) error {
	for _, h := range hs {
		if h == nil {
			panic("velox: Deferred.Set: a nil hook")
		}
	}
	if !d.set.CompareAndSwap(nil, &deferredSet{filter: filter, hooks: append([]Hook(nil), hs...)}) {
		return errors.New("velox: Deferred.Set was called before")
	}
	return nil
}

// filter is d's Filter: it refuses until Set, then asks what Set gave.
func (d *Deferred) filter(ctx context.Context, db *DB, e FilterEntity, op Op) (Predicate, error) {
	s := d.set.Load()
	if s == nil {
		return Predicate{}, ErrNotReady
	}
	if s.filter == nil {
		return Allow(), nil
	}
	return s.filter(ctx, db, e, op)
}

// hook is d's Hook: until Set a write fails; after it, it runs what Set gave
// as one chain, the first outermost. It is called on every write, as every
// hook is.
func (d *Deferred) hook(next Mutator) Mutator {
	s := d.set.Load()
	if s == nil {
		return MutateFunc(func(context.Context, *Mutation) error { return ErrNotReady })
	}
	for _, h := range slices.Backward(s.hooks) {
		next = h(next)
	}
	return next
}
