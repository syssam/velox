package engine

import (
	"context"
	"fmt"
)

// filterKey is the context key of filterCtx, the context a WithFilter filter
// is called with.
type filterKey struct{}

// filterCtx is the context a WithFilter filter is called with: the
// statement's, carrying the DB the statement runs on (FilterDB) and the
// filter being called, above the frames of the filters whose reads made
// the statement. One allocation, where context.WithValue and a frame would
// be two.
type filterCtx struct {
	context.Context
	db *DB
	f  *EntityFilterFn // the filter being called: its place in the shared config
	up *filterCtx      // the filter whose read made this statement; nil for none
}

// Value returns the frame for filterKey, and the parent's value otherwise.
func (c *filterCtx) Value(key any) any {
	if _, ok := key.(filterKey); ok {
		return c
	}
	return c.Context.Value(key)
}

// String names the context as the context package's do.
func (c *filterCtx) String() string { return fmt.Sprint(c.Context) + ".WithFilterDB" }

// frameOf is the innermost filter frame of ctx; nil outside every filter.
func frameOf(ctx context.Context) *filterCtx {
	c, _ := ctx.Value(filterKey{}).(*filterCtx)
	return c
}

// unfiltered is ctx without the filterCtx layers on it: the statement's
// context a filter was given ctx for.
func unfiltered(ctx context.Context) context.Context {
	for {
		c, ok := ctx.(*filterCtx)
		if !ok {
			return ctx
		}
		ctx = c.Context
	}
}

// FilterDB returns the DB of the statement whose WithFilter filter is being
// called under ctx: the DB the statement runs on, which is the transaction's
// inside Tx. It returns nil for a ctx no filter was given.
func FilterDB(ctx context.Context) *DB {
	if c := frameOf(ctx); c != nil {
		return c.db
	}
	return nil
}

// callFilter calls filters[i], of db's WithFilter filters, for entity and op,
// given ctx with the frame FilterDB reads. skip is true, and f is not
// called, when ctx is under a call of filter i already: a statement that
// filter's own reads made, directly or through other filters' reads. Such a
// statement is not narrowed by it, which ends the recursion a filter that
// reads would start.
func callFilter(ctx context.Context, db *DB, filters []EntityFilterFn, i int, entity string, op Op) (p Predicate, skip bool, err error) {
	f := &filters[i]
	up := frameOf(ctx)
	for c := up; c != nil; c = c.up {
		if c.f == f {
			return Predicate{}, true, nil
		}
	}
	p, err = (*f)(&filterCtx{Context: ctx, db: db, f: f, up: up}, entity, op)
	return p, false, err
}
