package engine

import (
	"context"
	"fmt"
)

// filterKey is the context key of filterCtx, the context a behaviour
// function is called with.
type filterKey struct{}

// frameKind is what a filterCtx's function is: a WithFilter filter, an
// interceptor's Filter, or a policy's function or rule list.
type frameKind uint8

const (
	kindFilter frameKind = iota
	kindInterceptor
	kindPolicy
)

// behaviourKey names one behaviour function in a frame: an interceptor's
// Filter, or one Policy value's Read, Write, Insert, Query or Mutation. It
// is not empty, so that two keys never share an address.
type behaviourKey struct{ _ byte }

// filterCtx is the context a behaviour function is called with (a
// WithFilter filter, an interceptor's Filter, a policy's function): the
// statement's, carrying the function being called, above the frames of the
// functions whose reads made the statement. One allocation, where
// context.WithValue and a frame would be two.
type filterCtx struct {
	context.Context
	key  any        // the function being called: *EntityFilterFn or *behaviourKey
	up   *filterCtx // the function whose read made this statement; nil for none
	kind frameKind
}

// Value returns the frame for filterKey, and the parent's value otherwise.
func (c *filterCtx) Value(key any) any {
	if _, ok := key.(filterKey); ok {
		return c
	}
	return c.Context.Value(key)
}

// String names the context as the context package's do.
func (c *filterCtx) String() string {
	name := ".WithFilter"
	switch c.kind {
	case kindInterceptor:
		name = ".Interceptor"
	case kindPolicy:
		name = ".Policy"
	}
	return fmt.Sprint(c.Context) + name
}

// frameOf is the innermost frame of ctx; nil outside every behaviour
// function.
func frameOf(ctx context.Context) *filterCtx {
	c, _ := ctx.Value(filterKey{}).(*filterCtx)
	return c
}

// unfiltered is ctx without the filterCtx layers on it: the statement's
// context a behaviour function was given ctx for.
func unfiltered(ctx context.Context) context.Context {
	for {
		c, ok := ctx.(*filterCtx)
		if !ok {
			return ctx
		}
		ctx = c.Context
	}
}

// enter returns ctx with a frame for the function key, of kind. skip is
// true, and there is no frame, when ctx is under a call of key already: a
// statement that function's own reads made, directly or through other
// functions' reads, on whatever DB. Such a statement is not narrowed or
// judged by it, which ends the recursion a function that reads would
// start.
func enter(ctx context.Context, key any, kind frameKind) (_ context.Context, skip bool) {
	up := frameOf(ctx)
	for c := up; c != nil; c = c.up {
		if c.key == key {
			return nil, true
		}
	}
	return &filterCtx{Context: ctx, key: key, up: up, kind: kind}, false
}

// callFilter calls filters[i], of db's WithFilter filters, for entity and op,
// given ctx with a frame of its own and db, the DB the statement runs on.
// skip is true, and f is not called, when ctx is under a call of filter i
// already (enter).
func callFilter(ctx context.Context, db *DB, filters []EntityFilterFn, i int, entity *EntityInfo, op Op) (p Predicate, skip bool, err error) {
	f := &filters[i]
	c, skip := enter(ctx, f, kindFilter)
	if skip {
		return Predicate{}, true, nil
	}
	p, err = (*f)(c, db, entity, op)
	return p, false, err
}

// allowed is a skipped condition's result: no restriction.
func allowed() (Predicate, error) { return NewPredicate(Node{Op: OpAllow}), nil }

// guardCond is f, an interceptor's Filter, called under a frame of its
// own, and skipped, with no restriction, under a call of itself (enter).
func guardCond(f func(ctx context.Context, db *DB) (Predicate, error)) FilterFn {
	if f == nil {
		return nil
	}
	key := new(behaviourKey)
	return func(ctx context.Context, db StmtDB) (Predicate, error) {
		c, skip := enter(ctx, key, kindInterceptor)
		if skip {
			return allowed()
		}
		return f(c, dbOf(db))
	}
}

// guardPolicy is guardCond of a policy's Read or Query condition, which is
// given the statement's DB.
func guardPolicy(f func(ctx context.Context, db StmtDB) (Predicate, error)) func(ctx context.Context, db StmtDB) (Predicate, error) {
	if f == nil {
		return nil
	}
	key := new(behaviourKey)
	return func(ctx context.Context, db StmtDB) (Predicate, error) {
		c, skip := enter(ctx, key, kindPolicy)
		if skip {
			return allowed()
		}
		return f(c, db)
	}
}

// guardWrite is guardCond of a policy's Write condition, which is given the
// write's kind.
func guardWrite(f func(ctx context.Context, db StmtDB, op Op) (Predicate, error)) func(ctx context.Context, db StmtDB, op Op) (Predicate, error) {
	if f == nil {
		return nil
	}
	key := new(behaviourKey)
	return func(ctx context.Context, db StmtDB, op Op) (Predicate, error) {
		c, skip := enter(ctx, key, kindPolicy)
		if skip {
			return allowed()
		}
		return f(c, db, op)
	}
}

// guardInsert is guardCond of a policy's Insert check, which passes when
// skipped.
func guardInsert(f func(ctx context.Context, db StmtDB, r Row) error) func(ctx context.Context, db StmtDB, r Row) error {
	if f == nil {
		return nil
	}
	key := new(behaviourKey)
	return func(ctx context.Context, db StmtDB, r Row) error {
		c, skip := enter(ctx, key, kindPolicy)
		if skip {
			return nil
		}
		return f(c, db, r)
	}
}
